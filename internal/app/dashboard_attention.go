package app

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

// The dashboard's "需要处理" list: linked, unarchived accounts that need a
// decision — close to a window limit, failed on the platform, about to
// expire, dropped by the platform, or out of step with the local record.

const (
	attentionLimit        = 8
	attentionExpiringDays = 30
	nearLimitWarn         = 0.8
	nearLimitDanger       = 0.95
)

type attentionItem struct {
	Kind       string  `json:"kind"`
	Severity   string  `json:"severity"`
	AccountID  string  `json:"account_id"`
	Account    string  `json:"account"`
	Alias      string  `json:"alias"`
	Label      string  `json:"label"`
	Detail     string  `json:"detail"`
	UpstreamID string  `json:"upstream_id"`
	ResetAt    string  `json:"reset_at"`
	ExpiresAt  string  `json:"expires_at"`
	Value      float64 `json:"value"`
}

var attentionKindOrder = map[string]int{"near_limit": 0, "banned": 1, "expiring": 2, "missing": 3, "profile_diff": 4}

// attentionItems builds the list and the per-kind totals (counted before
// the list is capped).
func (a *App) attentionItems(ctx context.Context, at time.Time) ([]attentionItem, map[string]int, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT a.id,a.account,COALESCE(a.plan,''),COALESCE(a.plan_start_time,''),COALESCE(a.plan_end_time,''),COALESCE(a.register_time,''),COALESCE(a.register_location,''),
l.upstream_id,COALESCE(c.timezone,''),
r.upstream_id IS NOT NULL,COALESCE(r.alias,''),COALESCE(r.ext_status,''),COALESCE(r.reason_text,''),COALESCE(r.missing_since,''),COALESCE(r.superseded_by,''),r.five_hour_used,COALESCE(r.five_hour_reset_at,''),
COALESCE(r.plan,''),COALESCE(r.plan_start_time,''),COALESCE(r.plan_end_time,''),COALESCE(r.register_time,''),COALESCE(r.register_location,'')
FROM platform_links l JOIN accounts a ON a.id=l.account_id LEFT JOIN platform_connections c ON c.id=l.connection_id
LEFT JOIN platform_remote_accounts r ON r.connection_id=l.connection_id AND r.upstream_id=l.upstream_id
WHERE l.ingestion_state='accepted' AND a.archived=0 ORDER BY a.account`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	counts := map[string]int{"near_limit": 0, "banned": 0, "expiring": 0, "missing": 0, "profile_diff": 0}
	var items []attentionItem
	for rows.Next() {
		var id, email, plan, planStart, planEnd, regTime, regLoc, upstream, zone, alias, status, reason, missing, superseded, resetAt string
		var rPlan, rStart, rEnd, rRegTime, rRegLoc string
		var hasRemote bool
		var fiveHour sql.NullFloat64
		if err = rows.Scan(&id, &email, &plan, &planStart, &planEnd, &regTime, &regLoc, &upstream, &zone, &hasRemote, &alias, &status, &reason, &missing, &superseded, &fiveHour, &resetAt, &rPlan, &rStart, &rEnd, &rRegTime, &rRegLoc); err != nil {
			return nil, nil, err
		}
		if superseded != "" {
			continue
		}
		base := attentionItem{AccountID: id, Account: email, Alias: alias, UpstreamID: upstream}
		failing := webFailureStatuses[status]
		if fiveHour.Valid && fiveHour.Float64 >= nearLimitWarn && missing == "" && !failing {
			it := base
			it.Kind, it.Value, it.ResetAt = "near_limit", math.Round(fiveHour.Float64*10000)/100, resetAt
			it.Severity = "warn"
			if fiveHour.Float64 >= nearLimitDanger {
				it.Severity = "danger"
			}
			it.Label = fmt.Sprintf("5 小时窗口 %.0f%%", it.Value)
			if t, e := time.Parse(time.RFC3339, resetAt); e == nil {
				it.Detail = "重置 " + t.In(connectionLocation(zone)).Format("15:04")
			}
			items = append(items, it)
			counts["near_limit"]++
		}
		if failing {
			it := base
			it.Kind, it.Severity, it.Label, it.Detail = "banned", "danger", status, reason
			if len([]rune(it.Detail)) > 80 {
				it.Detail = string([]rune(it.Detail)[:80]) + "…"
			}
			items = append(items, it)
			counts["banned"]++
		}
		end := rEnd
		if end == "" {
			end = planEnd
		}
		if end != "" && missing == "" && !failing {
			if endDay, e := time.Parse("2006-01-02", end); e == nil {
				today, _ := time.Parse("2006-01-02", at.In(connectionLocation(zone)).Format("2006-01-02"))
				days := int(math.Round(endDay.Sub(today).Hours() / 24))
				if days <= attentionExpiringDays {
					it := base
					it.Kind, it.Value, it.ExpiresAt = "expiring", float64(days), end
					it.Severity = "warn"
					if days <= 7 {
						it.Severity = "danger"
					}
					if days < 0 {
						it.Label = fmt.Sprintf("已到期 %d 天", -days)
					} else {
						it.Label = fmt.Sprintf("套餐 %s 到期（%d 天）", endDay.Format("01-02"), days)
					}
					items = append(items, it)
					counts["expiring"]++
				}
			}
		}
		if missing != "" {
			it := base
			it.Kind, it.Severity, it.Label, it.Detail = "missing", "warn", "平台已移除", missing
			items = append(items, it)
			counts["missing"]++
		}
		if hasRemote && missing == "" {
			var diff []string
			for i, pair := range [][2]string{{rPlan, plan}, {rStart, planStart}, {rEnd, planEnd}, {rRegTime, regTime}, {rRegLoc, regLoc}} {
				if pair[0] != "" && pair[1] != "" && pair[0] != pair[1] {
					diff = append(diff, remoteProfileFields[i].key)
				}
			}
			if len(diff) > 0 {
				it := base
				it.Kind, it.Severity, it.Label, it.Detail = "profile_diff", "info", "资料与平台不一致", strings.Join(diff, ",")
				items = append(items, it)
				counts["profile_diff"]++
			}
		}
	}
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if attentionKindOrder[a.Kind] != attentionKindOrder[b.Kind] {
			return attentionKindOrder[a.Kind] < attentionKindOrder[b.Kind]
		}
		switch a.Kind {
		case "near_limit":
			return a.Value > b.Value
		case "expiring":
			return a.Value < b.Value
		}
		return a.Account < b.Account
	})
	if len(items) > attentionLimit {
		items = items[:attentionLimit]
	}
	if items == nil {
		items = []attentionItem{}
	}
	return items, counts, nil
}

// dashboardAttention answers GET /api/v1/dashboard/attention.
func (a *App) dashboardAttention(w http.ResponseWriter, r *http.Request) {
	items, counts, err := a.attentionItems(r.Context(), time.Now())
	if err != nil {
		writeError(w, r, storageError(err, "读取待处理账号失败"))
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items, "counts": counts})
}
