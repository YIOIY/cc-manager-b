package app

import (
	"cc-manager/internal/errs"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"cc-manager/internal/supplier"
)

type submitJob struct {
	ConnectionID string `json:"connection_id"`
	Version      int    `json:"version"`
}

const (
	supplierRequestTimeout = 35 * time.Second
	metricsSyncInterval    = 2 * time.Minute
	webFirstTickDelay      = 30 * time.Second
	billScheduleInterval   = time.Minute
	workerPollInterval     = time.Second
)

// startWorker launches the background loops. Every loop observes ctx so
// Close can stop accepting work and wait for an in-flight supplier call.
func (a *App) startWorker(ctx context.Context) {
	// A process stopped after sending a request cannot safely retry it.
	if _, err := a.DB.Exec(`UPDATE jobs SET state='unknown',result_json='{"code":"WORKER_RESTART_UNKNOWN"}',updated_at=? WHERE state='running'`, now()); err != nil {
		slog.Error("marking interrupted jobs failed", "error", err)
	}
	if _, err := a.DB.Exec(`UPDATE deliveries SET ingestion_state='unknown',updated_at=? WHERE ingestion_state='running'`, now()); err != nil {
		slog.Error("marking interrupted deliveries failed", "error", err)
	}
	a.wg.Add(6)
	go func() {
		defer a.wg.Done()
		a.loop(ctx, workerPollInterval, safeTick("jobs", func() {
			a.runOne(ctx)
			a.ticks.jobs.Store(time.Now().Unix())
		}))
	}()
	go func() {
		defer a.wg.Done()
		a.loop(ctx, metricsSyncInterval, safeTick("metrics", func() {
			if err := a.syncAllPlatformUsage(ctx); err != nil {
				slog.Warn("metrics sync failed", "error", err)
			}
			a.ticks.metrics.Store(time.Now().Unix())
		}))
	}()
	go func() {
		defer a.wg.Done()
		a.loop(ctx, workerPollInterval, safeTick("bills", func() {
			if err := a.runBillJob(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("bill worker failed", "error", err)
			}
			a.ticks.bills.Store(time.Now().Unix())
		}))
	}()
	go func() {
		defer a.wg.Done()
		a.loopAfter(ctx, webFirstTickDelay, webAccountsInterval, safeTick("web-accounts", func() {
			if err := a.syncAllWebAccounts(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("web account sync failed", "error", err)
			}
			a.ticks.webAccounts.Store(time.Now().Unix())
		}))
	}()
	go func() {
		defer a.wg.Done()
		a.loopAfter(ctx, 2*webFirstTickDelay, webBillsInterval, safeTick("web-bills", func() {
			if err := a.syncAllWebBills(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("web bill sync failed", "error", err)
			}
			a.ticks.webBills.Store(time.Now().Unix())
		}))
	}()
	go func() {
		defer a.wg.Done()
		a.loop(ctx, billScheduleInterval, safeTick("schedule", func() {
			if err := a.scheduleBills(ctx); err != nil {
				slog.Warn("bill scheduling failed", "error", err)
			}
			a.ticks.schedule.Store(time.Now().Unix())
		}))
	}()
}

func (a *App) runOne(ctx context.Context) {
	var id, kind, accountID, payload string
	err := a.DB.QueryRowContext(ctx, `SELECT id,kind,account_id,payload_json FROM jobs WHERE state='queued' ORDER BY created_at,rowid LIMIT 1`).Scan(&id, &kind, &accountID, &payload)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, context.Canceled) {
			slog.Error("job lookup failed", "error", err)
		}
		return
	}
	res, err := a.DB.ExecContext(ctx, `UPDATE jobs SET state='running',updated_at=? WHERE id=? AND state='queued'`, now(), id)
	if err != nil {
		slog.Error("job claim failed", "job", id, "error", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return
	}
	if kind != "account.submit" {
		a.finishJob(id, "failed", "UNSUPPORTED_JOB")
		return
	}
	var spec submitJob
	if json.Unmarshal([]byte(payload), &spec) != nil {
		a.finishJob(id, "failed", "JOB_PAYLOAD_INVALID")
		return
	}
	// Any failure after the delivery is marked running must leave it in a
	// terminal state, otherwise the account card shows "submitting" forever.
	failed := func(code string) {
		a.finishJob(id, "failed", code)
		a.setDelivery(accountID, spec.ConnectionID, "failed", "")
	}
	var accepted int
	_ = a.DB.QueryRowContext(ctx, `SELECT 1 FROM platform_links WHERE account_id=? AND connection_id=? AND ingestion_state='accepted'`, accountID, spec.ConnectionID).Scan(&accepted)
	if accepted == 1 {
		a.finishJob(id, "succeeded", "ALREADY_LINKED")
		return
	}
	var uncertain int
	_ = a.DB.QueryRowContext(ctx, `SELECT 1 FROM deliveries WHERE account_id=? AND connection_id=? AND ingestion_state='unknown'`, accountID, spec.ConnectionID).Scan(&uncertain)
	if uncertain == 1 {
		a.finishJob(id, "unknown", "SUBMIT_OUTCOME_UNKNOWN")
		return
	}
	a.setDelivery(accountID, spec.ConnectionID, "running", "")
	var account, plan, location, registered, start, end, credential, metadataJSON string
	var version int
	err = a.DB.QueryRowContext(ctx, `SELECT account,plan,register_location,register_time,plan_start_time,plan_end_time,credentials_json,metadata_json,version FROM accounts WHERE id=?`, accountID).Scan(&account, &plan, &location, &registered, &start, &end, &credential, &metadataJSON, &version)
	if err != nil || version != spec.Version {
		failed("VERSION_CONFLICT")
		return
	}
	var base, tokenSecret string
	var enabled int
	if a.DB.QueryRowContext(ctx, `SELECT base_url,token_secret,enabled FROM platform_connections WHERE id=?`, spec.ConnectionID).Scan(&base, &tokenSecret, &enabled) != nil || enabled == 0 {
		failed("CONNECTION_DISABLED")
		return
	}
	if a.keys == nil {
		failed("ENCRYPTION_KEY_REQUIRED")
		return
	}
	decode := a.openSecret
	credentialBytes, err := decode(credential, accountID)
	if err != nil {
		failed("CREDENTIAL_UNREADABLE")
		return
	}
	var auth map[string]any
	if json.Unmarshal(credentialBytes, &auth) != nil {
		failed("CREDENTIAL_UNREADABLE")
		return
	}
	var proxyID string
	_ = a.DB.QueryRowContext(ctx, `SELECT proxy_id FROM accounts WHERE id=?`, accountID).Scan(&proxyID)
	if proxyID != "" {
		proxyURL, e := a.resolveProxyURL(proxyID, "")
		if e != nil {
			failed("PROXY_INVALID")
			return
		}
		// Local reachability and the supplier's network are independent.
		// Manual pool checks never gate a remote submission.
		auth["proxy_url"] = proxyURL
	} else if url, _ := auth["proxy_url"].(string); strings.TrimSpace(url) == "" {
		// Pool binding was cleared (e.g. shared-proxy unbind on assign) and
		// credentials no longer carry a proxy_url — block until rebound.
		failed("PROXY_REQUIRED")
		return
	}
	tokenBytes, err := decode(tokenSecret, spec.ConnectionID)
	if err != nil {
		a.finishJobErr(id, "failed", errs.New(errs.CodeConnectionUnreadable).WithDetail("secret", "connection_token"))
		a.setDelivery(accountID, spec.ConnectionID, "failed", "")
		return
	}
	var passwordSecret string
	_ = a.DB.QueryRowContext(ctx, `SELECT password_secret FROM account_secrets WHERE account_id=?`, accountID).Scan(&passwordSecret)
	password := ""
	if passwordSecret != "" {
		p, e := decode(passwordSecret, accountID)
		if e != nil {
			failed("PASSWORD_UNREADABLE")
			return
		}
		password = string(p)
	}
	client, err := supplier.New(base, string(tokenBytes))
	if err != nil {
		failed("CONNECTION_INVALID")
		return
	}
	// The supplier call must finish even when shutdown begins: an aborted
	// request has an unknown outcome and would block the account.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), supplierRequestTimeout)
	defer cancel()
	upstreamPayload := map[string]any{"plan": plan, "account": account, "password": password, "auth_json": auth, "register_location": location, "register_time": registered, "plan_start_time": start, "plan_end_time": end}
	var metadata map[string]any
	if json.Unmarshal([]byte(metadataJSON), &metadata) == nil {
		for _, key := range optionalMetadataKeys {
			if value, ok := metadata[key]; ok {
				if key == "tags" {
					upstreamPayload[key] = tagsForUpstream(value)
				} else {
					upstreamPayload[key] = value
				}
			}
		}
	}
	result, err := client.SubmitClaudeAccount(ctx, upstreamPayload)
	if err != nil {
		outcome := outcomeOf(err)
		if errors.Is(err, supplier.ErrSubmitUnknown) {
			a.finishJobErr(id, "unknown", outcome)
			a.setDelivery(accountID, spec.ConnectionID, "unknown", "")
		} else {
			a.finishJobErr(id, "failed", outcome)
			a.setDelivery(accountID, spec.ConnectionID, "rejected", "")
		}
		a.audit("account.submit.failed", "account", accountID, errs.Sanitize(err.Error()))
		return
	}
	stamp := now()
	tx, e := a.DB.BeginTx(ctx, nil)
	if e != nil {
		a.finishJob(id, "unknown", "LOCAL_LINK_SAVE_FAILED")
		return
	}
	defer tx.Rollback()
	_, e = tx.Exec(`UPDATE deliveries SET upstream_id=?,ingestion_state='accepted',updated_at=? WHERE account_id=? AND connection_id=?`, fmt.Sprint(result.ID), stamp, accountID, spec.ConnectionID)
	if e == nil {
		_, e = tx.Exec(`INSERT INTO platform_links(id,account_id,connection_id,upstream_id,alias,ingestion_state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(account_id,connection_id) DO UPDATE SET upstream_id=excluded.upstream_id,alias=excluded.alias,ingestion_state='accepted',updated_at=excluded.updated_at`, newID(), accountID, spec.ConnectionID, fmt.Sprint(result.ID), result.Alias, "accepted", stamp, stamp)
	}
	jobResult, _ := json.Marshal(map[string]any{"code": "ACCEPTED_PENDING_VERIFICATION"})
	if e == nil {
		_, e = tx.Exec(`UPDATE jobs SET state='succeeded',result_json=?,updated_at=? WHERE id=?`, string(jobResult), stamp, id)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		tx.Rollback()
		slog.Error("platform link save failed", "job", id, "account", accountID, "error", e)
		a.finishJob(id, "unknown", "LOCAL_LINK_SAVE_FAILED")
		a.setDelivery(accountID, spec.ConnectionID, "unknown", fmt.Sprint(result.ID))
		return
	}
	a.audit("account.submit.accepted", "account", accountID, "upstream="+fmt.Sprint(result.ID))
}

// setDelivery updates the per-connection delivery row; upstreamID is only
// written when known so an earlier id is never erased by a later failure.
func (a *App) setDelivery(accountID, connectionID, state, upstreamID string) {
	var err error
	if upstreamID != "" {
		_, err = a.DB.Exec(`UPDATE deliveries SET upstream_id=?,ingestion_state=?,updated_at=? WHERE account_id=? AND connection_id=?`, upstreamID, state, now(), accountID, connectionID)
	} else {
		_, err = a.DB.Exec(`UPDATE deliveries SET ingestion_state=?,updated_at=? WHERE account_id=? AND connection_id=?`, state, now(), accountID, connectionID)
	}
	if err != nil {
		slog.Error("delivery state update failed", "account", accountID, "state", state, "error", err)
	}
}

// sanitizeUpstream is errs.Sanitize under the historical local name.
func sanitizeUpstream(message string) string { return errs.Sanitize(message) }

// syncAllPlatformUsage is deliberately best-effort: one connection or account
// failure must not erase the last known observation for other accounts.
func (a *App) syncAllPlatformUsage(ctx context.Context) error {
	rows, err := a.DB.Query(`SELECT id FROM platform_connections WHERE enabled=1`)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		if _, _, skip, e := a.metricsBackoff(ctx, id, time.Now()); e != nil {
			failures = append(failures, e)
			continue
		} else if skip {
			// Still inside the backoff window: no request, no run row, no log.
			continue
		}
		if e := a.syncPlatformConnection(ctx, id); e != nil {
			failures = append(failures, e)
		}
	}
	return errors.Join(failures...)

}

func (a *App) syncPlatformConnection(ctx context.Context, connectionID string) error {
	started := now()
	runID := newID()
	if _, err := a.DB.ExecContext(ctx, `INSERT INTO sync_runs(id,connection_id,kind,state,started_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, runID, connectionID, "platform-usage", "running", started, started, started); err != nil {
		return err
	}
	finish := func(state, code, message string, processed, failed int) {
		if _, err := a.DB.ExecContext(ctx, `UPDATE sync_runs SET state=?,finished_at=?,last_success_at=?,error_code=?,error_message=?,processed=?,failed=?,updated_at=? WHERE id=?`, state, now(), map[bool]string{true: now(), false: ""}[state == "succeeded"], code, sanitizeUpstream(message), processed, failed, now(), runID); err != nil {
			slog.Error("sync run finish failed", "run", runID, "error", err)
		}
	}
	var base, tokenSecret, timezone string
	if err := a.DB.QueryRowContext(ctx, `SELECT base_url,token_secret,timezone FROM platform_connections WHERE id=? AND enabled=1`, connectionID).Scan(&base, &tokenSecret, &timezone); err != nil {
		finish("failed", "CONNECTION_DISABLED", "connection unavailable", 0, 0)
		return err
	}
	if a.keys == nil {
		finish("failed", "ENCRYPTION_KEY_REQUIRED", "encryption key unavailable", 0, 0)
		return errors.New("encryption key unavailable")
	}
	token, err := a.openSecret(tokenSecret, connectionID)
	if err != nil {
		finish("failed", "CONNECTION_UNREADABLE", "connection token unreadable", 0, 0)
		return err
	}
	client, err := supplier.New(base, string(token))
	if err != nil {
		finish("failed", "CONNECTION_INVALID", "connection invalid", 0, 0)
		return err
	}
	loc := time.Local
	if timezone != "" {
		if l, e := time.LoadLocation(timezone); e == nil {
			loc = l
		}
	}
	nowLocal := time.Now().In(loc)
	start := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, loc)
	end := nowLocal
	linkRows, err := a.DB.QueryContext(ctx, `SELECT account_id,upstream_id,COALESCE(alias,'') FROM platform_links WHERE connection_id=? AND ingestion_state='accepted' ORDER BY account_id`, connectionID)
	if err != nil {
		finish("failed", "INTERNAL_ERROR", "links unavailable", 0, 0)
		return err
	}
	type link struct{ account, upstream, alias string }
	links := []link{}
	for linkRows.Next() {
		var v link
		if err = linkRows.Scan(&v.account, &v.upstream, &v.alias); err != nil {
			break
		}
		links = append(links, v)
	}
	if err == nil {
		err = linkRows.Err()
	}
	linkRows.Close()
	if err != nil {
		finish("failed", "INTERNAL_ERROR", "links unavailable", 0, 0)
		return err
	}
	processed, failed := 0, 0
	var queryError error
	for i := 0; i < len(links); i += 100 {
		j := i + 100
		if j > len(links) {
			j = len(links)
		}
		ids := make([]string, j-i)
		for k := i; k < j; k++ {
			ids[k-i] = links[k].upstream
		}
		metrics, e := client.QueryMetrics(ctx, ids, start, end)
		if e != nil {
			failed += j - i
			if queryError == nil {
				queryError = e
			}
			continue
		}
		for _, m := range metrics {
			var accountID string
			if err := a.DB.QueryRowContext(ctx, `SELECT account_id FROM platform_links WHERE connection_id=? AND upstream_id=?`, connectionID, fmt.Sprint(m.ID)).Scan(&accountID); err != nil {
				continue
			}
			fetched := now()
			status := m.Status
			bucket := start.Format("2006-01-02")
			aid := ""
			if m.AID > 0 {
				aid = fmt.Sprint(m.AID)
			}
			if _, e := a.DB.ExecContext(ctx, `INSERT INTO platform_metric_snapshots(id,account_id,connection_id,upstream_id,upstream_aid,alias,runtime_status,runtime_status_raw,bucket_key,window_start,window_end,observed_window_end,requests,prompt_tokens,completion_tokens,success_rate,fetched_at,freshness_state,sync_generation) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(account_id,connection_id,bucket_key) DO UPDATE SET upstream_id=excluded.upstream_id,upstream_aid=excluded.upstream_aid,alias=excluded.alias,runtime_status=excluded.runtime_status,runtime_status_raw=excluded.runtime_status_raw,window_start=excluded.window_start,window_end=excluded.window_end,observed_window_end=excluded.observed_window_end,requests=excluded.requests,prompt_tokens=excluded.prompt_tokens,completion_tokens=excluded.completion_tokens,success_rate=excluded.success_rate,fetched_at=excluded.fetched_at,freshness_state='fresh',sync_generation=excluded.sync_generation`, newID(), accountID, connectionID, fmt.Sprint(m.ID), aid, m.Alias, status, status, bucket, start.Format(time.RFC3339), end.Format(time.RFC3339), end.Format(time.RFC3339), m.Requests, m.PromptTokens, m.CompletionTokens, nullableRate(m), fetched, "fresh", time.Now().UnixNano()); e != nil {
				slog.Error("metric snapshot write failed", "account", accountID, "error", e)
				failed++
				continue
			}
			if _, e := a.DB.ExecContext(ctx, `UPDATE platform_links SET alias=?,runtime_status=?,runtime_status_raw=?,last_observed_at=?,updated_at=? WHERE account_id=? AND connection_id=?`, m.Alias, status, status, fetched, fetched, accountID, connectionID); e != nil {
				slog.Error("platform link update failed", "account", accountID, "error", e)
			}
			processed++
		}
	}

	message := ""
	if queryError != nil {
		message = queryError.Error()
	}
	finish(func() string {
		if failed > 0 {
			return "partial"
		}
		return "succeeded"
	}(), metricsErrorCode(queryError), message, processed, failed)
	return queryError
}

// Metrics sync backs off per connection while it keeps failing, so a
// platform-side problem costs one request an hour instead of one every two
// minutes. Editing the connection restarts the count.
const metricsBackoffCap = 60 * time.Minute

func metricsBackoffDelay(consecutive int) time.Duration {
	if consecutive <= 0 {
		return 0
	}
	d := 2 * time.Minute
	for i := 1; i < consecutive && d < metricsBackoffCap; i++ {
		d *= 2
	}
	if d > metricsBackoffCap {
		d = metricsBackoffCap
	}
	return d
}

// metricsBackoff is syncBackoff for the OpenAPI metrics sync.
func (a *App) metricsBackoff(ctx context.Context, connectionID string, at time.Time) (consecutive int, next time.Time, skip bool, err error) {
	return a.syncBackoff(ctx, connectionID, "platform-usage", at)
}

// syncBackoff counts consecutive non-successful runs of one sync kind for a
// connection since its last success (and since its last edit) and reports
// whether the next run should wait. Kinds back off independently: a failing
// web login never delays the metrics sync and vice versa.
func (a *App) syncBackoff(ctx context.Context, connectionID, kind string, at time.Time) (consecutive int, next time.Time, skip bool, err error) {
	var edited string
	err = a.DB.QueryRowContext(ctx, `SELECT COALESCE(updated_at,'') FROM platform_connections WHERE id=?`, connectionID).Scan(&edited)
	if errors.Is(err, sql.ErrNoRows) {
		// A deleted connection has nothing to back off from.
		return 0, time.Time{}, false, nil
	}
	if err != nil {
		return 0, time.Time{}, false, err
	}
	// The streak starts after the later of the last success and the last edit.
	var since string
	if err = a.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(started_at),'') FROM sync_runs WHERE connection_id=? AND kind=? AND state='succeeded' AND started_at>?`, connectionID, kind, edited).Scan(&since); err != nil {
		return 0, time.Time{}, false, err
	}
	if since < edited {
		since = edited
	}
	// The metrics sync treats a partial run as a failure (one query, one
	// outcome). The web syncs do many requests per run: a partial run means
	// the list was read and only some accounts had problems, so only runs
	// that changed nothing count toward the backoff.
	failing := `state NOT IN ('succeeded','running')`
	if strings.HasPrefix(kind, "web-") {
		failing = `state='failed'`
	}
	var latest string
	if err = a.DB.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MAX(started_at),'') FROM sync_runs WHERE connection_id=? AND kind=? AND `+failing+` AND started_at>?`, connectionID, kind, since).Scan(&consecutive, &latest); err != nil || consecutive == 0 {
		return consecutive, time.Time{}, false, err
	}
	last, perr := time.Parse(time.RFC3339, latest)
	if perr != nil {
		return consecutive, time.Time{}, false, nil
	}
	next = last.Add(metricsBackoffDelay(consecutive)).UTC()
	return consecutive, next, at.Before(next), nil
}

// metricsErrorCode classifies a metrics query failure for storage.
func metricsErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var up errs.Upstream
	if errors.As(err, &up) {
		return up.UpstreamCode()
	}
	return errs.CodePlatformUnavailable
}

func nullableRate(m supplier.Metric) any {
	if !m.SuccessRatePresent {
		return nil
	}
	return m.SuccessRate
}

func (a *App) finishJob(id, state, code string) { a.finishJobErr(id, state, errs.Adhoc(code, 0, "")) }

// finishJobErr persists the whole classified outcome so readers need no
// client-side mapping table.
func (a *App) finishJobErr(id, state string, e *errs.Error) {
	payload := map[string]any{"code": e.Code(), "message": e.Message(), "retryable": e.Retryable(), "action": e.Action()}
	if e.Hint() != "" {
		payload["hint"] = e.Hint()
	}
	if len(e.Details()) > 0 {
		payload["details"] = e.Details()
	}
	result, _ := json.Marshal(payload)
	if _, err := a.DB.Exec(`UPDATE jobs SET state=?,result_json=?,updated_at=? WHERE id=?`, state, string(result), now(), id); err != nil {
		slog.Error("job finish failed", "job", id, "state", state, "error", err)
	}
}

// outcomeOf maps an upstream submission failure onto the registry: the code
// comes from the client's classification, the upstream status goes to
// details, and any upstream text is sanitized into the message.
func outcomeOf(err error) *errs.Error {
	var up errs.Upstream
	if !errors.As(err, &up) {
		return errs.New(errs.CodeSubmitOutcomeUnknown).WithCause(err)
	}
	e := errs.New(up.UpstreamCode())
	if st := up.UpstreamStatus(); st != 0 {
		e = e.WithDetail("upstream_status", st)
	}
	if s := errs.Sanitize(up.UpstreamSummary()); s != "" {
		e = e.WithMessage(e.Message() + "，平台返回：" + s)
	}
	return e.WithCause(err)
}

var requiredAuth = []string{"access_token", "refresh_token", "last_refresh", "email", "account_uuid", "organization_uuid", "organization_name", "type", "expired", "proxy_url", "verification_status"}

func (a *App) preflight(accountID, connectionID string) map[string]any {
	checks := []map[string]any{}
	add := func(code, status string, field ...string) {
		item := map[string]any{"code": code, "status": status}
		if def, ok := errs.Lookup(code); ok {
			item["message"] = def.Message
			if def.Hint != "" {
				item["hint"] = def.Hint
			}
		}
		if len(field) > 0 && field[0] != "" {
			item["field"] = field[0]
		}
		checks = append(checks, item)
	}
	addField := func(p errs.FieldError) {
		checks = append(checks, map[string]any{"code": errs.CodeCredentialsIncomplete, "status": "error", "field": p.Field, "message": p.Message})
	}
	var account, plan, location, registered, start, end, proxy, credential, metadataJSON string
	var archived, version int
	if a.DB.QueryRow(`SELECT account,plan,register_location,register_time,plan_start_time,plan_end_time,proxy_id,credentials_json,metadata_json,archived,version FROM accounts WHERE id=?`, accountID).Scan(&account, &plan, &location, &registered, &start, &end, &proxy, &credential, &metadataJSON, &archived, &version) != nil {
		add("NOT_FOUND", "error")
		return map[string]any{"can_submit": false, "checks": checks}
	}
	if archived == 1 {
		add("ARCHIVED", "error")
	}
	if account == "" {
		add("ACCOUNT_REQUIRED", "error", "account")
	} else if validAccountIdentity(account) != nil {
		add("ACCOUNT_EMAIL_INVALID", "error", "account")
	}
	if !validPlan(plan) {
		add("PLAN_REQUIRED", "error", "plan")
	}
	if location != supportedRegisterLocation {
		add("REGISTER_LOCATION_INVALID", "error", "register_location")
	}
	if registered == "" || start == "" || end == "" {
		add("ACCOUNT_DATES_REQUIRED", "error", "register_time")
	} else {
		for _, item := range []struct{ name, field, value string }{{"REGISTER_TIME_INVALID", "register_time", registered}, {"PLAN_START_TIME_INVALID", "plan_start_time", start}, {"PLAN_END_TIME_INVALID", "plan_end_time", end}} {
			if !validDate(item.value) {
				add(item.name, "error", item.field)
			}
		}
		if validDate(start) && validDate(end) && start >= end {
			add("PLAN_DATE_RANGE_INVALID", "error", "plan_end_time")
		}
		if validDate(registered) && validDate(start) && registered > start {
			add("REGISTER_DATE_RANGE_INVALID", "error", "register_time")
		}
	}
	if metadataJSON != "" {
		var metadata map[string]any
		if json.Unmarshal([]byte(metadataJSON), &metadata) != nil {
			add("METADATA_UNREADABLE", "error")
		} else if problems := validOptionalMetadataFields(metadata); len(problems) > 0 {
			add("OPTIONAL_METADATA_INVALID", "error", problems[0].Field)
		}
	}
	var password string
	_ = a.DB.QueryRow(`SELECT password_secret FROM account_secrets WHERE account_id=?`, accountID).Scan(&password)
	if password == "" {
		add("PASSWORD_REQUIRED", "error", "password")
	}
	if credential == "{}" || credential == "" {
		add("CREDENTIALS_REQUIRED", "error", "auth_json")
	} else if a.keys != nil {
		auth, e := a.loadCredential(accountID)
		if e != nil {
			add("CREDENTIAL_UNREADABLE", "error", "auth_json")
		} else {
			for _, p := range validateAuthFields(auth) {
				addField(p)
			}
		}
	}
	if proxy != "" {
		if _, err := a.resolveProxyURL(proxy, ""); err != nil {
			add("PROXY_REQUIRED", "error", "proxy_id")
		}
	} else if credential == "{}" || credential == "" {
		add("PROXY_REQUIRED", "error", "proxy_id")
	}
	var enabled int
	var token string
	if connectionID == "" || a.DB.QueryRow(`SELECT enabled,token_secret FROM platform_connections WHERE id=?`, connectionID).Scan(&enabled, &token) != nil {
		add("PLATFORM_CONNECTION_REQUIRED", "error", "platform_connection_id")
	} else if enabled == 0 || token == "" {
		add("CONNECTION_DISABLED", "error", "platform_connection_id")
	}
	if code, _, err := deliveryBlocked(a.DB, accountID, connectionID); err != nil {
		add("DELIVERY_STATE_UNAVAILABLE", "error")
	} else if code != "" {
		add(code, "error")
	}
	return map[string]any{"account_id": accountID, "can_submit": len(checks) == 0, "checks": checks, "versions": map[string]any{"account": version}, "checked_at": now()}
}
func (a *App) loadCredential(accountID string) (map[string]any, error) {
	var enc string
	if e := a.DB.QueryRow(`SELECT credentials_json FROM accounts WHERE id=?`, accountID).Scan(&enc); e != nil {
		return nil, e
	}
	plain, e := a.openSecret(enc, accountID)
	if e != nil {
		return nil, e
	}
	var m map[string]any
	e = json.Unmarshal(plain, &m)
	return m, e
}
