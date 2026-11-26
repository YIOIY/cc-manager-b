package app

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"cc-manager/internal/errs"
	"cc-manager/internal/supplier"
)

// Soft cap so a wide date range cannot dump unbounded rows into memory.
const channelUsageExportMaxRows = 50000

// platformAccountID prefers the console aid (same rule as /accounts cards),
// falling back to the delivery upstream_id.
func platformAccountID(aid, upstream string) string {
	if a := strings.TrimSpace(aid); a != "" && a != "0" {
		return a
	}
	return strings.TrimSpace(upstream)
}

// channelUsageAccounts answers GET /platform-usage/channel-usage/accounts:
// upstream deliveries still listed on the console for a connection, with the
// same display id as /accounts and optional owner info for the admin filter.
func (a *App) channelUsageAccounts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	connectionID := strings.TrimSpace(r.URL.Query().Get("connection_id"))
	if connectionID == "" {
		writeError(w, r, billField("connection_id", errs.FieldRequired, "请选择平台连接"))
		return
	}
	var n int
	if err := a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM platform_connections WHERE id=?`, connectionID).Scan(&n); err != nil || n == 0 {
		writeError(w, r, errs.New(errs.CodeNotFound).WithMessage("平台连接不存在").WithDetail("resource", "platform_connection"))
		return
	}
	scope := ownershipFrom(r)
	query := `SELECT r.upstream_id,COALESCE(r.upstream_aid,''),COALESCE(NULLIF(a.account,''),r.account_email,''),COALESCE(r.account_id,''),COALESCE(a.created_by_user_id,''),COALESCE(u.nickname,''),COALESCE(u.username,'')
FROM platform_remote_accounts r
LEFT JOIN accounts a ON a.id=r.account_id
LEFT JOIN users u ON u.id=a.created_by_user_id
WHERE r.connection_id=? AND r.missing_since=''`
	args := []any{connectionID}
	if frag, ownerArgs := scope.sqlAnd("a.created_by_user_id"); frag != "" {
		query += frag
		args = append(args, ownerArgs...)
	}
	query += ` ORDER BY CAST(r.upstream_id AS INTEGER) DESC,r.upstream_id DESC`
	rows, err := a.DB.QueryContext(ctx, query, args...)
	if err != nil {
		writeError(w, r, storageError(err, "账号列表读取失败"))
		return
	}
	list := []any{}
	owners := map[string]map[string]any{}
	for rows.Next() {
		var upstream, aid, account, accountID, ownerID, nick, username string
		if err = rows.Scan(&upstream, &aid, &account, &accountID, &ownerID, &nick, &username); err != nil {
			rows.Close()
			writeError(w, r, storageError(err, "账号列表读取失败"))
			return
		}
		item := map[string]any{
			"upstream_id":         upstream,
			"platform_account_id": platformAccountID(aid, upstream),
			"account":             account,
			"account_id":          accountID,
			"owner_user_id":       ownerID,
			"owner_nickname":      nick,
			"owner_username":      username,
		}
		list = append(list, item)
		if scope.Admin && ownerID != "" {
			if owners[ownerID] == nil {
				owners[ownerID] = map[string]any{"id": ownerID, "nickname": nick, "username": username}
			}
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		writeError(w, r, storageError(err, "账号列表读取失败"))
		return
	}
	rows.Close()
	out := map[string]any{"list": list}
	if scope.Admin {
		ownerList := make([]any, 0, len(owners))
		for _, o := range owners {
			ownerList = append(ownerList, o)
		}
		sort.Slice(ownerList, func(i, j int) bool {
			a, b := ownerList[i].(map[string]any), ownerList[j].(map[string]any)
			an, bn := a["nickname"].(string), b["nickname"].(string)
			if an == "" {
				an = a["username"].(string)
			}
			if bn == "" {
				bn = b["username"].(string)
			}
			if an != bn {
				return an < bn
			}
			return a["id"].(string) < b["id"].(string)
		})
		out["owners"] = ownerList
	}
	write(w, http.StatusOK, out)
}

type channelUsageFilter struct {
	connectionID string
	rate         float64
	dates        supplier.BillDateRange
	where        string
	args         []any
	base         string
	grouped      string
}

// parseChannelUsageFilter validates connection/date/owner/upstream filters for
// both the paged JSON list and the Excel export.
func (a *App) parseChannelUsageFilter(w http.ResponseWriter, r *http.Request) (*channelUsageFilter, bool) {
	ctx := r.Context()
	q := r.URL.Query()
	connectionID := strings.TrimSpace(q.Get("connection_id"))
	if connectionID == "" {
		writeError(w, r, billField("connection_id", errs.FieldRequired, "请选择平台连接"))
		return nil, false
	}
	var rate float64
	if err := a.DB.QueryRowContext(ctx, `SELECT COALESCE(usd_cny_rate,?) FROM platform_connections WHERE id=?`, defaultUSDCNYRate, connectionID).Scan(&rate); err != nil {
		writeError(w, r, errs.New(errs.CodeNotFound).WithMessage("平台连接不存在").WithDetail("resource", "platform_connection"))
		return nil, false
	}
	if rate <= 0 {
		rate = defaultUSDCNYRate
	}
	dates, err := supplier.ValidateBillDates(q.Get("from"), q.Get("to"), nil, time.Time{}, false)
	if err != nil {
		writeError(w, r, billBad("BILL_RANGE_INVALID", "账单起止日期无效"))
		return nil, false
	}
	scope := ownershipFrom(r)
	where := ` WHERE b.connection_id=? AND b.bill_date>=? AND b.bill_date<=?`
	args := []any{connectionID, dates.StartDate, dates.EndDate}
	if frag, ownerArgs := scope.sqlAnd("a.created_by_user_id"); frag != "" {
		where += frag
		args = append(args, ownerArgs...)
	} else {
		ownerRaw := strings.TrimSpace(q.Get("owner_user_ids"))
		if ownerRaw == "" {
			ownerRaw = strings.TrimSpace(q.Get("owner_user_id"))
		}
		if ownerRaw != "" {
			owners := uniqueSorted(strings.Split(ownerRaw, ","))
			cleaned := make([]string, 0, len(owners))
			for _, id := range owners {
				id = strings.TrimSpace(id)
				if id != "" {
					cleaned = append(cleaned, id)
				}
			}
			if len(cleaned) == 0 {
				writeError(w, r, billField("owner_user_ids", errs.FieldInvalidFormat, "所属人无效"))
				return nil, false
			}
			where += ` AND a.created_by_user_id IN (` + marks(len(cleaned)) + `)`
			for _, id := range cleaned {
				args = append(args, id)
			}
		}
	}
	if raw := strings.TrimSpace(q.Get("upstream_ids")); raw != "" {
		ids := uniqueSorted(strings.Split(raw, ","))
		cleaned := make([]string, 0, len(ids))
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			cleaned = append(cleaned, id)
		}
		if len(cleaned) == 0 {
			writeError(w, r, billBad("BILL_ACCOUNT_INVALID", "交付编号无效"))
			return nil, false
		}
		where += ` AND b.upstream_id IN (` + marks(len(cleaned)) + `)`
		for _, id := range cleaned {
			args = append(args, id)
		}
	}
	base := ` FROM platform_bill_models b
LEFT JOIN accounts a ON a.id=b.account_id
LEFT JOIN users u ON u.id=a.created_by_user_id
LEFT JOIN platform_remote_accounts r ON r.connection_id=b.connection_id AND r.upstream_id=b.upstream_id` + where
	grouped := `SELECT b.bill_date,b.upstream_id,COALESCE(r.upstream_aid,''),COALESCE(a.account,''),COALESCE(a.created_by_user_id,''),COALESCE(u.nickname,''),COALESCE(u.username,''),SUM(b.cost_fen),MIN(b.final)` + base + `
GROUP BY b.bill_date,b.upstream_id,COALESCE(r.upstream_aid,''),COALESCE(a.account,''),COALESCE(a.created_by_user_id,''),COALESCE(u.nickname,''),COALESCE(u.username,'')`
	return &channelUsageFilter{
		connectionID: connectionID,
		rate:         rate,
		dates:        dates,
		where:        where,
		args:         args,
		base:         base,
		grouped:      grouped,
	}, true
}

type channelUsageRow struct {
	billDate          string
	upstreamID        string
	platformAccountID string
	account           string
	ownerUserID       string
	ownerNickname     string
	ownerUsername     string
	costUsdCents      int64
	costUsd           string
	final             bool
}

func (a *App) scanChannelUsageRows(w http.ResponseWriter, r *http.Request, f *channelUsageFilter, limit, offset int) ([]channelUsageRow, bool) {
	ctx := r.Context()
	query := f.grouped + ` ORDER BY b.bill_date DESC,CAST(b.upstream_id AS INTEGER) DESC,b.upstream_id DESC`
	args := append(append([]any{}, f.args...), limit, offset)
	rows, err := a.DB.QueryContext(ctx, query+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		writeError(w, r, storageError(err, "渠道用量读取失败"))
		return nil, false
	}
	list := []channelUsageRow{}
	for rows.Next() {
		var day, upstream, aid, account, ownerID, nick, username string
		var fen int64
		var final int
		if err = rows.Scan(&day, &upstream, &aid, &account, &ownerID, &nick, &username, &fen, &final); err != nil {
			rows.Close()
			writeError(w, r, storageError(err, "渠道用量读取失败"))
			return nil, false
		}
		cents, text := costUSDFields(fen, f.rate)
		list = append(list, channelUsageRow{
			billDate:          day,
			upstreamID:        upstream,
			platformAccountID: platformAccountID(aid, upstream),
			account:           account,
			ownerUserID:       ownerID,
			ownerNickname:     nick,
			ownerUsername:     username,
			costUsdCents:      cents,
			costUsd:           text,
			final:             final == 1,
		})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		writeError(w, r, storageError(err, "渠道用量读取失败"))
		return nil, false
	}
	rows.Close()
	return list, true
}

func channelUsageOwnerLabel(nick, username string) string {
	if nick != "" {
		return nick
	}
	if username != "" {
		return username
	}
	return "—"
}

func channelUsageAccountLabel(account string) string {
	if account != "" {
		return account
	}
	return "（未关联本地账号）"
}

// channelUsage answers GET /platform-usage/channel-usage: daily totals from
// platform_bill_models (web sync), filtered by upstream delivery ids and
// optionally by owner (admins only).
func (a *App) channelUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, ok := a.parseChannelUsageFilter(w, r)
	if !ok {
		return
	}
	page, size, paged, ok := pageParams(w, r)
	if !ok {
		return
	}
	if !paged {
		page, size = 1, 50
	}
	var total int
	if err := a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+f.grouped+`)`, f.args...).Scan(&total); err != nil {
		writeError(w, r, storageError(err, "渠道用量读取失败"))
		return
	}
	rows, ok := a.scanChannelUsageRows(w, r, f, size, (page-1)*size)
	if !ok {
		return
	}
	list := make([]any, 0, len(rows))
	for _, row := range rows {
		list = append(list, map[string]any{
			"bill_date":           row.billDate,
			"upstream_id":         row.upstreamID,
			"platform_account_id": row.platformAccountID,
			"account":             row.account,
			"owner_user_id":       row.ownerUserID,
			"owner_nickname":      row.ownerNickname,
			"owner_username":      row.ownerUsername,
			"cost_usd_cents":      row.costUsdCents,
			"cost_usd":            row.costUsd,
			"final":               row.final,
		})
	}
	var totalFen int64
	if err := a.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(b.cost_fen),0)`+f.base, f.args...).Scan(&totalFen); err != nil {
		writeError(w, r, storageError(err, "渠道用量读取失败"))
		return
	}
	byDay, ok := a.scanChannelUsageByDay(w, r, f)
	if !ok {
		return
	}
	totalCents, totalText := costUSDFields(totalFen, f.rate)
	write(w, http.StatusOK, map[string]any{
		"list":            list,
		"by_day":          byDay,
		"total_cost_usd":  totalText,
		"total_usd_cents": totalCents,
		"usd_cny_rate":    f.rate,
		"page":            page,
		"page_size":       size,
		"total":           total,
		"from":            f.dates.StartDate,
		"to":              f.dates.EndDate,
		"connection_id":   f.connectionID,
	})
}

// scanChannelUsageByDay sums cost across accounts for each bill_date in the
// filtered window (newest first), using the same ownership/account filters.
func (a *App) scanChannelUsageByDay(w http.ResponseWriter, r *http.Request, f *channelUsageFilter) ([]map[string]any, bool) {
	ctx := r.Context()
	rows, err := a.DB.QueryContext(ctx, `SELECT b.bill_date,COALESCE(SUM(b.cost_fen),0)`+f.base+`
GROUP BY b.bill_date ORDER BY b.bill_date DESC`, f.args...)
	if err != nil {
		writeError(w, r, storageError(err, "渠道用量读取失败"))
		return nil, false
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var day string
		var fen int64
		if err = rows.Scan(&day, &fen); err != nil {
			writeError(w, r, storageError(err, "渠道用量读取失败"))
			return nil, false
		}
		cents, text := costUSDFields(fen, f.rate)
		out = append(out, map[string]any{
			"bill_date":      day,
			"cost_usd_cents": cents,
			"cost_usd":       text,
		})
	}
	if err = rows.Err(); err != nil {
		writeError(w, r, storageError(err, "渠道用量读取失败"))
		return nil, false
	}
	return out, true
}

// channelUsageExport answers GET /platform-usage/channel-usage/export:
// same filters as the list, all matching rows as an .xlsx attachment.
func (a *App) channelUsageExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, ok := a.parseChannelUsageFilter(w, r)
	if !ok {
		return
	}
	var total int
	if err := a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+f.grouped+`)`, f.args...).Scan(&total); err != nil {
		writeError(w, r, storageError(err, "渠道用量读取失败"))
		return
	}
	if total > channelUsageExportMaxRows {
		writeError(w, r, errs.New(errs.CodeQueryInvalid).
			WithMessage("导出行数过多，请缩小日期范围或筛选条件").
			WithHint(fmt.Sprintf("单次最多导出 %d 行，当前 %d 行", channelUsageExportMaxRows, total)))
		return
	}
	rows, ok := a.scanChannelUsageRows(w, r, f, channelUsageExportMaxRows, 0)
	if !ok {
		return
	}
	var totalFen int64
	if err := a.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(b.cost_fen),0)`+f.base, f.args...).Scan(&totalFen); err != nil {
		writeError(w, r, storageError(err, "渠道用量读取失败"))
		return
	}
	sheet := make([][]string, 0, len(rows)+2)
	sheet = append(sheet, []string{"日期", "账号 ID", "账号", "所属人", "消耗额度（美元）"})
	for _, row := range rows {
		sheet = append(sheet, []string{
			row.billDate,
			row.platformAccountID,
			channelUsageAccountLabel(row.account),
			channelUsageOwnerLabel(row.ownerNickname, row.ownerUsername),
			row.costUsd,
		})
	}
	sheet = append(sheet, []string{"合计", "", "", "", usdText(fenToUsdCents(totalFen, f.rate))})

	filename := fmt.Sprintf("channel-usage-%s-%s.xlsx", f.dates.StartDate, f.dates.EndDate)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"; filename*=UTF-8''`+url.PathEscape(filename))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if err := writeXLSX(w, "渠道用量", sheet); err != nil {
		// Headers already sent; best effort log via writeError path is unavailable.
		return
	}
}
