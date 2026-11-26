package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"time"
)

// The dashboard's second request: everything the fleet panels show beyond
// the headline numbers. All of it is read from what the sync jobs already
// stored; nothing here talks to the platform.

const (
	insightsQuotaLimit = 50
	insightsIdleLimit  = 20
	// An in-use account below this fraction of both its 5-hour and 7-day
	// windows counts as idle; a busy 5-hour window means it is in use now.
	idleWeekFraction = 0.10
	insightsJobLimit = 6
	insightsHours    = 24
)

type quotaRow struct {
	AccountID         string   `json:"account_id"`
	Account           string   `json:"account"`
	Alias             string   `json:"alias"`
	PlatformAccountID string   `json:"platform_account_id"`
	CreatedByUserID   string   `json:"created_by_user_id"`
	CreatedByNickname string   `json:"created_by_nickname"`
	CreatedByUsername string   `json:"created_by_username"`
	Status            string   `json:"status"`
	FiveHourPercent   *float64 `json:"five_hour_percent"`
	FiveHourResetAt   string   `json:"five_hour_reset_at"`
	WeekPercent       *float64 `json:"week_percent"`
	WeekResetAt       string   `json:"week_reset_at"`
	CostUsdCents      *int64   `json:"cost_usd_cents"`
	CostUsd           string   `json:"cost_usd,omitempty"`
}

// idleRow is an in-use account that has barely used its 7-day window.
type idleRow struct {
	AccountID         string   `json:"account_id"`
	Account           string   `json:"account"`
	Alias             string   `json:"alias"`
	PlatformAccountID string   `json:"platform_account_id"`
	CreatedByUserID   string   `json:"created_by_user_id"`
	CreatedByNickname string   `json:"created_by_nickname"`
	CreatedByUsername string   `json:"created_by_username"`
	WeekPercent       *float64 `json:"week_percent"`
	CostUsdCents      *int64   `json:"cost_usd_cents"`
	CostUsd           string   `json:"cost_usd,omitempty"`
	PlanEnd           string   `json:"plan_end"`
	// DaysLeft is nil when the plan end date is unknown.
	DaysLeft *int `json:"days_left"`
}

// dashboardInsights answers GET /api/v1/dashboard/insights.
func (a *App) dashboardInsights(w http.ResponseWriter, r *http.Request) {
	data, err := a.dashboardInsightsData(r.Context(), time.Now(), sessionUser(r).Access.allows(PermJobsView), ownershipFrom(r))
	if err != nil {
		writeError(w, r, storageError(err, "读取概览失败"))
		return
	}
	write(w, http.StatusOK, data)
}

// dashboardInsightsData builds the response. recent_jobs is nil unless the
// caller may see the task centre.
func (a *App) dashboardInsightsData(ctx context.Context, at time.Time, includeJobs bool, scope ownershipScope) (map[string]any, error) {
	fleet, quota, expiry, idle, nextExpiry, err := a.fleetQuota(ctx, at, scope)
	if err != nil {
		return nil, err
	}
	hourly, err := a.hourlyBurn(ctx, at, scope)
	if err != nil {
		return nil, err
	}
	byDelivery, err := a.deliveryCounts(ctx, scope)
	if err != nil {
		return nil, err
	}
	rate, err := a.sharedUSDRate(ctx)
	if err != nil {
		return nil, err
	}
	var jobs any
	if includeJobs {
		if jobs, err = a.recentSubmitJobs(ctx, scope); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"usd_cny_rate": rate,
		"fleet":        fleet,
		"quota":        quota,
		"idle":         idle,
		"hourly":       hourly,
		"composition":  map[string]any{"by_delivery": byDelivery, "expiry": expiry, "next_expiry": nextExpiry},
		"recent_jobs":  jobs,
	}, nil
}

// fleetQuota walks every onboarded account once: totals and the quota board
// come from accounts the platform still lists; the expiry buckets cover all
// onboarded accounts (platform end date first, the local one otherwise).
//
// It also returns the idle in-use accounts and the earliest plan end date
// that has not passed yet ("" when none is known).
func (a *App) fleetQuota(ctx context.Context, at time.Time, scope ownershipScope) (map[string]any, []quotaRow, map[string]int, []idleRow, string, error) {
	ownerSQL, ownerArgs := scope.sqlAnd("a.created_by_user_id")
	rows, err := a.DB.QueryContext(ctx, `SELECT a.id,a.account,COALESCE(a.plan_end_time,''),COALESCE(c.timezone,''),COALESCE(c.usd_cny_rate,?),
r.upstream_id IS NOT NULL,COALESCE(l.upstream_id,''),COALESCE(r.upstream_aid,''),COALESCE(r.missing_since,''),COALESCE(r.superseded_by,''),COALESCE(r.alias,''),COALESCE(NULLIF(r.ext_status,''),r.a1_status,''),COALESCE(r.plan_end_time,''),
COALESCE(a.created_by_user_id,''),COALESCE(u.nickname,''),COALESCE(u.username,''),
r.five_hour_used,COALESCE(r.five_hour_reset_at,''),r.week_used,COALESCE(r.week_reset_at,''),r.usage_cost_micro,r.usage_input_tokens,r.usage_output_tokens
FROM platform_links l JOIN accounts a ON a.id=l.account_id LEFT JOIN platform_connections c ON c.id=l.connection_id
LEFT JOIN platform_remote_accounts r ON r.connection_id=l.connection_id AND r.upstream_id=l.upstream_id
LEFT JOIN users u ON u.id=a.created_by_user_id
WHERE l.ingestion_state='accepted' AND a.archived=0`+ownerSQL+`
ORDER BY a.id,COALESCE(r.superseded_by,'')<>'',COALESCE(r.missing_since,'')<>'',l.updated_at DESC`, append([]any{defaultUSDCNYRate}, ownerArgs...)...)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	defer rows.Close()
	idle := []idleRow{}
	nextExpiry := ""
	expiry := map[string]int{"expired": 0, "within_7": 0, "within_30": 0, "later": 0, "unknown": 0}
	quota := []quotaRow{}
	seen := map[string]bool{}
	var costCents, inTokens, outTokens int64
	var fiveSum, weekSum float64
	var fiveN, weekN int
	for rows.Next() {
		var id, email, localEnd, zone, upstream, aid, missing, superseded, alias, status, remoteEnd, ownerID, nick, username, fiveReset, weekReset string
		var hasRemote bool
		var rate float64
		var five, week sql.NullFloat64
		var cost, in, out sql.NullInt64
		if err = rows.Scan(&id, &email, &localEnd, &zone, &rate, &hasRemote, &upstream, &aid, &missing, &superseded, &alias, &status, &remoteEnd, &ownerID, &nick, &username, &five, &fiveReset, &week, &weekReset, &cost, &in, &out); err != nil {
			return nil, nil, nil, nil, "", err
		}
		if seen[id] { // an older delivery of the same account
			continue
		}
		seen[id] = true

		end := remoteEnd
		if end == "" {
			end = localEnd
		}
		bucket := expiryBucket(end, at, zone)
		expiry[bucket]++
		if bucket != "expired" && bucket != "unknown" && (nextExpiry == "" || end < nextExpiry) {
			nextExpiry = end
		}

		if !hasRemote || missing != "" || superseded != "" {
			continue
		}
		platformID := platformAccountID(aid, upstream)
		q := quotaRow{
			AccountID: id, Account: email, Alias: alias, PlatformAccountID: platformID,
			CreatedByUserID: ownerID, CreatedByNickname: nick, CreatedByUsername: username,
			Status: status, FiveHourResetAt: fiveReset, WeekResetAt: weekReset,
		}
		if five.Valid {
			v := percent(five.Float64)
			q.FiveHourPercent = &v
			fiveSum += five.Float64
			fiveN++
		}
		if week.Valid {
			v := percent(week.Float64)
			q.WeekPercent = &v
			weekSum += week.Float64
			weekN++
		}
		if cost.Valid {
			cents, text := costUSDFields(microToFen(cost.Int64), rate)
			q.CostUsdCents = &cents
			q.CostUsd = text
			costCents += cents
		}
		inTokens += in.Int64
		outTokens += out.Int64
		quota = append(quota, q)
		if status == "using" && week.Valid && week.Float64 < idleWeekFraction && (!five.Valid || five.Float64 < idleWeekFraction) {
			row := idleRow{
				AccountID: id, Account: email, Alias: alias, PlatformAccountID: platformID,
				CreatedByUserID: ownerID, CreatedByNickname: nick, CreatedByUsername: username,
				WeekPercent: q.WeekPercent, CostUsdCents: q.CostUsdCents, CostUsd: q.CostUsd, PlanEnd: end,
			}
			if days, ok := daysUntil(end, at, zone); ok {
				row.DaysLeft = &days
			}
			idle = append(idle, row)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, nil, nil, nil, "", err
	}
	// The most plan days left unused is the biggest waste; unknown ends last.
	sort.SliceStable(idle, func(i, j int) bool {
		x, y := idle[i].DaysLeft, idle[j].DaysLeft
		switch {
		case x != nil && y != nil && *x != *y:
			return *x > *y
		case (x == nil) != (y == nil):
			return x != nil
		}
		return *idle[i].WeekPercent < *idle[j].WeekPercent
	})
	if len(idle) > insightsIdleLimit {
		idle = idle[:insightsIdleLimit]
	}
	sort.SliceStable(quota, func(i, j int) bool {
		x, y := quota[i].FiveHourPercent, quota[j].FiveHourPercent
		switch {
		case x != nil && y != nil && *x != *y:
			return *x > *y
		case (x == nil) != (y == nil):
			return x != nil
		}
		return quota[i].Account < quota[j].Account
	})
	fleet := map[string]any{
		"accounts": len(quota), "cost_usd_cents_total": costCents, "cost_usd_total": usdText(costCents),
		"input_tokens": inTokens, "output_tokens": outTokens,
		"avg_five_hour_percent": average(fiveSum, fiveN), "avg_week_percent": average(weekSum, weekN),
	}
	if len(quota) > insightsQuotaLimit {
		quota = quota[:insightsQuotaLimit]
	}
	return fleet, quota, expiry, idle, nextExpiry, nil
}

func average(sum float64, n int) float64 {
	if n == 0 {
		return 0
	}
	return percent(sum / float64(n))
}

// expiryBucket places a plan end date relative to today in the
// connection's time zone.
func expiryBucket(end string, at time.Time, zone string) string {
	days, ok := daysUntil(end, at, zone)
	if !ok {
		return "unknown"
	}
	switch {
	case days < 0:
		return "expired"
	case days <= 7:
		return "within_7"
	case days <= attentionExpiringDays:
		return "within_30"
	}
	return "later"
}

// hourlyBurn turns the usage snapshots into spend per hour for the last 24
// hours. A snapshot holds an account's cumulative cost, so the spend of an
// hour is the sum of the increases observed in it. The first snapshot of an
// account has nothing to compare with and a decrease (the counter was
// reset) is skipped.
func (a *App) hourlyBurn(ctx context.Context, at time.Time, scope ownershipScope) ([]map[string]any, error) {
	last := at.UTC().Truncate(time.Hour)
	first := last.Add(-(insightsHours - 1) * time.Hour)
	from := first.Format(time.RFC3339)
	ownerSQL := ""
	args := []any{at.UTC().Format(time.RFC3339), from, from}
	if !scope.Admin {
		ownerSQL = ` AND EXISTS(SELECT 1 FROM platform_links l JOIN accounts a ON a.id=l.account_id WHERE l.connection_id=s.connection_id AND l.upstream_id=s.upstream_id AND l.ingestion_state='accepted' AND a.created_by_user_id=?)`
		args = append(args, scope.UserID)
	}
	rows, err := a.DB.QueryContext(ctx, `SELECT s.connection_id,s.upstream_id,s.observed_at,s.usage_cost_micro,COALESCE(c.usd_cny_rate,?) FROM platform_usage_snapshots s
LEFT JOIN platform_connections c ON c.id=s.connection_id
WHERE s.usage_cost_micro IS NOT NULL AND s.observed_at<=? AND (s.observed_at>=? OR s.observed_at=(
	SELECT MAX(x.observed_at) FROM platform_usage_snapshots x
	WHERE x.connection_id=s.connection_id AND x.upstream_id=s.upstream_id AND x.observed_at<? AND x.usage_cost_micro IS NOT NULL))`+ownerSQL+`
ORDER BY s.connection_id,s.upstream_id,s.observed_at,s.rowid`, append([]any{defaultUSDCNYRate}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	buckets := make([]int64, insightsHours) // USD cents
	var prevKey string
	var prevCost int64
	var prevRate float64
	for rows.Next() {
		var conn, upstream, observed string
		var cost int64
		var rate float64
		if err = rows.Scan(&conn, &upstream, &observed, &cost, &rate); err != nil {
			return nil, err
		}
		key := conn + "\x00" + upstream
		if key == prevKey && cost > prevCost {
			if t, e := time.Parse(time.RFC3339, observed); e == nil {
				if i := int(t.UTC().Truncate(time.Hour).Sub(first) / time.Hour); i >= 0 && i < insightsHours {
					buckets[i] += fenToUsdCents(microToFen(cost-prevCost), prevRate)
				}
			}
		}
		prevKey, prevCost, prevRate = key, cost, rate
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]map[string]any, insightsHours)
	for i, cents := range buckets {
		out[i] = map[string]any{"hour": first.Add(time.Duration(i) * time.Hour).Format(time.RFC3339), "cost_usd_cents": cents, "cost_usd": usdText(cents)}
	}
	return out, nil
}

// deliveryCounts groups the non-archived accounts by onboarding state with
// the same rules as the account list.
func (a *App) deliveryCounts(ctx context.Context, scope ownershipScope) (map[string]int, error) {
	out := map[string]int{"pending": 0, "submitting": 0, "linked": 0, "failed": 0, "unknown": 0}
	ownerSQL, ownerArgs := scope.sqlAnd("t.created_by_user_id")
	rows, err := a.DB.QueryContext(ctx, `SELECT onboarding_state,COUNT(*) FROM (`+accountListBase+`) t WHERE archived=0`+ownerSQL+` GROUP BY onboarding_state`, ownerArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var n int
		if err = rows.Scan(&state, &n); err != nil {
			return nil, err
		}
		out[state] = n
	}
	return out, rows.Err()
}

// sharedUSDRate is the platform rate when every enabled connection uses
// the same one; nil otherwise, so fleet totals are shown in RMB only
// rather than converted at a rate that is wrong for part of them.
func (a *App) sharedUSDRate(ctx context.Context) (any, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT DISTINCT usd_cny_rate FROM platform_connections WHERE enabled=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rates []float64
	for rows.Next() {
		var r float64
		if err = rows.Scan(&r); err != nil {
			return nil, err
		}
		rates = append(rates, r)
	}
	if err = rows.Err(); err != nil || len(rates) != 1 {
		return nil, err
	}
	return rates[0], nil
}

// recentSubmitJobs lists the latest onboarding submissions. Only the
// outcome code is returned; platform messages stay in the task centre.
func (a *App) recentSubmitJobs(ctx context.Context, scope ownershipScope) ([]map[string]any, error) {
	ownerSQL, ownerArgs := scope.sqlAnd("a.created_by_user_id")
	args := append(append([]any{}, ownerArgs...), insightsJobLimit)
	rows, err := a.DB.QueryContext(ctx, `SELECT j.id,COALESCE(j.account_id,''),COALESCE(a.account,''),COALESCE(c.name,''),j.state,COALESCE(j.result_json,'{}'),COALESCE(j.updated_at,'')
FROM jobs j LEFT JOIN accounts a ON a.id=j.account_id LEFT JOIN platform_connections c ON c.id=j.connection_id
WHERE j.kind='account.submit'`+ownerSQL+` ORDER BY j.created_at DESC,j.rowid DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, accountID, account, connection, state, result, updated string
		if err = rows.Scan(&id, &accountID, &account, &connection, &state, &result, &updated); err != nil {
			return nil, err
		}
		var parsed struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal([]byte(result), &parsed)
		out = append(out, map[string]any{"id": id, "account_id": accountID, "account": account, "connection": connection, "state": state, "code": parsed.Code, "updated_at": updated})
	}
	return out, rows.Err()
}

// daysUntil counts whole days from today (in the connection's zone) to a
// YYYY-MM-DD plan end date; negative once it has passed.
func daysUntil(end string, at time.Time, zone string) (int, bool) {
	endDay, err := time.Parse("2006-01-02", end)
	if err != nil {
		return 0, false
	}
	today, _ := time.Parse("2006-01-02", at.In(connectionLocation(zone)).Format("2006-01-02"))
	return int(math.Round(endDay.Sub(today).Hours() / 24)), true
}
