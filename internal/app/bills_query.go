package app

import (
	"cc-manager/internal/errs"
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cc-manager/internal/supplier"
)

type billQueryRequest struct {
	ConnectionID    string   `json:"connection_id"`
	AccountIDs      []string `json:"account_ids"`
	From            string   `json:"from"`
	To              string   `json:"to"`
	Q               string   `json:"q"`
	IncludeArchived bool     `json:"include_archived"`
	RevisionMin     *int     `json:"revision_min"`
	Page            int      `json:"page"`
	PageSize        int      `json:"page_size"`
	Sort            string   `json:"sort"`
}

const billPlatformID = `COALESCE((SELECT NULLIF(NULLIF(m.upstream_aid,''),'0') FROM platform_metric_snapshots m WHERE m.account_id=b.account_id AND m.connection_id=b.connection_id AND m.upstream_id=b.upstream_id ORDER BY m.fetched_at DESC LIMIT 1),NULLIF(NULLIF(l.upstream_aid,''),'0'))`
const billJoin = ` FROM platform_bill_daily b JOIN accounts a ON a.id=b.account_id LEFT JOIN platform_links l ON l.account_id=b.account_id AND l.connection_id=b.connection_id LEFT JOIN bill_sync_buckets k ON k.account_id=b.account_id AND k.connection_id=b.connection_id AND k.bill_date=b.bill_date`

func (a *App) queryBills(w http.ResponseWriter, r *http.Request) {
	var in billQueryRequest
	if !decodeBillRequest(w, r, &in) {
		return
	}
	result, err := a.readBills(r.Context(), in)
	if err != nil {
		billFail(w, err)
		return
	}
	write(w, 200, result)
}

func (a *App) readBills(ctx context.Context, in billQueryRequest) (map[string]any, error) {
	if in.ConnectionID == "" {
		return nil, billField("connection_id", errs.FieldRequired, "请选择平台连接")
	}
	var zone string
	err := a.DB.QueryRowContext(ctx, `SELECT timezone FROM platform_connections WHERE id=?`, in.ConnectionID).Scan(&zone)
	if err != nil {
		return nil, errs.New(errs.CodeNotFound).WithMessage("平台连接不存在").WithDetail("resource", "platform_connection")
	}
	dates, err := supplier.ValidateBillDates(in.From, in.To, nil, time.Time{}, false)
	if err != nil {
		return nil, billBad("BILL_RANGE_INVALID", "账单起止日期无效")
	}
	in.From = dates.StartDate
	in.To = dates.EndDate
	if in.Page == 0 {
		in.Page = 1
	}
	if in.PageSize == 0 {
		in.PageSize = 20
	}
	if in.Page < 1 || in.Page > 1000000 || (in.PageSize != 10 && in.PageSize != 20 && in.PageSize != 50 && in.PageSize != 100) {
		return nil, queryInvalid("page", "分页参数无效")
	}
	order := `b.bill_date DESC,b.account_id,b.id`
	if in.Sort == "bill_date_asc" {
		order = `b.bill_date ASC,b.account_id,b.id`
	} else if in.Sort != "" && in.Sort != "bill_date_desc" {
		return nil, queryInvalid("sort", "排序参数无效")
	}
	if in.RevisionMin != nil && *in.RevisionMin < 0 {
		return nil, queryInvalid("revision_min", "修订次数不能为负数")
	}
	in.AccountIDs = uniqueSorted(in.AccountIDs)
	if len(in.AccountIDs) > 10000 {
		return nil, billBad("QUERY_SCOPE_TOO_LARGE", "单次最多选择 10000 个账号，请缩小范围")
	}
	where := ` WHERE b.connection_id=? AND b.bill_date>=? AND b.bill_date<=?`
	args := []any{in.ConnectionID, in.From, in.To}
	if !in.IncludeArchived {
		where += ` AND a.archived=0`
	}
	if len(in.AccountIDs) > 0 {
		where += ` AND b.account_id IN (` + marks(len(in.AccountIDs)) + `)`
		for _, id := range in.AccountIDs {
			args = append(args, id)
		}
	}
	coverageWhere, coverageArgs := where, append([]any{}, args...)
	if in.RevisionMin != nil {
		where += ` AND b.revision>=?`
		args = append(args, *in.RevisionMin)
	}
	if in.Q != "" {
		where += ` AND (instr(lower(a.account),lower(?))>0 OR instr(lower(COALESCE(l.alias,'')),lower(?))>0 OR instr(b.upstream_id,?)>0 OR instr(COALESCE(` + billPlatformID + `,''),?)>0)`
		for i := 0; i < 4; i++ {
			args = append(args, strings.TrimSpace(in.Q))
		}
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var total int
	var sums [6]int64
	err = tx.QueryRow(`SELECT COUNT(*),COALESCE(SUM(b.request_count),0),COALESCE(SUM(b.prompt_tokens),0),COALESCE(SUM(b.completion_tokens),0),COALESCE(SUM(b.cache_read_input_tokens),0),COALESCE(SUM(b.cache_write_input_tokens),0),COALESCE(SUM(b.cost_fen),0)`+billJoin+where, args...).Scan(&total, &sums[0], &sums[1], &sums[2], &sums[3], &sums[4], &sums[5])
	if err != nil {
		return nil, err
	}
	query := `SELECT b.id,b.account_id,a.account,b.upstream_id,` + billPlatformID + `,COALESCE(l.alias,''),b.bill_date,b.request_count,b.prompt_tokens,b.completion_tokens,b.cache_read_input_tokens,b.cache_write_input_tokens,b.cost_fen,b.revision,b.fetched_at,b.response_verified,COALESCE(b.source_timezone,''),COALESCE(k.last_state,''),COALESCE(k.last_attempt_at,''),COALESCE(b.actual_start_ds,''),COALESCE(b.actual_end_ds,'')` + billJoin + where + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?`
	rows, err := tx.Query(query, append(append([]any{}, args...), in.PageSize, (in.Page-1)*in.PageSize)...)
	if err != nil {
		return nil, err
	}
	items := []any{}
	for rows.Next() {
		var id, account, name, upstream, alias, day, fetched, sourceZone, lastState, lastAttempt, actualStart, actualEnd string
		var aid sql.NullString
		var values [6]int64
		var revision, verified int
		err = rows.Scan(&id, &account, &name, &upstream, &aid, &alias, &day, &values[0], &values[1], &values[2], &values[3], &values[4], &values[5], &revision, &fetched, &verified, &sourceZone, &lastState, &lastAttempt, &actualStart, &actualEnd)
		if err != nil {
			break
		}
		state := "verified"
		if verified == 0 {
			state = "legacy_unverified"
		} else if sourceZone != zone {
			state = "timezone_changed"
		} else if lastState == "failed" || lastState == "no_data" {
			state = lastState
		} else if billStale(day, fetched, zone) {
			state = "stale"
		}
		item := map[string]any{"id": id, "account_id": account, "account": name, "connection_id": in.ConnectionID, "supplier_account_id": upstream, "platform_account_id": nil, "alias": alias, "bill_date": day, "revision": revision, "fetched_at": fetched, "response_verified": verified == 1, "data_state": state, "last_sync_state": lastState, "last_attempt_at": lastAttempt, "source_timezone": sourceZone, "actual_range": map[string]string{"start_ds": actualStart, "end_ds": actualEnd}, "finalized": false}
		if aid.Valid {
			item["platform_account_id"] = aid.String
		}
		for i, key := range []string{"request_count", "prompt_tokens", "completion_tokens", "cache_read_input_tokens", "cache_write_input_tokens", "cost_fen"} {
			item[key] = strconv.FormatInt(values[i], 10)
		}
		items = append(items, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	coverage, err := billCoverage(tx, in, zone, coverageWhere, coverageArgs)
	if err != nil {
		return nil, err
	}
	totals := map[string]any{"is_partial": !coverage["complete"].(bool), "currency": "CNY", "scope": "all_matching_saved_rows"}
	for i, key := range []string{"request_count", "prompt_tokens", "completion_tokens", "cache_read_input_tokens", "cache_write_input_tokens", "cost_fen"} {
		totals[key] = strconv.FormatInt(sums[i], 10)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"items": items, "pagination": map[string]int{"page": in.Page, "page_size": in.PageSize, "total": total}, "totals": totals, "coverage": coverage, "timezone": zone}, nil
}

func billStale(day, fetched, zone string) bool {
	loc, e := time.LoadLocation(zone)
	if e != nil {
		return true
	}
	stamp, e := time.Parse(time.RFC3339, fetched)
	if e != nil {
		return true
	}
	limit := 30 * time.Hour
	if day >= time.Now().In(loc).AddDate(0, 0, -2).Format("2006-01-02") {
		limit = 6 * time.Hour
	}
	return time.Since(stamp) > limit
}

func billCoverage(tx *sql.Tx, in billQueryRequest, zone, where string, args []any) (map[string]any, error) {
	// Account/date coverage deliberately excludes keyword and revision filters.
	universe := `SELECT account_id FROM platform_links WHERE connection_id=? AND ingestion_state='accepted' UNION SELECT account_id FROM platform_bill_daily WHERE connection_id=? AND bill_date>=? AND bill_date<=? UNION SELECT account_id FROM bill_sync_buckets WHERE connection_id=? AND bill_date>=? AND bill_date<=?`
	query := `SELECT COUNT(*) FROM accounts a JOIN (` + universe + `) u ON u.account_id=a.id WHERE 1=1`
	scopeArgs := []any{in.ConnectionID, in.ConnectionID, in.From, in.To, in.ConnectionID, in.From, in.To}
	if !in.IncludeArchived {
		query += ` AND a.archived=0`
	}
	if len(in.AccountIDs) > 0 {
		query += ` AND a.id IN (` + marks(len(in.AccountIDs)) + `)`
		for _, id := range in.AccountIDs {
			scopeArgs = append(scopeArgs, id)
		}
	}
	var accounts int64
	if err := tx.QueryRow(query, scopeArgs...).Scan(&accounts); err != nil {
		return nil, err
	}
	from, _ := time.Parse("2006-01-02", in.From)
	to, _ := time.Parse("2006-01-02", in.To)
	days := int64(to.Sub(from)/(24*time.Hour)) + 1
	coverage := map[string]any{"scope": "account_date_selection", "account_count": accounts, "expected_items": accounts * days, "verified_items": int64(0), "legacy_unverified_items": int64(0), "no_data_items": int64(0), "failed_items": int64(0), "stale_items": int64(0), "missing_items": int64(0), "latest_fetched_at": "", "complete": false}
	rows, err := tx.Query(`SELECT b.bill_date,b.fetched_at,b.response_verified,COALESCE(b.source_timezone,''),COALESCE(k.last_state,'')`+billJoin+where, args...)
	if err != nil {
		return nil, err
	}
	var saved, verified, legacy, stale int64
	latest := ""
	for rows.Next() {
		var day, fetched, sourceZone, state string
		var checked int
		if err = rows.Scan(&day, &fetched, &checked, &sourceZone, &state); err != nil {
			break
		}
		saved++
		if fetched > latest {
			latest = fetched
		}
		if checked == 0 {
			legacy++
		} else if sourceZone != zone || billStale(day, fetched, zone) {
			stale++
		} else if state != "failed" && state != "no_data" {
			verified++
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	bucketQuery := `SELECT k.last_state,COUNT(*) FROM bill_sync_buckets k JOIN accounts a ON a.id=k.account_id WHERE k.connection_id=? AND k.bill_date>=? AND k.bill_date<=?`
	bucketArgs := []any{in.ConnectionID, in.From, in.To}
	if !in.IncludeArchived {
		bucketQuery += ` AND a.archived=0`
	}
	if len(in.AccountIDs) > 0 {
		bucketQuery += ` AND k.account_id IN (` + marks(len(in.AccountIDs)) + `)`
		for _, id := range in.AccountIDs {
			bucketArgs = append(bucketArgs, id)
		}
	}
	bucketQuery += ` GROUP BY k.last_state`
	rows, err = tx.Query(bucketQuery, bucketArgs...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var state string
		var n int64
		if err = rows.Scan(&state, &n); err != nil {
			break
		}
		if state == "failed" {
			coverage["failed_items"] = n
		}
		if state == "no_data" {
			coverage["no_data_items"] = n
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	coverage["verified_items"] = verified
	coverage["legacy_unverified_items"] = legacy
	coverage["stale_items"] = stale
	coverage["missing_items"] = accounts*days - saved
	coverage["latest_fetched_at"] = latest
	coverage["complete"] = accounts > 0 && verified == accounts*days
	return coverage, nil
}

func (a *App) billAccounts(w http.ResponseWriter, r *http.Request) {
	conn := r.URL.Query().Get("connection_id")
	if conn == "" {
		billFail(w, billField("connection_id", errs.FieldRequired, "请选择平台连接"))
		return
	}
	query := `SELECT a.id,a.account,a.archived,COALESCE(l.upstream_id,''),COALESCE(l.alias,''),CASE WHEN l.ingestion_state='accepted' THEN 1 ELSE 0 END FROM accounts a LEFT JOIN platform_links l ON l.account_id=a.id AND l.connection_id=? WHERE (l.id IS NOT NULL OR EXISTS(SELECT 1 FROM platform_bill_daily b WHERE b.account_id=a.id AND b.connection_id=?))`
	if r.URL.Query().Get("include_archived") != "true" {
		query += ` AND a.archived=0`
	}
	query += ` ORDER BY a.account,a.id`
	rows, err := a.DB.QueryContext(r.Context(), query, conn, conn)
	if err != nil {
		billFail(w, err)
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, name, upstream, alias string
		var archived, linked int
		if err = rows.Scan(&id, &name, &archived, &upstream, &alias, &linked); err != nil {
			break
		}
		items = append(items, map[string]any{"id": id, "account": name, "supplier_account_id": upstream, "alias": alias, "archived": archived == 1, "linked": linked == 1})
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
