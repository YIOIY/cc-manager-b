package app

import (
	"cc-manager/internal/errs"
	"context"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"cc-manager/internal/supplier"
)

type billSummaryRequest struct {
	ConnectionID string  `json:"connection_id"`
	AccountIDs   []int64 `json:"account_ids"`
	StartDate    string  `json:"start_ds"`
	EndDate      string  `json:"end_ds"`
}

var billSummaryCountFields = []string{"request_count", "prompt_tokens", "completion_tokens", "cache_read_input_tokens", "cache_write_input_tokens"}

func (a *App) queryBillSummary(w http.ResponseWriter, r *http.Request) {
	var in billSummaryRequest
	if !decodeBillRequest(w, r, &in) {
		return
	}
	result, err := a.readBillSummary(r.Context(), in, ownershipFrom(r))
	if err != nil {
		billFail(w, err)
		return
	}
	write(w, http.StatusOK, result)
}

func (a *App) readBillSummary(ctx context.Context, in billSummaryRequest, scope ownershipScope) (map[string]any, error) {
	if in.ConnectionID == "" {
		return nil, billField("connection_id", errs.FieldRequired, "请选择平台连接")
	}
	if len(in.AccountIDs) == 0 || len(in.AccountIDs) > 100 {
		return nil, billBad("BILL_ACCOUNT_LIMIT", "请选择 1 至 100 个已上号账号")
	}
	ids := make([]string, 0, len(in.AccountIDs))
	seen := map[int64]bool{}
	for _, id := range in.AccountIDs {
		if id <= 0 || seen[id] {
			return nil, billBad("BILL_ACCOUNT_INVALID", "交付 ID 必须为正整数且不能重复")
		}
		seen[id] = true
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	var base, secret, zone string
	var enabled int
	rate := defaultUSDCNYRate
	if a.DB.QueryRowContext(ctx, `SELECT base_url,token_secret,timezone,enabled,COALESCE(usd_cny_rate,?) FROM platform_connections WHERE id=?`, defaultUSDCNYRate, in.ConnectionID).Scan(&base, &secret, &zone, &enabled, &rate) != nil || enabled != 1 {
		return nil, billBad("CONNECTION_DISABLED", "平台连接不存在或未启用")
	}
	loc, err := loadBillLocation(zone)
	if err != nil {
		return nil, err
	}
	dates, err := supplier.ValidateBillDates(in.StartDate, in.EndDate, nil, time.Time{}, false)
	if err != nil {
		return nil, billBad("BILL_RANGE_INVALID", "请填写有效的起止日期，结束日期不得早于开始日期")
	}
	// The interval contract limits the start date; it does not specify a
	// maximum end date. Daily synchronization keeps its separate policy.
	if dates.StartDate < time.Now().In(loc).AddDate(0, 0, -90).Format("2006-01-02") {
		return nil, billBad("BILL_RANGE_EXPIRED", "开始日期不得早于平台业务日期的 90 天前")
	}
	args := []any{in.ConnectionID}
	for _, id := range ids {
		args = append(args, id)
	}
	ownerSQL, ownerArgs := scope.sqlAnd("a.created_by_user_id")
	args = append(args, ownerArgs...)
	rows, err := a.DB.QueryContext(ctx, `SELECT a.id,a.account,COALESCE(l.alias,''),l.upstream_id FROM platform_links l JOIN accounts a ON a.id=l.account_id WHERE l.connection_id=? AND l.ingestion_state='accepted' AND l.upstream_id IN (`+marks(len(ids))+`)`+ownerSQL, args...)
	if err != nil {
		return nil, err
	}
	accounts := map[string]map[string]any{}
	for rows.Next() {
		var local, email, alias, upstream string
		if err = rows.Scan(&local, &email, &alias, &upstream); err != nil {
			break
		}
		if accounts[upstream] != nil {
			err = billBad("BILL_ACCOUNT_AMBIGUOUS", "同一交付 ID 存在多个本地关联，请先核实账号关联")
			break
		}
		accounts[upstream] = map[string]any{"account_id": local, "account": email, "alias": alias, "supplier_account_id": upstream, "state": "no_data"}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(accounts) != len(ids) {
		return nil, billBad("BILL_ACCOUNT_UNLINKED", "部分交付 ID 未关联到所选平台，请选择该平台已上号的账号")
	}
	token, err := a.openSecret(secret, in.ConnectionID)
	if err != nil {
		return nil, secretError("connection_token", "平台令牌无法读取，请检查平台设置", err)
	}
	client, err := supplier.New(base, string(token))
	if err != nil {
		return nil, billBad("CONNECTION_INVALID", "平台连接配置无效，请检查地址和 Token")
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	response, err := client.QueryBillSummary(ctx, ids, dates.StartDate, dates.EndDate)
	if err != nil {
		return nil, summaryUpstreamError(err)
	}
	sums := make([]big.Int, len(billSummaryCountFields))
	var totalUsdCents int64
	for _, row := range accounts {
		for _, field := range billSummaryCountFields {
			row[field] = nil
		}
		row["cost_usd"] = nil
		row["cost_usd_cents"] = nil
	}
	for _, bill := range response.Bills {
		row := accounts[strconv.FormatInt(bill.ID, 10)]
		row["state"] = "returned"
		values := []int64{bill.RequestCount, bill.PromptTokens, bill.CompletionTokens, bill.CacheReadInputTokens, bill.CacheWriteInputTokens}
		for i, value := range values {
			row[billSummaryCountFields[i]] = strconv.FormatInt(value, 10)
			sums[i].Add(&sums[i], big.NewInt(value))
		}
		cents, text := costUSDFields(bill.CostFen, rate)
		row["cost_usd_cents"] = strconv.FormatInt(cents, 10)
		row["cost_usd"] = text
		totalUsdCents += cents
	}
	for id, code := range response.RowErrors {
		accounts[id]["state"] = "invalid"
		accounts[id]["error_code"] = code
	}
	items := make([]any, 0, len(ids))
	for _, id := range ids {
		items = append(items, accounts[id])
	}
	returned, invalid := len(response.Bills), len(response.RowErrors)
	missing := len(ids) - returned - invalid
	// Preserve exact counters beyond JavaScript's safe integer range.
	totals := map[string]any{"is_partial": returned != len(ids), "currency": "USD"}
	for i, field := range billSummaryCountFields {
		totals[field] = nil
		if returned > 0 {
			totals[field] = sums[i].String()
		}
	}
	totals["cost_usd"] = nil
	totals["cost_usd_cents"] = nil
	if returned > 0 {
		totals["cost_usd"] = usdText(totalUsdCents)
		totals["cost_usd_cents"] = strconv.FormatInt(totalUsdCents, 10)
	}
	return map[string]any{"connection_id": in.ConnectionID, "timezone": zone, "usd_cny_rate": rate, "requested_range": dates, "range": response.ActualRange, "range_adjusted": dates != response.ActualRange, "queried_at": now(), "account_count": len(ids), "returned_count": returned, "no_data_count": missing, "invalid_count": invalid, "complete": returned == len(ids), "items": items, "totals": totals}, nil
}

func summaryUpstreamError(err error) error {
	var remote *supplier.BillHTTPError
	if errors.As(err, &remote) {
		switch remote.Status {
		case 401, 403:
			return errs.New(errs.CodeBillAccessDenied).WithDetail("upstream_status", remote.Status)
		case 404:
			return errs.New(errs.CodeBillAccountNotFound).WithDetail("upstream_status", remote.Status)
		case 429:
			return errs.New(errs.CodeBillRateLimited).WithDetail("upstream_status", remote.Status)
		default:
			return errs.New(errs.CodeBillUpstreamError).WithDetail("upstream_status", remote.Status)
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errs.New(errs.CodeBillQueryTimeout)
	}
	return errs.New(errs.CodeBillResponseError)
}
