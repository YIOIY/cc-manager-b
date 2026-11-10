package supplier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type BillDateRange struct {
	StartDate string `json:"start_ds"`
	EndDate   string `json:"end_ds"`
}
type BillQueryResult struct {
	Bills          []Bill
	RequestedRange BillDateRange
	ActualRange    BillDateRange
	RowErrors      map[string]string
}
type BillHTTPError struct {
	Status     int
	RetryAfter time.Duration
}

func (e *BillHTTPError) Error() string { return fmt.Sprintf("supplier bill HTTP %d", e.Status) }

func (e *BillHTTPError) UpstreamCode() string {
	switch {
	case e.Status == 401 || e.Status == 403:
		return "PLATFORM_AUTH_FAILED"
	case e.Status >= 500 || e.Status == 429:
		return "PLATFORM_UNAVAILABLE"
	default:
		return "PLATFORM_RESPONSE_INVALID"
	}
}
func (e *BillHTTPError) UpstreamStatus() int     { return e.Status }
func (e *BillHTTPError) UpstreamSummary() string { return "" }

func NormalizeBillDate(value string) (string, error) {
	for _, layout := range []string{"2006-01-02", "20060102"} {
		if d, err := time.Parse(layout, value); err == nil {
			return d.Format("2006-01-02"), nil
		}
	}
	return "", errors.New("invalid bill date")
}

// ValidateBillDates compares calendar dates in the connection timezone.
func ValidateBillDates(start, end string, loc *time.Location, current time.Time, remote bool) (BillDateRange, error) {
	from, err := NormalizeBillDate(start)
	if err != nil {
		return BillDateRange{}, err
	}
	to, err := NormalizeBillDate(end)
	if err != nil || from > to {
		return BillDateRange{}, errors.New("invalid bill date range")
	}
	if remote {
		if loc == nil {
			return BillDateRange{}, errors.New("connection timezone required")
		}
		today := current.In(loc)
		if from < today.AddDate(0, 0, -90).Format("2006-01-02") || to > today.Format("2006-01-02") {
			return BillDateRange{}, errors.New("bill dates outside supplier query window")
		}
	}
	return BillDateRange{from, to}, nil
}

func rangeDate(raw json.RawMessage) (string, error) {
	return NormalizeBillDate(strings.Trim(string(raw), `"`))
}

// QueryBillResult preserves the actual range and distinguishes missing rows
// from explicit zero values. Business date limits belong to the caller's timezone.
func (c *Client) QueryBillResult(ctx context.Context, ids []string, start, end string) (BillQueryResult, error) {
	return c.queryBillResult(ctx, ids, start, end, true)
}

// QueryBillSummary returns the platform's effective range for interval totals.
// Unlike daily persistence, interval display can represent an adjusted range.
func (c *Client) QueryBillSummary(ctx context.Context, ids []string, start, end string) (BillQueryResult, error) {
	return c.queryBillResult(ctx, ids, start, end, false)
}

func (c *Client) queryBillResult(ctx context.Context, ids []string, start, end string, exactRange bool) (BillQueryResult, error) {
	result := BillQueryResult{Bills: []Bill{}, RowErrors: map[string]string{}}
	accountIDs, err := validateIDs(ids)
	if err != nil {
		return result, err
	}
	rangeQuery, err := ValidateBillDates(start, end, nil, time.Time{}, false)
	if err != nil {
		return result, err
	}
	result.RequestedRange = rangeQuery
	data, status, headers, err := c.requestDetails(ctx, http.MethodPost, "/supplier-account/api/openapi/bill/query", map[string]any{"account_ids": accountIDs, "start_ds": rangeQuery.StartDate, "end_ds": rangeQuery.EndDate})
	if err != nil {
		return result, err
	}
	if status < 200 || status >= 300 {
		var delay time.Duration
		if seconds, e := strconv.Atoi(headers.Get("Retry-After")); e == nil && seconds > 0 {
			delay = time.Duration(seconds) * time.Second
		} else if deadline, e := http.ParseTime(headers.Get("Retry-After")); e == nil {
			delay = time.Until(deadline)
		}
		return result, &BillHTTPError{status, delay}
	}
	var response struct {
		List  json.RawMessage `json:"list"`
		Range struct {
			Start json.RawMessage `json:"start_ds"`
			End   json.RawMessage `json:"end_ds"`
		} `json:"range"`
	}
	if json.Unmarshal(data, &response) != nil {
		return result, errors.New("BILL_RESPONSE_INVALID")
	}
	from, e1 := rangeDate(response.Range.Start)
	to, e2 := rangeDate(response.Range.End)
	if e1 != nil || e2 != nil || from > to {
		return result, errors.New("BILL_RANGE_INVALID")
	}
	result.ActualRange = BillDateRange{from, to}
	if exactRange && result.ActualRange != rangeQuery {
		return result, errors.New("BILL_RANGE_MISMATCH")
	}
	if len(response.List) == 0 || string(response.List) == "null" {
		return result, errors.New("BILL_RESPONSE_INVALID")
	}
	var rows []json.RawMessage
	if json.Unmarshal(response.List, &rows) != nil {
		return result, errors.New("BILL_RESPONSE_INVALID")
	}
	wanted := map[int64]bool{}
	seen := map[int64]bool{}
	for _, id := range accountIDs {
		wanted[id] = true
	}
	for _, raw := range rows {
		var identity struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(raw, &identity) != nil || !wanted[identity.ID] || seen[identity.ID] {
			return result, errors.New("BILL_ID_MISMATCH")
		}
		seen[identity.ID] = true
		var v struct {
			RequestCount          *int64       `json:"request_count"`
			PromptTokens          *int64       `json:"prompt_tokens"`
			CompletionTokens      *int64       `json:"completion_tokens"`
			CacheReadInputTokens  *int64       `json:"cache_read_input_tokens"`
			CacheWriteInputTokens *int64       `json:"cache_write_input_tokens"`
			Cost                  *json.Number `json:"cost"`
		}
		id := strconv.FormatInt(identity.ID, 10)
		if json.Unmarshal(raw, &v) != nil {
			result.RowErrors[id] = "BILL_ROW_INVALID"
			continue
		}
		valid := v.Cost != nil
		for _, n := range []*int64{v.RequestCount, v.PromptTokens, v.CompletionTokens, v.CacheReadInputTokens, v.CacheWriteInputTokens} {
			if n == nil || *n < 0 {
				valid = false
			}
		}
		if !valid {
			result.RowErrors[id] = "BILL_ROW_INVALID"
			continue
		}
		cost, e := decimalFen(v.Cost.String())
		if e != nil || cost < 0 {
			result.RowErrors[id] = "BILL_COST_INVALID"
			continue
		}
		result.Bills = append(result.Bills, Bill{ID: identity.ID, RequestCount: *v.RequestCount, PromptTokens: *v.PromptTokens, CompletionTokens: *v.CompletionTokens, CacheReadInputTokens: *v.CacheReadInputTokens, CacheWriteInputTokens: *v.CacheWriteInputTokens, CostFen: cost})
	}
	return result, nil
}

// QueryBills is retained for existing integrations; new sync code consumes the
// full result. Invalid rows cannot be mistaken for a successful empty response.
func (c *Client) QueryBills(ctx context.Context, ids []string, start, end string) ([]Bill, error) {
	r, err := c.QueryBillResult(ctx, ids, start, end)
	if err == nil && len(r.RowErrors) > 0 {
		err = errors.New("BILL_ROW_INVALID")
	}
	return r.Bills, err
}
