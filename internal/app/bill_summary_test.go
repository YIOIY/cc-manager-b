package app

import (
	"cc-manager/internal/errs"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func summaryFixture(t *testing.T, n int, handler http.HandlerFunc) (*App, billSummaryRequest) {
	t.Helper()
	a, original := billFixture(t, n)
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	pin := fmt.Sprintf("%x", sha256.Sum256(server.Certificate().Raw))
	pins, _ := json.Marshal(map[string]string{strings.TrimPrefix(server.URL, "https://"): pin})
	t.Setenv("CC_MANAGER_SUPPLIER_TLS_PINS", string(pins))
	t.Setenv("CC_MANAGER_SUPPLIER_INSECURE_TLS", "")
	if _, err := a.DB.Exec(`UPDATE platform_connections SET base_url=? WHERE id='conn'`, server.URL); err != nil {
		t.Fatal(err)
	}
	from, _ := time.Parse("2006-01-02", original.From)
	in := billSummaryRequest{ConnectionID: "conn", StartDate: from.AddDate(0, 0, -6).Format("2006-01-02"), EndDate: original.To}
	for i := 1; i <= n && i <= 100; i++ {
		in.AccountIDs = append(in.AccountIDs, int64(i))
	}
	return a, in
}

func TestBillSummaryForwardsOneWholeRangeWithDeliveryIDsAndPreservesMissing(t *testing.T) {
	calls := 0
	var sent billSummaryRequest
	a, in := summaryFixture(t, 4, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/supplier-account/api/openapi/bill/query" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer fixture-openapi-token" {
			t.Error("wrong upstream endpoint, method or credential")
		}
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Error(err)
		}
		fmt.Fprintf(w, `{"list":[{"id":1,"request_count":1234,"prompt_tokens":100000,"completion_tokens":50000,"cache_read_input_tokens":3000,"cache_write_input_tokens":1000,"cost":12.34},{"id":2,"request_count":0,"prompt_tokens":0,"completion_tokens":0,"cache_read_input_tokens":0,"cache_write_input_tokens":0,"cost":0},{"id":4,"cost":null}],"range":{"start_ds":%q,"end_ds":%q}}`, sent.StartDate, sent.EndDate)
	})
	a.DB.Exec(`UPDATE platform_links SET upstream_aid='99999'`)
	result, err := a.readBillSummary(context.Background(), in, ownershipScope{Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || sent.StartDate != in.StartDate || sent.EndDate != in.EndDate || fmt.Sprint(sent.AccountIDs) != "[1 2 3 4]" {
		t.Fatalf("range was split or wrong IDs used: calls=%d sent=%+v", calls, sent)
	}
	items := result["items"].([]any)
	if result["returned_count"] != 2 || result["no_data_count"] != 1 || result["invalid_count"] != 1 || result["complete"] != false {
		t.Fatalf("counts=%+v", result)
	}
	if result["totals"].(map[string]any)["cost_fen"] != "1234" || result["totals"].(map[string]any)["request_count"] != "1234" {
		t.Fatal(result["totals"])
	}
	if items[1].(map[string]any)["cost_fen"] != "0" || items[2].(map[string]any)["cost_fen"] != nil || items[2].(map[string]any)["state"] != "no_data" || items[3].(map[string]any)["state"] != "invalid" {
		t.Fatal(items)
	}
	for _, table := range []string{"platform_bill_daily", "sync_runs"} {
		var count int
		a.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count)
		if count != 0 {
			t.Fatal("interval result incorrectly persisted as daily data")
		}
	}
}

func TestBillSummaryValidatesContractBeforeCallingPlatform(t *testing.T) {
	calls := 0
	a, valid := summaryFixture(t, 100, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var sent billSummaryRequest
		json.NewDecoder(r.Body).Decode(&sent)
		fmt.Fprintf(w, `{"list":[],"range":{"start_ds":%q,"end_ds":%q}}`, sent.StartDate, sent.EndDate)
	})
	today, _ := time.Parse("2006-01-02", valid.EndDate)
	for _, tc := range []struct {
		name string
		edit func(*billSummaryRequest)
	}{
		{"missing connection", func(v *billSummaryRequest) { v.ConnectionID = "" }},
		{"missing accounts", func(v *billSummaryRequest) { v.AccountIDs = nil }},
		{"101 accounts", func(v *billSummaryRequest) { v.AccountIDs = append(append([]int64{}, v.AccountIDs...), 101) }},
		{"zero ID", func(v *billSummaryRequest) { v.AccountIDs = []int64{0} }},
		{"duplicate IDs", func(v *billSummaryRequest) { v.AccountIDs = []int64{1, 1} }},
		{"unlinked ID", func(v *billSummaryRequest) { v.AccountIDs = []int64{99999} }},
		{"missing date", func(v *billSummaryRequest) { v.StartDate = "" }},
		{"bad date", func(v *billSummaryRequest) { v.StartDate = "2026-02-30" }},
		{"reversed range", func(v *billSummaryRequest) { v.StartDate = today.AddDate(0, 0, 1).Format("2006-01-02") }},
		{"older than 90 days", func(v *billSummaryRequest) { v.StartDate = today.AddDate(0, 0, -91).Format("2006-01-02") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := valid
			tc.edit(&input)
			if _, err := a.readBillSummary(context.Background(), input, ownershipScope{Admin: true}); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
	if calls != 0 {
		t.Fatal("invalid request reached supplier")
	}
	valid.StartDate = today.AddDate(0, 0, -90).Format("20060102")
	valid.EndDate = today.AddDate(0, 0, 1).Format("20060102")
	result, err := a.readBillSummary(context.Background(), valid, ownershipScope{Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || result["no_data_count"] != 100 || result["totals"].(map[string]any)["cost_fen"] != nil {
		t.Fatal("missing rows were converted to zeros", result)
	}
	if _, err = a.DB.Exec(`UPDATE platform_connections SET enabled=0`); err != nil {
		t.Fatal(err)
	}
	if _, err = a.readBillSummary(context.Background(), valid, ownershipScope{Admin: true}); err == nil || calls != 1 {
		t.Fatal("disabled connection queried")
	}
}

func TestBillSummaryShowsEffectiveRangeAndSumsWithoutOverflow(t *testing.T) {
	a, in := summaryFixture(t, 2, func(w http.ResponseWriter, r *http.Request) {
		var sent billSummaryRequest
		json.NewDecoder(r.Body).Decode(&sent)
		fmt.Fprintf(w, `{"list":[{"id":1,"request_count":9223372036854775807,"prompt_tokens":0,"completion_tokens":0,"cache_read_input_tokens":0,"cache_write_input_tokens":0,"cost":0.01},{"id":2,"request_count":9223372036854775807,"prompt_tokens":0,"completion_tokens":0,"cache_read_input_tokens":0,"cache_write_input_tokens":0,"cost":0.02}],"range":{"start_ds":%q,"end_ds":%q}}`, strings.ReplaceAll(sent.EndDate, "-", ""), sent.EndDate)
	})
	result, err := a.readBillSummary(context.Background(), in, ownershipScope{Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	if result["range_adjusted"] != true || result["complete"] != true || result["totals"].(map[string]any)["request_count"] != "18446744073709551614" || result["totals"].(map[string]any)["cost_fen"] != "3" {
		t.Fatal(result)
	}
}

func TestBillSummaryErrorsDoNotBecomeZeroBills(t *testing.T) {
	status := 404
	a, in := summaryFixture(t, 1, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
	for _, tc := range []struct {
		status int
		code   string
	}{{404, "BILL_ACCOUNT_NOT_FOUND"}, {401, "BILL_ACCESS_DENIED"}, {403, "BILL_ACCESS_DENIED"}, {429, "BILL_RATE_LIMITED"}, {500, "BILL_UPSTREAM_ERROR"}, {200, "BILL_RESPONSE_ERROR"}} {
		status = tc.status
		value, err := a.readBillSummary(context.Background(), in, ownershipScope{Admin: true})
		var problem *errs.Error
		if value != nil || !errors.As(err, &problem) || problem.Code() != tc.code {
			t.Fatalf("status=%d value=%v error=%v", status, value, err)
		}
	}
}

func TestBillSummaryRouteUsesPostAuthAndNumericIDs(t *testing.T) {
	a, _ := billFixture(t, 1)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/platform-usage/bills/summary", strings.NewReader(`{}`)))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	a.billRoutes(w, httptest.NewRequest("GET", "/bills/summary", nil), "/bills/summary")
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	a.queryBillSummary(w, httptest.NewRequest("POST", "/bills/summary", strings.NewReader(`{"connection_id":"conn","account_ids":["1"],"start_ds":"20261106","end_ds":"20261106"}`)))
	if w.Code != 400 {
		t.Fatal("non-numeric IDs accepted", w.Body.String())
	}
}
