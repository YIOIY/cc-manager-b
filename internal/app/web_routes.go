package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"cc-manager/internal/errs"
	"cc-manager/internal/supplier"
)

// API surface of the supplier web channel: the per-account "platform" block,
// the reconciliation list of remote accounts, per-model bills and the usage
// history. Everything here reads local snapshots; nothing calls the console
// except the explicit bill sync trigger.

// remoteRow is one platform_remote_accounts row as the API needs it.
type remoteRow struct {
	ConnectionID, UpstreamID, AID, Email, Alias, Provider, Model, ModelsJSON string
	ExtStatus, A1Status, A2Status, ProxyType, Remark                         string
	Locked, AutoCreated                                                      int
	RemoteCreatedAt, RemoteUpdatedAt, FirstSeenAt, LastSeenAt, MissingSince  string
	SupersededBy, AccountID                                                  string
	ReasonKind, ReasonText, ReasonFetchedAt, StatusChangedAt                 string
	Plan, PlanStart, PlanEnd, RegisterTime, RegisterLocation, Balance        string
	PaymentMethod, RegisterDevice, RechargeTime, WarrantyPeriod, TagsJSON    string
	CostMicro, InputTokens, OutputTokens                                     sql.NullInt64
	FiveHour, Week                                                           sql.NullFloat64
	FiveHourResetAt, WeekResetAt, UsageObservedAt                            string
}

const remoteColumns = `r.connection_id,r.upstream_id,r.upstream_aid,r.account_email,r.alias,r.provider,r.model,r.models_json,r.ext_status,r.a1_status,r.a2_status,r.proxy_type,r.remark,r.is_locked,r.auto_created,r.remote_created_at,r.remote_updated_at,r.first_seen_at,r.last_seen_at,r.missing_since,r.superseded_by,r.account_id,r.reason_kind,r.reason_text,r.reason_fetched_at,r.status_changed_at,r.plan,r.plan_start_time,r.plan_end_time,r.register_time,r.register_location,r.balance,r.payment_method,r.register_device,r.recharge_time,r.warranty_period,r.tags_json,r.usage_cost_micro,r.usage_input_tokens,r.usage_output_tokens,r.five_hour_used,r.week_used,r.five_hour_reset_at,r.week_reset_at,r.usage_observed_at`

func scanRemoteRow(rows interface{ Scan(...any) error }) (remoteRow, error) {
	var x remoteRow
	err := rows.Scan(&x.ConnectionID, &x.UpstreamID, &x.AID, &x.Email, &x.Alias, &x.Provider, &x.Model, &x.ModelsJSON, &x.ExtStatus, &x.A1Status, &x.A2Status, &x.ProxyType, &x.Remark, &x.Locked, &x.AutoCreated, &x.RemoteCreatedAt, &x.RemoteUpdatedAt, &x.FirstSeenAt, &x.LastSeenAt, &x.MissingSince, &x.SupersededBy, &x.AccountID, &x.ReasonKind, &x.ReasonText, &x.ReasonFetchedAt, &x.StatusChangedAt, &x.Plan, &x.PlanStart, &x.PlanEnd, &x.RegisterTime, &x.RegisterLocation, &x.Balance, &x.PaymentMethod, &x.RegisterDevice, &x.RechargeTime, &x.WarrantyPeriod, &x.TagsJSON, &x.CostMicro, &x.InputTokens, &x.OutputTokens, &x.FiveHour, &x.Week, &x.FiveHourResetAt, &x.WeekResetAt, &x.UsageObservedAt)
	return x, err
}

func percent(v float64) float64 { return math.Round(v*10000) / 100 }

func microToFen(micro int64) int64 { return int64(math.Round(float64(micro) / 10000)) }

func yuanText(fen int64) string { return fmt.Sprintf("%d.%02d", fen/100, fen%100) }

// webChannelState is what an account response says about its connection:
// whether profile edits can reach the platform at all, and the platform's
// fixed USD→RMB rate for showing the RMB cost in dollars.
type webChannelState struct {
	Configured, Suspended bool
	USDRate               float64
}

func (a *App) webChannelState(ctx context.Context, connectionID string) (webChannelState, error) {
	var configured, suspended int
	rate := defaultUSDCNYRate
	err := a.DB.QueryRowContext(ctx, `SELECT web_username<>'' AND web_password_secret<>'',web_suspended_at<>'',usd_cny_rate FROM platform_connections WHERE id=?`, connectionID).Scan(&configured, &suspended, &rate)
	return webChannelState{Configured: configured == 1, Suspended: suspended == 1, USDRate: rate}, err
}

// platformBlock renders the "platform" object of an account response.
func platformBlock(x remoteRow, channel string, web webChannelState) map[string]any {
	status := x.ExtStatus
	if status == "" {
		status = x.A1Status
	}
	var tags []string
	_ = json.Unmarshal([]byte(x.TagsJSON), &tags)
	tags = normalizeAccountTags(tags)
	if tags == nil {
		tags = []string{}
	}
	modelsUsable := strings.Contains(x.ModelsJSON, `"usable":true`)
	block := map[string]any{
		"status": status, "status_raw": x.ExtStatus, "locked": x.Locked == 1, "reason_kind": x.ReasonKind, "reason_text": x.ReasonText, "reason_fetched_at": x.ReasonFetchedAt,
		"status_changed_at": x.StatusChangedAt,
		"observed_at":       x.LastSeenAt, "aid": x.AID, "upstream_id": x.UpstreamID, "alias": x.Alias, "provider": x.Provider, "model": x.Model, "models_usable": modelsUsable,
		"remote_created_at": x.RemoteCreatedAt, "missing_since": x.MissingSince, "auto_created": x.AutoCreated == 1, "source_channel": channel, "remark": x.Remark,
		"web_channel": map[string]any{"configured": web.Configured, "suspended": web.Suspended}, "usd_cny_rate": web.USDRate,
		"profile": map[string]any{"plan": x.Plan, "plan_start_time": x.PlanStart, "plan_end_time": x.PlanEnd, "register_time": x.RegisterTime, "register_location": x.RegisterLocation, "balance": x.Balance, "payment_method": x.PaymentMethod, "register_device": x.RegisterDevice, "recharge_time": x.RechargeTime, "warranty_period": x.WarrantyPeriod, "tags": tags},
		"usage":   nil,
	}
	if x.CostMicro.Valid || x.InputTokens.Valid || x.FiveHour.Valid {
		fen := microToFen(x.CostMicro.Int64)
		block["usage"] = map[string]any{
			"cost_fen": fen, "cost_yuan": yuanText(fen), "input_tokens": x.InputTokens.Int64, "output_tokens": x.OutputTokens.Int64,
			"five_hour_used_percent": percent(x.FiveHour.Float64), "five_hour_reset_at": x.FiveHourResetAt,
			"week_used_percent": percent(x.Week.Float64), "week_reset_at": x.WeekResetAt, "observed_at": x.UsageObservedAt,
		}
	}
	return block
}

// attachPlatformBlocks adds "platform" to every account map in out (nil when
// the platform never listed the account's linked delivery). One query for
// the whole page.
func (a *App) attachPlatformBlocks(ctx context.Context, out []any) error {
	ids := make([]any, 0, len(out))
	byID := map[string]map[string]any{}
	for _, item := range out {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		m["platform"] = nil
		if id != "" {
			ids = append(ids, id)
			byID[id] = m
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := a.DB.QueryContext(ctx, `SELECT `+remoteColumns+`,l.account_id,COALESCE((SELECT MAX(fetched_at) FROM platform_metric_snapshots s WHERE s.account_id=l.account_id),''),COALESCE(c.web_username<>'' AND c.web_password_secret<>'',0),COALESCE(c.web_suspended_at<>'',0),COALESCE(c.usd_cny_rate,7.2) FROM platform_links l JOIN platform_remote_accounts r ON r.connection_id=l.connection_id AND r.upstream_id=l.upstream_id LEFT JOIN platform_connections c ON c.id=r.connection_id WHERE l.account_id IN (`+marks(len(ids))+`) ORDER BY l.updated_at DESC`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var x remoteRow
		var accountID, metricsAt string
		var webConfigured, webSuspended int
		var usdRate float64
		if err = rows.Scan(&x.ConnectionID, &x.UpstreamID, &x.AID, &x.Email, &x.Alias, &x.Provider, &x.Model, &x.ModelsJSON, &x.ExtStatus, &x.A1Status, &x.A2Status, &x.ProxyType, &x.Remark, &x.Locked, &x.AutoCreated, &x.RemoteCreatedAt, &x.RemoteUpdatedAt, &x.FirstSeenAt, &x.LastSeenAt, &x.MissingSince, &x.SupersededBy, &x.AccountID, &x.ReasonKind, &x.ReasonText, &x.ReasonFetchedAt, &x.StatusChangedAt, &x.Plan, &x.PlanStart, &x.PlanEnd, &x.RegisterTime, &x.RegisterLocation, &x.Balance, &x.PaymentMethod, &x.RegisterDevice, &x.RechargeTime, &x.WarrantyPeriod, &x.TagsJSON, &x.CostMicro, &x.InputTokens, &x.OutputTokens, &x.FiveHour, &x.Week, &x.FiveHourResetAt, &x.WeekResetAt, &x.UsageObservedAt, &accountID, &metricsAt, &webConfigured, &webSuspended, &usdRate); err != nil {
			return err
		}
		m := byID[accountID]
		if m == nil || m["platform"] != nil {
			continue
		}
		channel := "web"
		if metricsAt > x.LastSeenAt {
			channel = "openapi"
		}
		m["platform"] = platformBlock(x, channel, webChannelState{Configured: webConfigured == 1, Suspended: webSuspended == 1, USDRate: usdRate})
	}
	return rows.Err()
}

var remoteProfileFields = []struct{ key, local string }{{"plan", "plan"}, {"plan_start_time", "plan_start_time"}, {"plan_end_time", "plan_end_time"}, {"register_time", "register_time"}, {"register_location", "register_location"}}

// remoteAccounts answers GET /api/v1/platform-connections/{id}/remote-accounts.
// Admins see the full remote inventory for the connection; everyone else only
// sees remotes linked to accounts they own (unmatched / unowned auto-imports
// stay admin-only, matching account list ownership).
func (a *App) remoteAccounts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	connectionID := r.PathValue("id")
	var exists int
	if err := a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM platform_connections WHERE id=?`, connectionID).Scan(&exists); err != nil {
		writeError(w, r, storageError(err, "平台连接读取失败"))
		return
	}
	if exists == 0 {
		fail(w, http.StatusNotFound, "NOT_FOUND", "平台连接不存在")
		return
	}
	page, size, paged, ok := pageParams(w, r)
	if !ok {
		return
	}
	if !paged {
		page, size = 1, 100
	}
	scope := ownershipFrom(r)
	q := r.URL.Query()
	from := ` FROM platform_remote_accounts r LEFT JOIN accounts a ON a.id=r.account_id`
	where := ` WHERE r.connection_id=?`
	web, err := a.webChannelState(ctx, connectionID)
	if err != nil {
		writeError(w, r, storageError(err, "平台连接读取失败"))
		return
	}
	args := []any{connectionID}
	if frag, ownerArgs := scope.sqlAnd("a.created_by_user_id"); frag != "" {
		where += frag
		args = append(args, ownerArgs...)
	}
	if status := strings.TrimSpace(q.Get("status")); status != "" {
		where += ` AND r.ext_status=?`
		args = append(args, status)
	}
	if kw := strings.TrimSpace(q.Get("q")); kw != "" {
		where += ` AND (instr(lower(r.account_email),lower(?))>0 OR instr(lower(r.alias),lower(?))>0 OR instr(r.upstream_id,?)>0 OR instr(r.upstream_aid,?)>0)`
		args = append(args, kw, kw, kw, kw)
	}
	var total int
	if err := a.DB.QueryRowContext(ctx, `SELECT COUNT(*)`+from+where, args...).Scan(&total); err != nil {
		writeError(w, r, storageError(err, "平台账号读取失败"))
		return
	}
	rows, err := a.DB.QueryContext(ctx, `SELECT `+remoteColumns+`,COALESCE(a.plan,''),COALESCE(a.plan_start_time,''),COALESCE(a.plan_end_time,''),COALESCE(a.register_time,''),COALESCE(a.register_location,''),COALESCE(a.account,'')`+from+where+` ORDER BY r.remote_created_at DESC,r.upstream_id DESC LIMIT ? OFFSET ?`, append(append([]any{}, args...), size, (page-1)*size)...)
	if err != nil {
		writeError(w, r, storageError(err, "平台账号读取失败"))
		return
	}
	defer rows.Close()
	list := []any{}
	for rows.Next() {
		var x remoteRow
		var local [6]string
		if err = rows.Scan(&x.ConnectionID, &x.UpstreamID, &x.AID, &x.Email, &x.Alias, &x.Provider, &x.Model, &x.ModelsJSON, &x.ExtStatus, &x.A1Status, &x.A2Status, &x.ProxyType, &x.Remark, &x.Locked, &x.AutoCreated, &x.RemoteCreatedAt, &x.RemoteUpdatedAt, &x.FirstSeenAt, &x.LastSeenAt, &x.MissingSince, &x.SupersededBy, &x.AccountID, &x.ReasonKind, &x.ReasonText, &x.ReasonFetchedAt, &x.StatusChangedAt, &x.Plan, &x.PlanStart, &x.PlanEnd, &x.RegisterTime, &x.RegisterLocation, &x.Balance, &x.PaymentMethod, &x.RegisterDevice, &x.RechargeTime, &x.WarrantyPeriod, &x.TagsJSON, &x.CostMicro, &x.InputTokens, &x.OutputTokens, &x.FiveHour, &x.Week, &x.FiveHourResetAt, &x.WeekResetAt, &x.UsageObservedAt, &local[0], &local[1], &local[2], &local[3], &local[4], &local[5]); err != nil {
			writeError(w, r, storageError(err, "平台账号读取失败"))
			return
		}
		remoteValues := []string{x.Plan, x.PlanStart, x.PlanEnd, x.RegisterTime, x.RegisterLocation}
		diff := []string{}
		incomplete := []string{}
		if x.AccountID != "" {
			for i, f := range remoteProfileFields {
				if remoteValues[i] != "" && local[i] != "" && remoteValues[i] != local[i] {
					diff = append(diff, f.key)
				}
				if remoteValues[i] != "" && local[i] == "" && x.AutoCreated == 1 {
					incomplete = append(incomplete, f.key)
				}
			}
		}
		item := platformBlock(x, "web", web)
		item["email"] = x.Email
		item["account_id"] = x.AccountID
		item["account"] = local[5]
		item["superseded_by"] = x.SupersededBy
		item["first_seen_at"] = x.FirstSeenAt
		item["profile_diff"] = diff
		item["profile_incomplete"] = len(incomplete) > 0
		item["profile_missing_fields"] = incomplete
		list = append(list, item)
	}
	if err = rows.Err(); err != nil {
		writeError(w, r, storageError(err, "平台账号读取失败"))
		return
	}
	summary := map[string]int{}
	summaryFrom := ` FROM platform_remote_accounts r`
	summaryWhere := ` WHERE r.connection_id=?`
	summaryArgs := []any{connectionID}
	if !scope.Admin {
		summaryFrom += ` JOIN accounts a ON a.id=r.account_id`
		summaryWhere += ` AND a.created_by_user_id=?`
		summaryArgs = append(summaryArgs, scope.UserID)
	}
	if err = a.DB.QueryRowContext(ctx, `SELECT COUNT(*),SUM(r.account_id<>''),SUM(r.auto_created),SUM(r.account_email=''),SUM(r.superseded_by<>''),SUM(r.missing_since<>'')`+summaryFrom+summaryWhere, summaryArgs...).Scan(sqlInt(summary, "total"), sqlInt(summary, "linked"), sqlInt(summary, "auto_created"), sqlInt(summary, "unmatched"), sqlInt(summary, "superseded"), sqlInt(summary, "missing")); err != nil {
		writeError(w, r, storageError(err, "平台账号读取失败"))
		return
	}
	counts, err := a.remoteStatusCounts(ctx, connectionID, scope)
	if err != nil {
		writeError(w, r, storageError(err, "平台账号读取失败"))
		return
	}
	write(w, http.StatusOK, map[string]any{"list": list, "summary": summary, "counts_by_status": counts, "page": page, "page_size": size, "total": total})
}

// sqlInt scans a nullable aggregate into a map entry.
func sqlInt(m map[string]int, key string) *nullInt {
	return &nullInt{m: m, key: key}
}

type nullInt struct {
	m   map[string]int
	key string
}

func (n *nullInt) Scan(v any) error {
	var x sql.NullInt64
	if err := x.Scan(v); err != nil {
		return err
	}
	n.m[n.key] = int(x.Int64)
	return nil
}

// remoteStatusCounts counts the currently listed (non-missing) remote
// accounts per console status. Non-admins only see remotes linked to
// accounts they created.
func (a *App) remoteStatusCounts(ctx context.Context, connectionID string, scope ownershipScope) (map[string]int, error) {
	query := `SELECT r.ext_status,COUNT(*) FROM platform_remote_accounts r`
	args := []any{}
	if !scope.Admin {
		query += ` JOIN accounts a ON a.id=r.account_id`
	}
	query += ` WHERE r.missing_since=''`
	if connectionID != "" {
		query += ` AND r.connection_id=?`
		args = append(args, connectionID)
	}
	if frag, ownerArgs := scope.sqlAnd("a.created_by_user_id"); frag != "" {
		query += frag
		args = append(args, ownerArgs...)
	}
	rows, err := a.DB.QueryContext(ctx, query+` GROUP BY r.ext_status`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err = rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		if status == "" {
			status = "unknown"
		}
		counts[status] = n
	}
	return counts, rows.Err()
}

// webDashboard adds the web channel's figures to the dashboard "platform"
// block: its sync health, status counts, near-limit accounts and today's
// per-model spend.
func (a *App) webDashboard(ctx context.Context, scope ownershipScope) (map[string]any, error) {
	accountsHealth, err := a.syncHealth(ctx, "web-accounts")
	if err != nil {
		return nil, err
	}
	billsHealth, err := a.syncHealth(ctx, "web-bills")
	if err != nil {
		return nil, err
	}
	worse := accountsHealth
	switch {
	case accountsHealth["sync_status"] == "failing" && billsHealth["sync_status"] == "failing":
		if intOf(billsHealth["consecutive_failures"]) > intOf(accountsHealth["consecutive_failures"]) {
			worse = billsHealth
		}
	case billsHealth["sync_status"] == "failing":
		worse = billsHealth
	case accountsHealth["sync_status"] == "never" && billsHealth["sync_status"] == "ok":
		worse = billsHealth
	}
	counts, err := a.remoteStatusCounts(ctx, "", scope)
	if err != nil {
		return nil, err
	}
	var nearLimit int
	nearQuery := `SELECT COUNT(*) FROM platform_remote_accounts WHERE missing_since='' AND five_hour_used>=0.8`
	nearArgs := []any{}
	if !scope.Admin {
		nearQuery = `SELECT COUNT(*) FROM platform_remote_accounts r JOIN accounts a ON a.id=r.account_id WHERE r.missing_since='' AND r.five_hour_used>=0.8 AND a.created_by_user_id=?`
		nearArgs = []any{scope.UserID}
	}
	if err = a.DB.QueryRowContext(ctx, nearQuery, nearArgs...).Scan(&nearLimit); err != nil {
		return nil, err
	}
	spend, err := a.spendSummary(ctx, time.Now(), scope)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"web_sync_health": worse, "web_accounts_health": accountsHealth, "web_bills_health": billsHealth, "remote_counts_by_status": counts, "near_limit_accounts": nearLimit}
	for k, v := range spend {
		out[k] = v
	}
	return out, nil
}

// connectionLocation resolves a connection's IANA zone; anything empty or
// unknown falls back to UTC so date buckets are always computable.
func connectionLocation(zone string) *time.Location {
	if zone == "" || zone == "Local" {
		return time.UTC
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.UTC
	}
	return loc
}

const spendWindowDays = 7

// spendSummary aggregates the per-model bills of every enabled connection
// over its own local "today" and the 6 days before, merging connections by
// local date string. total_model_cost_fen is the all-time scoped sum (admin =
// every row; others = accounts they created).
func (a *App) spendSummary(ctx context.Context, at time.Time, scope ownershipScope) (map[string]any, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT id,timezone FROM platform_connections WHERE enabled=1`)
	if err != nil {
		return nil, err
	}
	type conn struct{ id, today, start string }
	var conns []conn
	for rows.Next() {
		var id, zone string
		if err = rows.Scan(&id, &zone); err != nil {
			break
		}
		local := at.In(connectionLocation(zone))
		conns = append(conns, conn{id, local.Format("2006-01-02"), local.AddDate(0, 0, -(spendWindowDays - 1)).Format("2006-01-02")})
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	byDay := map[string]int64{}
	byModel := map[string]int64{}
	byAccount := map[string]*struct {
		email string
		fen   int64
	}{}
	var today, week int64
	latestToday, latestStart := "", ""
	ownerSQL, ownerArgs := scope.sqlAnd("acc.created_by_user_id")
	var total int64
	if err = a.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(b.cost_fen),0) FROM platform_bill_models b LEFT JOIN accounts acc ON acc.id=b.account_id WHERE 1=1`+ownerSQL, ownerArgs...).Scan(&total); err != nil {
		return nil, err
	}
	for _, c := range conns {
		if c.today > latestToday {
			latestToday, latestStart = c.today, c.start
		}
		args := append([]any{c.id, c.start, c.today}, ownerArgs...)
		billRows, err := a.DB.QueryContext(ctx, `SELECT b.bill_date,b.model_name,b.account_id,COALESCE(acc.account,''),b.cost_fen FROM platform_bill_models b LEFT JOIN accounts acc ON acc.id=b.account_id WHERE b.connection_id=? AND b.bill_date>=? AND b.bill_date<=?`+ownerSQL, args...)
		if err != nil {
			return nil, err
		}
		for billRows.Next() {
			var day, model, accountID, email string
			var fen int64
			if err = billRows.Scan(&day, &model, &accountID, &email, &fen); err != nil {
				break
			}
			byDay[day] += fen
			byModel[model] += fen
			week += fen
			if day == c.today {
				today += fen
			}
			if byAccount[accountID] == nil {
				byAccount[accountID] = &struct {
					email string
					fen   int64
				}{email, 0}
			}
			byAccount[accountID].fen += fen
		}
		if err == nil {
			err = billRows.Err()
		}
		billRows.Close()
		if err != nil {
			return nil, err
		}
	}
	days := make([]map[string]any, 0, spendWindowDays)
	if latestToday != "" {
		start, _ := time.Parse("2006-01-02", latestStart)
		for i := 0; i < spendWindowDays; i++ {
			day := start.AddDate(0, 0, i).Format("2006-01-02")
			days = append(days, map[string]any{"date": day, "cost_fen": byDay[day], "final": day < latestToday})
		}
	}
	topN := func(m map[string]int64, key string) []map[string]any {
		type kv struct {
			k string
			v int64
		}
		list := make([]kv, 0, len(m))
		for k, v := range m {
			list = append(list, kv{k, v})
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].v != list[j].v {
				return list[i].v > list[j].v
			}
			return list[i].k < list[j].k
		})
		out := make([]map[string]any, 0, 5)
		for _, e := range list {
			if len(out) == 5 {
				break
			}
			out = append(out, map[string]any{key: e.k, "cost_fen": e.v})
		}
		return out
	}
	accountTotals := map[string]int64{}
	emails := map[string]string{}
	for id, v := range byAccount {
		accountTotals[id] = v.fen
		emails[id] = v.email
	}
	accounts := topN(accountTotals, "account_id")
	for _, m := range accounts {
		m["account"] = emails[m["account_id"].(string)]
	}
	return map[string]any{"today_model_cost_fen": today, "week_model_cost_fen": week, "total_model_cost_fen": total, "cost_by_day": days, "cost_by_model": topN(byModel, "model_name"), "cost_by_account": accounts}, nil
}

func intOf(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	}
	return 0
}

// billModels answers GET /platform-usage/bills/models.
func (a *App) billModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	connectionID := strings.TrimSpace(q.Get("connection_id"))
	if connectionID == "" {
		writeError(w, r, billField("connection_id", errs.FieldRequired, "请选择平台连接"))
		return
	}
	var zone string
	if err := a.DB.QueryRowContext(ctx, `SELECT timezone FROM platform_connections WHERE id=?`, connectionID).Scan(&zone); err != nil {
		writeError(w, r, errs.New(errs.CodeNotFound).WithMessage("平台连接不存在").WithDetail("resource", "platform_connection"))
		return
	}
	dates, err := supplier.ValidateBillDates(q.Get("from"), q.Get("to"), nil, time.Time{}, false)
	if err != nil {
		writeError(w, r, billBad("BILL_RANGE_INVALID", "账单起止日期无效"))
		return
	}
	page, size, paged, ok := pageParams(w, r)
	if !ok {
		return
	}
	if !paged {
		page, size = 1, 100
	}
	where := ` WHERE b.connection_id=? AND b.bill_date>=? AND b.bill_date<=?`
	args := []any{connectionID, dates.StartDate, dates.EndDate}
	if frag, ownerArgs := ownershipFrom(r).sqlAnd("a.created_by_user_id"); frag != "" {
		where += frag
		args = append(args, ownerArgs...)
	}
	if raw := strings.TrimSpace(q.Get("account_ids")); raw != "" {
		ids := uniqueSorted(strings.Split(raw, ","))
		where += ` AND b.account_id IN (` + marks(len(ids)) + `)`
		for _, id := range ids {
			args = append(args, strings.TrimSpace(id))
		}
	}
	base := ` FROM platform_bill_models b LEFT JOIN accounts a ON a.id=b.account_id` + where
	var total int
	if err = a.DB.QueryRowContext(ctx, `SELECT COUNT(*)`+base, args...).Scan(&total); err != nil {
		writeError(w, r, storageError(err, "账单读取失败"))
		return
	}
	rows, err := a.DB.QueryContext(ctx, `SELECT b.bill_date,b.account_id,COALESCE(a.account,''),b.upstream_id,b.model_name,b.bill_model_name,b.cost_fen,b.final,b.fetched_at,b.generation`+base+` ORDER BY b.bill_date DESC,b.account_id,b.model_name LIMIT ? OFFSET ?`, append(append([]any{}, args...), size, (page-1)*size)...)
	if err != nil {
		writeError(w, r, storageError(err, "账单读取失败"))
		return
	}
	list := []any{}
	for rows.Next() {
		var day, account, email, upstream, model, billModel, fetched string
		var fen, generation int64
		var final int
		if err = rows.Scan(&day, &account, &email, &upstream, &model, &billModel, &fen, &final, &fetched, &generation); err != nil {
			rows.Close()
			writeError(w, r, storageError(err, "账单读取失败"))
			return
		}
		list = append(list, map[string]any{"bill_date": day, "account_id": account, "account": email, "upstream_id": upstream, "model_name": model, "bill_model_name": billModel, "cost_fen": fen, "cost_yuan": yuanText(fen), "final": final == 1, "fetched_at": fetched, "generation": generation})
	}
	rows.Close()
	type agg struct {
		key, label string
		fen        int64
	}
	byAccount, byModel := map[string]*agg{}, map[string]*agg{}
	var totalFen int64
	days := map[string]bool{}
	aggRows, err := a.DB.QueryContext(ctx, `SELECT b.bill_date,b.account_id,COALESCE(a.account,''),b.model_name,b.cost_fen`+base, args...)
	if err != nil {
		writeError(w, r, storageError(err, "账单读取失败"))
		return
	}
	for aggRows.Next() {
		var day, account, email, model string
		var fen int64
		if err = aggRows.Scan(&day, &account, &email, &model, &fen); err != nil {
			break
		}
		days[day] = true
		totalFen += fen
		if byAccount[account] == nil {
			byAccount[account] = &agg{key: account, label: email}
		}
		byAccount[account].fen += fen
		if byModel[model] == nil {
			byModel[model] = &agg{key: model, label: model}
		}
		byModel[model].fen += fen
	}
	if err == nil {
		err = aggRows.Err()
	}
	aggRows.Close()
	if err != nil {
		writeError(w, r, storageError(err, "账单读取失败"))
		return
	}
	sortedAgg := func(m map[string]*agg) []any {
		items := make([]*agg, 0, len(m))
		for _, v := range m {
			items = append(items, v)
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].fen != items[j].fen {
				return items[i].fen > items[j].fen
			}
			return items[i].key < items[j].key
		})
		out := make([]any, 0, len(items))
		for _, v := range items {
			out = append(out, map[string]any{"key": v.key, "label": v.label, "cost_fen": v.fen, "cost_yuan": yuanText(v.fen)})
		}
		return out
	}
	accounts := sortedAgg(byAccount)
	for _, item := range accounts {
		m := item.(map[string]any)
		m["account_id"], m["account"] = m["key"], m["label"]
	}
	models := sortedAgg(byModel)
	for _, item := range models {
		item.(map[string]any)["model_name"] = item.(map[string]any)["key"]
	}
	start, _ := time.Parse("2006-01-02", dates.StartDate)
	end, _ := time.Parse("2006-01-02", dates.EndDate)
	requested := int(end.Sub(start).Hours()/24) + 1
	today := ""
	if loc, e := time.LoadLocation(zone); e == nil {
		today = time.Now().In(loc).Format("2006-01-02")
	}
	write(w, http.StatusOK, map[string]any{
		"list": list, "by_account": accounts, "by_model": models, "total_cost_fen": totalFen, "total_cost_yuan": yuanText(totalFen),
		"coverage": map[string]any{"days_requested": requested, "days_present": len(days), "today": today, "today_final": false, "today_included": today != "" && today >= dates.StartDate && today <= dates.EndDate},
		"page":     page, "page_size": size, "total": total,
	})
}

// billModelsSync answers POST /platform-usage/bills/models/sync: one
// immediate web bill sync for a connection, unless it is backing off.
func (a *App) billModelsSync(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ConnectionID string `json:"connection_id"`
	}
	if !decodeJSON(w, r, &in, defaultBodyLimit) {
		return
	}
	ctx := r.Context()
	if strings.TrimSpace(in.ConnectionID) == "" {
		writeError(w, r, billField("connection_id", errs.FieldRequired, "请选择平台连接"))
		return
	}
	conn, _, err := a.loadWebConnection(ctx, in.ConnectionID)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "NOT_FOUND", "平台连接不存在")
		return
	}
	if err != nil {
		writeError(w, r, storageError(err, "平台连接读取失败"))
		return
	}
	if !conn.Configured {
		writeError(w, r, errWebNotConfigured)
		return
	}
	if conn.SuspendedAt != "" {
		writeError(w, r, errWebSuspended)
		return
	}
	_, next, skip, err := a.syncBackoff(ctx, in.ConnectionID, "web-bills", time.Now())
	if err != nil {
		writeError(w, r, storageError(err, "同步状态读取失败"))
		return
	}
	if skip {
		writeError(w, r, errs.New(errs.CodeSyncInBackoff).WithDetail("next_attempt_at", next.Format(time.RFC3339)))
		return
	}
	runID := newID()
	a.auditFor(r, "bills.web_sync", "platform_connection", in.ConnectionID, runID)
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		if e := a.syncWebBillsAs(context.Background(), in.ConnectionID, runID); e != nil {
			slog.Warn("manual web bill sync failed", "connection", in.ConnectionID, "error", e)
		}
	}()
	write(w, http.StatusAccepted, map[string]any{"run_id": runID, "state": "running"})
}

// accountUsageHistory answers GET /api/v1/accounts/{id}/platform-usage-history.
func (a *App) accountUsageHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.requireVisible(w, r, "accounts", id, "账号不存在"); !ok {
		return
	}
	days := 7
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 90 {
			writeError(w, r, queryInvalid("days", "天数必须是 1 到 90 的整数"))
			return
		}
		days = n
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour).UTC().Format(time.RFC3339)
	rows, err := a.DB.QueryContext(r.Context(), `SELECT observed_at,usage_cost_micro,usage_input_tokens,usage_output_tokens,five_hour_used,five_hour_reset_at,week_used,week_reset_at FROM platform_usage_snapshots WHERE account_id=? AND observed_at>=? ORDER BY observed_at,rowid`, id, since)
	if err != nil {
		writeError(w, r, storageError(err, "用量历史读取失败"))
		return
	}
	defer rows.Close()
	list := []any{}
	for rows.Next() {
		var observed, fiveReset, weekReset string
		var cost, in, out sql.NullInt64
		var five, week sql.NullFloat64
		if err = rows.Scan(&observed, &cost, &in, &out, &five, &fiveReset, &week, &weekReset); err != nil {
			writeError(w, r, storageError(err, "用量历史读取失败"))
			return
		}
		fen := microToFen(cost.Int64)
		list = append(list, map[string]any{"observed_at": observed, "cost_fen": fen, "cost_yuan": yuanText(fen), "input_tokens": in.Int64, "output_tokens": out.Int64, "five_hour_used_percent": percent(five.Float64), "five_hour_reset_at": fiveReset, "week_used_percent": percent(week.Float64), "week_reset_at": weekReset})
	}
	if err = rows.Err(); err != nil {
		writeError(w, r, storageError(err, "用量历史读取失败"))
		return
	}
	write(w, http.StatusOK, map[string]any{"account_id": id, "days": days, "list": list})
}
