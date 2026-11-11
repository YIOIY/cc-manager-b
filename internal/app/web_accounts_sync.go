package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"cc-manager/internal/errs"
	"cc-manager/internal/supplier"
)

// Account sync over the supplier web console: every 10 minutes the full
// account list is read, mirrored into platform_remote_accounts, linked to
// local accounts (creating the ones the platform has and we do not), and the
// usage block is kept as a change-only history.

const (
	webAccountsInterval = 10 * time.Minute
	webReasonRefresh    = 6 * time.Hour
)

// webFailureStatuses are the console statuses that carry an explanation
// worth fetching and that make a delivery a worse "current" candidate.
var webFailureStatuses = map[string]bool{"ban": true, "banned": true, "check_fail": true, "login_failed": true, "bound_failed": true, "cooking_failed": true, "re_login_failed": true, "sync_failed": true, "fault": true, "suspended": true, "arrearage": true}

func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func webStatusOf(r supplier.WebAccount) string {
	if r.ExtStatus != "" {
		return r.ExtStatus
	}
	return r.A1Status
}

func isWebFailure(r supplier.WebAccount) bool {
	return webFailureStatuses[r.ExtStatus] || webFailureStatuses[r.A1Status]
}

// chooseCurrentDelivery picks, among the deliveries of one email, the one the
// local account should follow: a non-failed one over a failed one, then the
// most recently created.
func chooseCurrentDelivery(rows []supplier.WebAccount) supplier.WebAccount {
	best := rows[0]
	for _, r := range rows[1:] {
		bf, rf := isWebFailure(best), isWebFailure(r)
		switch {
		case bf && !rf:
			best = r
		case bf == rf && (r.CreatedAt > best.CreatedAt || (r.CreatedAt == best.CreatedAt && r.ID > best.ID)):
			best = r
		}
	}
	return best
}

func (a *App) syncAllWebAccounts(ctx context.Context) error {
	ids, err := a.webSyncCandidates(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		if _, _, skip, e := a.syncBackoff(ctx, id, "web-accounts", time.Now()); e != nil {
			failures = append(failures, e)
			continue
		} else if skip {
			continue
		}
		if e := a.syncWebAccounts(ctx, id); e != nil {
			failures = append(failures, e)
		}
	}
	return errors.Join(failures...)
}

// webSyncCandidates lists the enabled connections whose web channel is
// configured and not suspended.
func (a *App) webSyncCandidates(ctx context.Context) ([]string, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT id FROM platform_connections WHERE enabled=1 AND web_username<>'' AND web_password_secret<>'' AND web_suspended_at='' ORDER BY created_at,rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// startWebRun inserts the running sync_runs row and returns the finisher.
func (a *App) startWebRun(ctx context.Context, connectionID, kind string) (string, func(state, code, message, request string, processed, failed int)) {
	started := now()
	runID := newID()
	if _, err := a.DB.ExecContext(ctx, `INSERT INTO sync_runs(id,connection_id,kind,state,started_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, runID, connectionID, kind, "running", started, started, started); err != nil {
		slog.Error("sync run insert failed", "kind", kind, "error", err)
	}
	return runID, func(state, code, message, request string, processed, failed int) {
		success := ""
		if state == "succeeded" {
			success = now()
		}
		if request == "" {
			request = "{}"
		}
		if _, err := a.DB.ExecContext(ctx, `UPDATE sync_runs SET state=?,finished_at=?,last_success_at=?,error_code=?,error_message=?,request_json=?,processed=?,failed=?,updated_at=? WHERE id=?`, state, now(), success, code, sanitizeUpstream(message), request, processed, failed, now(), runID); err != nil {
			slog.Error("sync run finish failed", "run", runID, "error", err)
		}
	}
}

// syncWebAccounts runs one account sync for a connection. Failures that
// change nothing (login, empty list) are recorded on the run and returned;
// per-account problems are counted and the run ends partial.
func (a *App) syncWebAccounts(ctx context.Context, connectionID string) error {
	lock := a.webSyncLock(connectionID)
	if !lock.TryLock() {
		return nil
	}
	defer lock.Unlock()
	_, finish := a.startWebRun(ctx, connectionID, "web-accounts")
	client, _, err := a.webClientFor(ctx, connectionID)
	if err != nil {
		finish("failed", upstreamCode(err), err.Error(), "", 0, 0)
		return err
	}
	listCtx, cancel := context.WithTimeout(ctx, 2*supplierRequestTimeout)
	accounts, counts, total, err := client.ListAccounts(listCtx)
	cancel()
	if err != nil {
		a.noteWebLogin(ctx, connectionID, err)
		finish("failed", upstreamCode(err), err.Error(), "", 0, 0)
		return err
	}
	if total == 0 || len(accounts) == 0 {
		err = errors.New("supplier web account list is empty")
		finish("failed", errs.CodePlatformResponseInvalid, err.Error(), "", 0, 0)
		return err
	}
	request, _ := json.Marshal(map[string]any{"counts": counts, "total": total, "listed": len(accounts)})

	observed := now()
	processed, failed := 0, 0
	seen := make([]string, 0, len(accounts))
	byEmail := map[string][]supplier.WebAccount{}
	var emails []string
	var anonymous []supplier.WebAccount
	for _, r := range accounts {
		seen = append(seen, fmt.Sprint(r.ID))
		email := normalizeEmail(r.Email)
		if email == "" {
			anonymous = append(anonymous, r)
			continue
		}
		if _, ok := byEmail[email]; !ok {
			emails = append(emails, email)
		}
		byEmail[email] = append(byEmail[email], r)
	}
	sort.Strings(emails)
	for _, email := range emails {
		group := byEmail[email]
		current := chooseCurrentDelivery(group)
		accountID, created, err := a.linkRemoteAccount(ctx, connectionID, current, email, observed)
		if err != nil {
			slog.Error("web account link failed", "connection", connectionID, "upstream", current.ID, "error", err)
			failed += len(group)
			continue
		}
		for _, r := range group {
			superseded := ""
			if r.ID != current.ID {
				superseded = fmt.Sprint(current.ID)
			}
			if err := a.upsertRemoteAccount(ctx, connectionID, r, accountID, created && r.ID == current.ID, superseded, observed); err != nil {
				slog.Error("remote account upsert failed", "connection", connectionID, "upstream", r.ID, "error", err)
				failed++
				continue
			}
			processed++
		}
	}
	for _, r := range anonymous {
		var accountID string
		_ = a.DB.QueryRowContext(ctx, `SELECT account_id FROM platform_links WHERE connection_id=? AND upstream_id=?`, connectionID, fmt.Sprint(r.ID)).Scan(&accountID)
		if err := a.upsertRemoteAccount(ctx, connectionID, r, accountID, false, "", observed); err != nil {
			slog.Error("remote account upsert failed", "connection", connectionID, "upstream", r.ID, "error", err)
			failed++
			continue
		}
		processed++
	}
	// Everything the console no longer lists is marked, never deleted.
	args := []any{observed, connectionID}
	for _, id := range seen {
		args = append(args, id)
	}
	if _, err := a.DB.ExecContext(ctx, `UPDATE platform_remote_accounts SET missing_since=? WHERE connection_id=? AND missing_since='' AND upstream_id NOT IN (`+marks(len(seen))+`)`, args...); err != nil {
		slog.Error("remote account missing mark failed", "connection", connectionID, "error", err)
		failed++
	}
	failed += a.refreshWebReasons(ctx, client, connectionID, accounts)
	state := "succeeded"
	if failed > 0 {
		state = "partial"
	}
	finish(state, "", "", string(request), processed, failed)
	return nil
}

// linkRemoteAccount makes sure the current delivery of an email has a local
// account and a platform_links row pointing at it, creating or re-pointing
// as needed, in one transaction. created reports an auto-created account.
func (a *App) linkRemoteAccount(ctx context.Context, connectionID string, r supplier.WebAccount, email, observed string) (accountID string, created bool, err error) {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	upstream := fmt.Sprint(r.ID)
	aid := ""
	if r.AID > 0 {
		aid = fmt.Sprint(r.AID)
	}
	status := webStatusOf(r)
	// 1. Already linked to this delivery: refresh the observation.
	if err = tx.QueryRow(`SELECT account_id FROM platform_links WHERE connection_id=? AND upstream_id=?`, connectionID, upstream).Scan(&accountID); err == nil {
		if _, err = tx.Exec(`UPDATE platform_links SET upstream_aid=?,alias=?,runtime_status=?,runtime_status_raw=?,last_observed_at=?,ingestion_state='accepted',updated_at=? WHERE connection_id=? AND upstream_id=?`, aid, r.Alias, status, status, observed, observed, connectionID, upstream); err != nil {
			return "", false, err
		}
		return accountID, false, tx.Commit()
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	// 2. A local account with this email: link it, re-pointing an older link.
	err = tx.QueryRow(`SELECT id FROM accounts WHERE lower(trim(account))=? ORDER BY archived,created_at LIMIT 1`, email).Scan(&accountID)
	switch {
	case err == nil:
		var old string
		switch e := tx.QueryRow(`SELECT upstream_id FROM platform_links WHERE account_id=? AND connection_id=?`, accountID, connectionID).Scan(&old); {
		case e == nil:
			if _, err = tx.Exec(`UPDATE platform_links SET upstream_id=?,upstream_aid=?,alias=?,runtime_status=?,runtime_status_raw=?,last_observed_at=?,ingestion_state='accepted',updated_at=? WHERE account_id=? AND connection_id=?`, upstream, aid, r.Alias, status, status, observed, observed, accountID, connectionID); err != nil {
				return "", false, err
			}
			if _, err = tx.Exec(`UPDATE platform_remote_accounts SET superseded_by=? WHERE connection_id=? AND upstream_id=?`, upstream, connectionID, old); err != nil {
				return "", false, err
			}
			if err = tx.Commit(); err != nil {
				return "", false, err
			}
			a.audit("account.platform_relink", "account", accountID, old+"->"+upstream)
			return accountID, false, nil
		case errors.Is(e, sql.ErrNoRows):
			if _, err = tx.Exec(`INSERT INTO platform_links(id,account_id,connection_id,upstream_id,upstream_aid,alias,runtime_status,runtime_status_raw,last_observed_at,ingestion_state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,'accepted',?,?)`, newID(), accountID, connectionID, upstream, aid, r.Alias, status, status, observed, observed, observed); err != nil {
				return "", false, err
			}
			return accountID, false, tx.Commit()
		default:
			return "", false, e
		}
	case errors.Is(err, sql.ErrNoRows):
		// 3. Unknown locally: create the account from the console's profile.
		accountID, err = autoCreateAccount(tx, connectionID, r, observed)
		if err != nil {
			return "", false, err
		}
		if _, err = tx.Exec(`INSERT INTO platform_links(id,account_id,connection_id,upstream_id,upstream_aid,alias,runtime_status,runtime_status_raw,last_observed_at,ingestion_state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,'accepted',?,?)`, newID(), accountID, connectionID, upstream, aid, r.Alias, status, status, observed, observed, observed); err != nil {
			return "", false, err
		}
		if err = tx.Commit(); err != nil {
			return "", false, err
		}
		a.audit("account.platform_import", "account", accountID, upstream)
		return accountID, true, nil
	default:
		return "", false, err
	}
}

// autoCreateAccount inserts a local account from a console row. Values the
// local validators would reject are left empty rather than stored raw; the
// platform's own values stay visible through the remote snapshot.
func autoCreateAccount(tx *sql.Tx, connectionID string, r supplier.WebAccount, at string) (string, error) {
	p := r.Profile
	plan := p.Plan
	if !validPlan(plan) {
		plan = ""
	}
	date := func(s string) string {
		if validDate(s) {
			return s
		}
		return ""
	}
	raw := map[string]any{"balance": p.Balance, "payment_method": p.PaymentMethod, "register_device": p.RegisterDevice, "recharge_time": p.RechargeTime, "warranty_period": p.WarrantyPeriod, "remark": r.Remark}
	if len(p.Tags) > 0 {
		raw["tags"] = p.Tags
	}
	metadata := pickMetadata(raw)
	metadata["source"] = "platform_sync"
	metadata["platform_connection_id"] = connectionID
	metadata["platform_upstream_id"] = fmt.Sprint(r.ID)
	metadata["imported_at"] = at
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	id := newID()
	_, err = tx.Exec(`INSERT INTO accounts(id,account,plan,register_location,register_time,plan_start_time,plan_end_time,proxy_id,metadata_json,credentials_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,'',?,'{}',?,?)`, id, strings.TrimSpace(r.Email), plan, p.RegisterLocation, date(p.RegisterTime), date(p.PlanStart), date(p.PlanEnd), string(encoded), at, at)
	return id, err
}

// upsertRemoteAccount mirrors one console row and appends a usage snapshot
// when the usage block changed. auto marks a row whose local account was
// created by this sync (sticky once set).
func (a *App) upsertRemoteAccount(ctx context.Context, connectionID string, r supplier.WebAccount, accountID string, auto bool, supersededBy, observed string) error {
	upstream := fmt.Sprint(r.ID)
	aid := ""
	if r.AID > 0 {
		aid = fmt.Sprint(r.AID)
	}
	tags, _ := json.Marshal(append([]string{}, r.Profile.Tags...))
	models := "[]"
	if r.ModelsUsable {
		models = `[{"name":` + fmt.Sprintf("%q", r.Model) + `,"usable":true}]`
	}
	var cost, in, out any
	var fiveHour, week any
	fiveReset, weekReset, usageAt := "", "", ""
	if u := r.Usage; u != nil {
		cost, in, out, fiveHour, week = u.CostMicro, u.InputTokens, u.OutputTokens, u.FiveHourUsed, u.WeekUsed
		if !u.FiveHourResetAt.IsZero() {
			fiveReset = u.FiveHourResetAt.UTC().Format(time.RFC3339)
		}
		if !u.WeekResetAt.IsZero() {
			weekReset = u.WeekResetAt.UTC().Format(time.RFC3339)
		}
		usageAt = observed
	}
	locked := 0
	if r.Locked {
		locked = 1
	}
	autoFlag := 0
	if auto {
		autoFlag = 1
	}
	_, err := a.DB.ExecContext(ctx, `INSERT INTO platform_remote_accounts(connection_id,upstream_id,upstream_aid,account_email,alias,provider,model,models_json,ext_status,a1_status,a2_status,is_locked,proxy_type,usage_limit_json,remark,remote_created_at,remote_updated_at,first_seen_at,last_seen_at,missing_since,superseded_by,account_id,auto_created,plan,plan_start_time,plan_end_time,register_time,register_location,balance,payment_method,register_device,recharge_time,warranty_period,tags_json,usage_cost_micro,usage_input_tokens,usage_output_tokens,five_hour_used,five_hour_reset_at,week_used,week_reset_at,usage_observed_at,raw_json)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'',?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(connection_id,upstream_id) DO UPDATE SET upstream_aid=excluded.upstream_aid,account_email=excluded.account_email,alias=excluded.alias,provider=excluded.provider,model=excluded.model,models_json=excluded.models_json,ext_status=excluded.ext_status,a1_status=excluded.a1_status,a2_status=excluded.a2_status,is_locked=excluded.is_locked,proxy_type=excluded.proxy_type,usage_limit_json=excluded.usage_limit_json,remark=excluded.remark,remote_created_at=excluded.remote_created_at,remote_updated_at=excluded.remote_updated_at,last_seen_at=excluded.last_seen_at,missing_since='',superseded_by=excluded.superseded_by,account_id=CASE WHEN excluded.account_id<>'' THEN excluded.account_id ELSE platform_remote_accounts.account_id END,auto_created=MAX(platform_remote_accounts.auto_created,excluded.auto_created),plan=excluded.plan,plan_start_time=excluded.plan_start_time,plan_end_time=excluded.plan_end_time,register_time=excluded.register_time,register_location=excluded.register_location,balance=excluded.balance,payment_method=excluded.payment_method,register_device=excluded.register_device,recharge_time=excluded.recharge_time,warranty_period=excluded.warranty_period,tags_json=excluded.tags_json,usage_cost_micro=excluded.usage_cost_micro,usage_input_tokens=excluded.usage_input_tokens,usage_output_tokens=excluded.usage_output_tokens,five_hour_used=excluded.five_hour_used,five_hour_reset_at=excluded.five_hour_reset_at,week_used=excluded.week_used,week_reset_at=excluded.week_reset_at,usage_observed_at=CASE WHEN excluded.usage_observed_at<>'' THEN excluded.usage_observed_at ELSE platform_remote_accounts.usage_observed_at END,raw_json=excluded.raw_json`,
		connectionID, upstream, aid, strings.TrimSpace(r.Email), r.Alias, r.Provider, r.Model, models, r.ExtStatus, r.A1Status, r.A2Status, locked, r.ProxyType, r.UsageLimitJSON, r.Remark, r.CreatedAt, r.UpdatedAt, observed, observed, supersededBy, accountID, autoFlag,
		r.Profile.Plan, r.Profile.PlanStart, r.Profile.PlanEnd, r.Profile.RegisterTime, r.Profile.RegisterLocation, r.Profile.Balance, r.Profile.PaymentMethod, r.Profile.RegisterDevice, r.Profile.RechargeTime, r.Profile.WarrantyPeriod, string(tags),
		cost, in, out, fiveHour, fiveReset, week, weekReset, usageAt, r.RawJSON)
	if err != nil {
		return err
	}
	if r.Usage == nil {
		return nil
	}
	return a.recordUsageSnapshot(ctx, connectionID, upstream, accountID, observed, r.Usage, fiveReset, weekReset)
}

// recordUsageSnapshot appends a row only when any usage value differs from
// the newest snapshot of the same delivery.
func (a *App) recordUsageSnapshot(ctx context.Context, connectionID, upstream, accountID, observed string, u *supplier.WebUsage, fiveReset, weekReset string) error {
	var lastCost, lastIn, lastOut sql.NullInt64
	var lastFive, lastWeek sql.NullFloat64
	var lastFiveReset, lastWeekReset string
	err := a.DB.QueryRowContext(ctx, `SELECT usage_cost_micro,usage_input_tokens,usage_output_tokens,five_hour_used,five_hour_reset_at,week_used,week_reset_at FROM platform_usage_snapshots WHERE connection_id=? AND upstream_id=? ORDER BY observed_at DESC,rowid DESC LIMIT 1`, connectionID, upstream).Scan(&lastCost, &lastIn, &lastOut, &lastFive, &lastFiveReset, &lastWeek, &lastWeekReset)
	if err == nil && lastCost.Valid && lastCost.Int64 == u.CostMicro && lastIn.Int64 == u.InputTokens && lastOut.Int64 == u.OutputTokens && lastFive.Float64 == u.FiveHourUsed && lastWeek.Float64 == u.WeekUsed && lastFiveReset == fiveReset && lastWeekReset == weekReset {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = a.DB.ExecContext(ctx, `INSERT INTO platform_usage_snapshots(id,connection_id,upstream_id,account_id,observed_at,usage_cost_micro,usage_input_tokens,usage_output_tokens,five_hour_used,five_hour_reset_at,week_used,week_reset_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, newID(), connectionID, upstream, accountID, observed, u.CostMicro, u.InputTokens, u.OutputTokens, u.FiveHourUsed, fiveReset, u.WeekUsed, weekReset)
	return err
}

// refreshWebReasons fetches the console's explanations for accounts in a
// failure status when none is stored, the status changed, or the stored one
// is older than webReasonRefresh. Returns the number of accounts whose
// reasons could not be read.
func (a *App) refreshWebReasons(ctx context.Context, client *supplier.WebClient, connectionID string, accounts []supplier.WebAccount) int {
	failed := 0
	cutoff := time.Now().Add(-webReasonRefresh).UTC().Format(time.RFC3339)
	for _, r := range accounts {
		if !isWebFailure(r) {
			continue
		}
		upstream := fmt.Sprint(r.ID)
		status := webStatusOf(r)
		var fetchedAt, fetchedFor string
		if err := a.DB.QueryRowContext(ctx, `SELECT reason_fetched_at,reason_status FROM platform_remote_accounts WHERE connection_id=? AND upstream_id=?`, connectionID, upstream).Scan(&fetchedAt, &fetchedFor); err != nil {
			continue
		}
		if fetchedAt != "" && fetchedFor == status && fetchedAt > cutoff {
			continue
		}
		kinds := []string{"claude-status", "check-fail"}
		if status == "check_fail" || strings.HasSuffix(status, "_failed") {
			kinds = []string{"check-fail", "claude-status"}
		}
		text, kind := "", ""
		raws := map[string]string{}
		errorsSeen := 0
		for _, k := range kinds {
			reasonCtx, cancel := context.WithTimeout(ctx, supplierRequestTimeout)
			reason, err := client.AccountReason(reasonCtx, r.ID, k)
			cancel()
			if err != nil {
				errorsSeen++
				continue
			}
			raws[k] = reason.Raw
			if text == "" && reason.Text != "" {
				text, kind = reason.Text, k
			}
		}
		if errorsSeen == len(kinds) {
			failed++
			continue
		}
		raw, _ := json.Marshal(raws)
		if _, err := a.DB.ExecContext(ctx, `UPDATE platform_remote_accounts SET reason_kind=?,reason_text=?,reason_raw=?,reason_fetched_at=?,reason_status=? WHERE connection_id=? AND upstream_id=?`, kind, text, string(raw), now(), status, connectionID, upstream); err != nil {
			failed++
		}
	}
	return failed
}
