package app

import (
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"cc-manager/internal/errs"
	"cc-manager/internal/supplier"
)

// platformAccountID prefers the console aid (same rule as /accounts cards),
// falling back to the delivery upstream_id.
func platformAccountID(aid, upstream string) string {
	if a := strings.TrimSpace(aid); a != "" && a != "0" {
		return a
	}
	return strings.TrimSpace(upstream)
}

// fenToUsdCents converts RMB fen to US cents at the platform's fixed rate
// (RMB per dollar), matching web/src/model/money.ts.
func fenToUsdCents(fen int64, rate float64) int64 {
	if rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return 0
	}
	return int64(math.Round(float64(fen) / rate))
}

func usdText(cents int64) string {
	if cents < 0 {
		return "-" + yuanText(-cents)
	}
	return yuanText(cents)
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

// channelUsage answers GET /platform-usage/channel-usage: daily totals from
// platform_bill_models (web sync), filtered by upstream delivery ids and
// optionally by owner (admins only).
func (a *App) channelUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	connectionID := strings.TrimSpace(q.Get("connection_id"))
	if connectionID == "" {
		writeError(w, r, billField("connection_id", errs.FieldRequired, "请选择平台连接"))
		return
	}
	var rate float64
	if err := a.DB.QueryRowContext(ctx, `SELECT COALESCE(usd_cny_rate,?) FROM platform_connections WHERE id=?`, defaultUSDCNYRate, connectionID).Scan(&rate); err != nil {
		writeError(w, r, errs.New(errs.CodeNotFound).WithMessage("平台连接不存在").WithDetail("resource", "platform_connection"))
		return
	}
	if rate <= 0 {
		rate = defaultUSDCNYRate
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
		page, size = 1, 50
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
				return
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
			return
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
	var total int
	if err = a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+grouped+`)`, args...).Scan(&total); err != nil {
		writeError(w, r, storageError(err, "渠道用量读取失败"))
		return
	}
	rows, err := a.DB.QueryContext(ctx, grouped+` ORDER BY b.bill_date DESC,CAST(b.upstream_id AS INTEGER) DESC,b.upstream_id DESC LIMIT ? OFFSET ?`, append(append([]any{}, args...), size, (page-1)*size)...)
	if err != nil {
		writeError(w, r, storageError(err, "渠道用量读取失败"))
		return
	}
	list := []any{}
	for rows.Next() {
		var day, upstream, aid, account, ownerID, nick, username string
		var fen int64
		var final int
		if err = rows.Scan(&day, &upstream, &aid, &account, &ownerID, &nick, &username, &fen, &final); err != nil {
			rows.Close()
			writeError(w, r, storageError(err, "渠道用量读取失败"))
			return
		}
		cents := fenToUsdCents(fen, rate)
		list = append(list, map[string]any{
			"bill_date":           day,
			"upstream_id":         upstream,
			"platform_account_id": platformAccountID(aid, upstream),
			"account":             account,
			"owner_user_id":       ownerID,
			"owner_nickname":      nick,
			"owner_username":      username,
			"cost_fen":            fen,
			"cost_usd_cents":      cents,
			"cost_usd":            usdText(cents),
			"final":               final == 1,
		})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		writeError(w, r, storageError(err, "渠道用量读取失败"))
		return
	}
	rows.Close()
	var totalFen int64
	if err = a.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(b.cost_fen),0)`+base, args...).Scan(&totalFen); err != nil {
		writeError(w, r, storageError(err, "渠道用量读取失败"))
		return
	}
	totalCents := fenToUsdCents(totalFen, rate)
	write(w, http.StatusOK, map[string]any{
		"list":            list,
		"total_cost_fen":  totalFen,
		"total_cost_usd":  usdText(totalCents),
		"total_usd_cents": totalCents,
		"usd_cny_rate":    rate,
		"page":            page,
		"page_size":       size,
		"total":           total,
		"from":            dates.StartDate,
		"to":              dates.EndDate,
		"connection_id":   connectionID,
	})
}
