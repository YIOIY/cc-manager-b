package app

import (
	"cc-manager/internal/errs"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func requireBillMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		w.Header().Set("Allow", method)
		fail(w, 405, "METHOD_NOT_ALLOWED", "不支持的请求方法")
		return false
	}
	return true
}
func (a *App) billRoutes(w http.ResponseWriter, r *http.Request, path string) bool {
	switch path {
	case "/bills/summary":
		if requireBillMethod(w, r, "POST") {
			a.queryBillSummary(w, r)
		}
		return true
	case "/bills/query":
		if requireBillMethod(w, r, "POST") {
			a.queryBills(w, r)
		}
		return true
	case "/bills/accounts":
		if requireBillMethod(w, r, "GET") {
			a.billAccounts(w, r)
		}
		return true
	case "/bills/models":
		if requireBillMethod(w, r, "GET") {
			a.billModels(w, r)
		}
		return true
	case "/bills/models/sync":
		if requireBillMethod(w, r, "POST") {
			a.billModelsSync(w, r)
		}
		return true
	case "/bills/sync":
		if requireBillMethod(w, r, "POST") {
			var in billSyncRequest
			if decodeBillRequest(w, r, &in) {
				result, err := a.createBillRun(r.Context(), in, r.Header.Get("Idempotency-Key"), "")
				if err != nil {
					billFail(w, err)
				} else {
					write(w, 202, result)
				}
			}
		}
		return true
	}
	if strings.HasPrefix(path, "/sync-runs/") {
		parts := strings.Split(strings.TrimPrefix(path, "/sync-runs/"), "/")
		id := parts[0]
		if len(parts) == 1 {
			if requireBillMethod(w, r, "GET") {
				result, err := a.billRunDetail(id)
				if err != nil {
					billFail(w, err)
				} else {
					write(w, 200, result)
				}
			}
			return true
		}
		if len(parts) == 2 && parts[1] == "items" {
			if requireBillMethod(w, r, "GET") {
				a.billRunItems(w, r, id)
			}
			return true
		}
		if len(parts) == 2 && parts[1] == "retry-failed" {
			if requireBillMethod(w, r, "POST") {
				a.retryBillRun(w, r, id)
			}
			return true
		}
		fail(w, 404, "NOT_FOUND", "任务资源不存在")
		return true
	}
	return false
}

func (a *App) billRunDetail(id string) (map[string]any, error) {
	var conn, state, payload, created, started, finished, parent string
	var total, requests, retries int
	err := a.DB.QueryRow(`SELECT connection_id,state,request_json,COALESCE(created_at,''),started_at,COALESCE(finished_at,''),COALESCE(parent_id,''),total_items,http_requests,http_retries FROM sync_runs WHERE id=? AND kind='bill'`, id).Scan(&conn, &state, &payload, &created, &started, &finished, &parent, &total, &requests, &retries)
	if err == sql.ErrNoRows {
		return nil, errs.New(errs.CodeNotFound).WithMessage("账单同步任务不存在").WithDetail("resource", "bill_run")
	}
	if err != nil {
		return nil, err
	}
	counts := map[string]int{"queued": 0, "running": 0, "updated": 0, "unchanged": 0, "no_data": 0, "failed": 0, "superseded": 0}
	rows, err := a.DB.Query(`SELECT state,COUNT(*) FROM bill_sync_items WHERE run_id=? GROUP BY state`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s string
		var n int
		if err = rows.Scan(&s, &n); err != nil {
			break
		}
		counts[s] = n
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	var spec billRunSpec
	if err = json.Unmarshal([]byte(payload), &spec); err != nil {
		return nil, err
	}
	completed := total - counts["queued"] - counts["running"]
	return map[string]any{"run_id": id, "connection_id": conn, "state": state, "counts": counts, "total_items": total, "completed": completed, "http_requests": requests, "http_retries": retries, "created_at": created, "started_at": started, "finished_at": finished, "parent_id": parent, "from": spec.From, "to": spec.To, "timezone": spec.Timezone, "account_count": len(spec.AccountIDs)}, nil
}

func (a *App) billRunList(w http.ResponseWriter, r *http.Request) {
	if !requireBillMethod(w, r, "GET") {
		return
	}
	if r.URL.Query().Get("kind") != "bill" {
		a.legacySyncRunList(w, r)
		return
	}
	query := `SELECT id FROM sync_runs WHERE kind='bill'`
	args := []any{}
	if conn := r.URL.Query().Get("connection_id"); conn != "" {
		query += ` AND connection_id=?`
		args = append(args, conn)
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT 20`
	rows, err := a.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		billFail(w, err)
		return
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
		billFail(w, err)
		return
	}
	items := []any{}
	for _, id := range ids {
		result, e := a.billRunDetail(id)
		if e != nil {
			billFail(w, e)
			return
		}
		items = append(items, result)
	}
	write(w, 200, items)
}

func (a *App) legacySyncRunList(w http.ResponseWriter, r *http.Request) {
	query := `SELECT id,connection_id,kind,state,started_at,COALESCE(finished_at,''),COALESCE(last_success_at,''),COALESCE(error_message,''),processed,failed FROM sync_runs WHERE 1=1`
	args := []any{}
	if conn := r.URL.Query().Get("connection_id"); conn != "" {
		query += ` AND connection_id=?`
		args = append(args, conn)
	}
	if kind := r.URL.Query().Get("kind"); kind != "" {
		query += ` AND kind=?`
		args = append(args, kind)
	}
	rows, err := a.DB.QueryContext(r.Context(), query+` ORDER BY started_at DESC,id DESC LIMIT 100`, args...)
	if err != nil {
		billFail(w, err)
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, conn, kind, state, started, finished, last, message string
		var processed, failed int
		if err = rows.Scan(&id, &conn, &kind, &state, &started, &finished, &last, &message, &processed, &failed); err != nil {
			break
		}
		items = append(items, map[string]any{"id": id, "connection_id": conn, "kind": kind, "state": state, "started_at": started, "finished_at": finished, "last_success_at": last, "error_message": message, "processed": processed, "failed": failed})
	}
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		billFail(w, err)
		return
	}
	write(w, 200, items)
}

func pageArgs(r *http.Request) (int, int, error) {
	page, size := 1, 20
	var err error
	if v := r.URL.Query().Get("page"); v != "" {
		page, err = strconv.Atoi(v)
		if err != nil || page < 1 {
			return 0, 0, queryInvalid("page", "页码无效")
		}
	}
	if v := r.URL.Query().Get("page_size"); v != "" {
		size, err = strconv.Atoi(v)
		if err != nil {
			return 0, 0, queryInvalid("page_size", "每页数量无效")
		}
	}
	if size != 10 && size != 20 && size != 50 && size != 100 {
		return 0, 0, queryInvalid("page_size", "每页数量应为 10、20、50 或 100")
	}
	if page > 1000000 {
		return 0, 0, queryInvalid("page", "页码过大")
	}
	return page, size, nil
}
func (a *App) billRunItems(w http.ResponseWriter, r *http.Request, id string) {
	if _, err := a.billRunDetail(id); err != nil {
		billFail(w, err)
		return
	}
	page, size, err := pageArgs(r)
	if err != nil {
		billFail(w, err)
		return
	}
	where := ` WHERE i.run_id=?`
	args := []any{id}
	if state := r.URL.Query().Get("state"); state != "" {
		switch state {
		case "queued", "running", "updated", "unchanged", "no_data", "failed", "superseded":
		default:
			billFail(w, queryInvalid("state", "任务项状态无效"))
			return
		}
		where += ` AND i.state=?`
		args = append(args, state)
	}
	var total int
	if err = a.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM bill_sync_items i`+where, args...).Scan(&total); err != nil {
		billFail(w, err)
		return
	}
	rows, err := a.DB.QueryContext(r.Context(), `SELECT i.account_id,COALESCE(a.account,''),i.upstream_id,i.bill_date,i.state,i.attempts,i.error_code,i.error_message FROM bill_sync_items i LEFT JOIN accounts a ON a.id=i.account_id`+where+` ORDER BY i.bill_date DESC,i.account_id LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		billFail(w, err)
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var account, name, upstream, day, state, code, message string
		var attempts int
		if err = rows.Scan(&account, &name, &upstream, &day, &state, &attempts, &code, &message); err != nil {
			break
		}
		if message == "" && code != "" {
			if def, ok := errs.Lookup(code); ok {
				message = def.Message
			}
		}
		items = append(items, map[string]any{"account_id": account, "account": name, "supplier_account_id": upstream, "bill_date": day, "state": state, "attempts": attempts, "error_code": code, "error_message": message})
	}
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		billFail(w, err)
		return
	}
	write(w, 200, map[string]any{"items": items, "pagination": map[string]int{"page": page, "page_size": size, "total": total}})
}

func (a *App) retryBillRun(w http.ResponseWriter, r *http.Request, id string) {
	var payload, state string
	err := a.DB.QueryRowContext(r.Context(), `SELECT request_json,state FROM sync_runs WHERE id=? AND kind='bill'`, id).Scan(&payload, &state)
	if err == sql.ErrNoRows {
		billFail(w, errs.New(errs.CodeNotFound).WithMessage("账单同步任务不存在").WithDetail("resource", "bill_run"))
		return
	}
	if err != nil {
		billFail(w, err)
		return
	}
	if state == "queued" || state == "running" {
		billFail(w, errs.New(errs.CodeRunActive))
		return
	}
	var spec billRunSpec
	if json.Unmarshal([]byte(payload), &spec) != nil {
		billFail(w, errs.New(errs.CodeInternalError).WithMessage("任务参数无法读取"))
		return
	}
	loc, err := loadBillLocation(spec.Timezone)
	if err != nil {
		billFail(w, err)
		return
	}
	lower := time.Now().In(loc).AddDate(0, 0, -90).Format("2006-01-02")
	rows, err := a.DB.QueryContext(r.Context(), `SELECT bill_date,account_id FROM bill_sync_items WHERE run_id=? AND state='failed' AND bill_date>=? ORDER BY bill_date,account_id`, id, lower)
	if err != nil {
		billFail(w, err)
		return
	}
	byDay := map[string][]string{}
	for rows.Next() {
		var day, account string
		if err = rows.Scan(&day, &account); err != nil {
			break
		}
		byDay[day] = append(byDay[day], account)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		billFail(w, err)
		return
	}
	if len(byDay) == 0 {
		billFail(w, billBad("NO_RETRY_ITEMS", "没有仍在可查窗口内的失败项"))
		return
	}
	// Separate per-day tasks preserve the exact failed set, rather than creating
	// the cross product of all failed accounts and dates.
	days := []string{}
	for day := range byDay {
		days = append(days, day)
	}
	sort.Strings(days)
	runs := []any{}
	for _, day := range days {
		input := spec.billSyncRequest
		input.Scope = "selected"
		input.AccountIDs = byDay[day]
		input.From = day
		input.To = day
		key := r.Header.Get("Idempotency-Key")
		if key != "" {
			key = fmt.Sprintf("%s:%s", key, day)
		}
		result, e := a.createBillRun(r.Context(), input, key, id)
		if e != nil {
			if len(runs) > 0 {
				write(w, 202, map[string]any{"runs": runs, "partial": true, "message": "部分失败项已排队，其余请检查连接后重试"})
			} else {
				billFail(w, e)
			}
			return
		}
		runs = append(runs, result)
	}
	write(w, 202, map[string]any{"runs": runs})
}

func (a *App) scheduleBills(ctx context.Context) error {
	rows, err := a.DB.QueryContext(ctx, `SELECT c.id,c.timezone,COALESCE(MAX(CASE WHEN r.parent_id='schedule-recent' THEN r.created_at END),''),COALESCE(MAX(CASE WHEN r.parent_id='schedule-weekly' THEN r.created_at END),'') FROM platform_connections c LEFT JOIN sync_runs r ON r.connection_id=c.id AND r.kind='bill' WHERE c.enabled=1 GROUP BY c.id,c.timezone`)
	if err != nil {
		return err
	}
	type candidate struct{ id, zone, recent, weekly string }
	all := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.zone, &c.recent, &c.weekly); err != nil {
			break
		}
		all = append(all, c)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range all {
		loc, e := loadBillLocation(c.zone)
		if e != nil {
			continue
		}
		today := time.Now().In(loc)
		for _, period := range []struct {
			last, kind string
			days       int
			delay      time.Duration
		}{{c.recent, "schedule-recent", 3, 2 * time.Hour}, {c.weekly, "schedule-weekly", 7, 24 * time.Hour}} {
			last, e := time.Parse(time.RFC3339, period.last)
			if e == nil && time.Since(last) < period.delay {
				continue
			}
			var count int
			if e = a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM platform_links l JOIN accounts a ON a.id=l.account_id WHERE l.connection_id=? AND l.ingestion_state='accepted' AND a.archived=0`, c.id).Scan(&count); e != nil {
				return e
			}
			if count == 0 {
				continue
			}
			_, e = a.createBillRun(ctx, billSyncRequest{ConnectionID: c.id, Scope: "all_linked", From: today.AddDate(0, 0, -period.days+1).Format("2006-01-02"), To: today.Format("2006-01-02")}, "", period.kind)
			if e != nil {
				return e
			}
		}
	}
	return nil
}
