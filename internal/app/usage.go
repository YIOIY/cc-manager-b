package app

import (
	"database/sql"
	"net/http"
	"strings"
	"time"
)

// platformUsage serves the read-only platform observation domain.
func (a *App) platformUsage(w http.ResponseWriter, r *http.Request, rest string) {
	if rest == "" || rest == "/" {
		write(w, 200, map[string]any{"sources": []string{"metrics", "bills"}, "billing_status": "计费源未接入"})
		return
	}
	if a.billRoutes(w, r, rest) {
		return
	}
	switch rest {
	case "/metrics":
		if r.Method != http.MethodGet {
			fail(w, 405, "METHOD_NOT_ALLOWED", "不支持的请求方法")
			return
		}
		a.platformMetrics(w, r)
	case "/bills":
		if r.Method != http.MethodGet {
			fail(w, 405, "METHOD_NOT_ALLOWED", "不支持的请求方法")
			return
		}
		a.platformBills(w, r)
	case "/sync-runs":
		a.billRunList(w, r)
	default:
		fail(w, 404, "NOT_FOUND", "资源不存在")
	}
}

func (a *App) platformMetrics(w http.ResponseWriter, r *http.Request) {
	query := `SELECT m.account_id,m.connection_id,m.upstream_id,m.upstream_aid,m.alias,m.runtime_status,m.runtime_status_raw,m.window_start,m.window_end,m.requests,m.prompt_tokens,m.completion_tokens,m.success_rate,m.fetched_at,m.freshness_state,a.account FROM platform_metric_snapshots m JOIN accounts a ON a.id=m.account_id WHERE 1=1`
	args := []any{}
	if v := strings.TrimSpace(r.URL.Query().Get("account_id")); v != "" {
		query += " AND m.account_id=?"
		args = append(args, v)
	}
	query += " ORDER BY m.fetched_at DESC LIMIT 1000"
	rows, err := a.DB.Query(query, args...)
	if err != nil {
		writeError(w, r, storageError(nil, "读取平台指标失败"))
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var accountID, conn, upstream, upstreamAID, alias, status, raw, start, end, fetched, fresh, account string
		var req, prompt, completion sql.NullInt64
		var rate sql.NullFloat64
		if rows.Scan(&accountID, &conn, &upstream, &upstreamAID, &alias, &status, &raw, &start, &end, &req, &prompt, &completion, &rate, &fetched, &fresh, &account) == nil {
			if t, e := time.Parse(time.RFC3339, fetched); e == nil && time.Since(t) > 5*time.Minute && fresh == "fresh" {
				fresh = "stale"
			}
			platformID := upstreamAID
			if platformID == "" {
				platformID = upstream
			}
			m := map[string]any{"account_id": accountID, "account": account, "connection_id": conn, "delivery_id": upstream, "platform_account_id": platformID, "alias": alias, "status": status, "status_raw": raw, "window_start": start, "window_end": end, "fetched_at": fetched, "freshness_state": fresh}
			if req.Valid {
				m["requests"] = req.Int64
			}
			if prompt.Valid {
				m["prompt_tokens"] = prompt.Int64
			}
			if completion.Valid {
				m["completion_tokens"] = completion.Int64
			}
			if rate.Valid {
				m["success_rate_percent"] = rate.Float64
			}
			out = append(out, m)
		}
	}
	write(w, 200, out)
}

func (a *App) platformBills(w http.ResponseWriter, r *http.Request) {
	query := `SELECT b.account_id,b.connection_id,b.upstream_id,b.bill_date,b.request_count,b.prompt_tokens,b.completion_tokens,b.cache_read_input_tokens,b.cache_write_input_tokens,b.cost_fen,b.fetched_at,b.revision,b.finalized,a.account,COALESCE(c.usd_cny_rate,?) FROM platform_bill_daily b JOIN accounts a ON a.id=b.account_id LEFT JOIN platform_connections c ON c.id=b.connection_id WHERE 1=1`
	args := []any{defaultUSDCNYRate}
	if v := strings.TrimSpace(r.URL.Query().Get("account_id")); v != "" {
		query += " AND b.account_id=?"
		args = append(args, v)
	}
	if v := strings.TrimSpace(r.URL.Query().Get("from")); v != "" {
		query += " AND b.bill_date>=?"
		args = append(args, v)
	}
	if v := strings.TrimSpace(r.URL.Query().Get("to")); v != "" {
		query += " AND b.bill_date<=?"
		args = append(args, v)
	}
	query += " ORDER BY b.bill_date DESC,b.fetched_at DESC LIMIT 1000"
	rows, err := a.DB.Query(query, args...)
	if err != nil {
		writeError(w, r, storageError(nil, "读取平台账单失败"))
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var accountID, conn, upstream, date, fetched, account string
		var req, prompt, completion, read, writeN, cost, rev int64
		var final int
		var rate float64
		if rows.Scan(&accountID, &conn, &upstream, &date, &req, &prompt, &completion, &read, &writeN, &cost, &fetched, &rev, &final, &account, &rate) == nil {
			cents, text := costUSDFields(cost, rate)
			out = append(out, map[string]any{"account_id": accountID, "account": account, "connection_id": conn, "platform_account_id": upstream, "bill_date": date, "request_count": req, "prompt_tokens": prompt, "completion_tokens": completion, "cache_read_input_tokens": read, "cache_write_input_tokens": writeN, "cost_usd_cents": cents, "cost_usd": text, "fetched_at": fetched, "revision": rev, "finalized": final == 1})
		}
	}
	write(w, 200, out)
}
