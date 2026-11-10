package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"cc-manager/internal/supplier"
)

type fakeBillCall func(context.Context, []string, string, string) (supplier.BillQueryResult, error)

func (f fakeBillCall) QueryBillResult(ctx context.Context, ids []string, from, to string) (supplier.BillQueryResult, error) {
	return f(ctx, ids, from, to)
}
func billFixture(t *testing.T, n int) (*App, billSyncRequest) {
	t.Helper()
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "local-dev-password")
	t.Setenv("CC_MANAGER_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	a, err := newApp(t.TempDir()+"/bills.db", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.DB.Close() })
	a.billInterval = 0
	secret, err := a.keys.Seal([]byte("fixture-openapi-token"), []byte("conn"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.DB.Exec(`INSERT INTO platform_connections(id,name,base_url,token_secret,timezone,enabled,version) VALUES('conn','Fixture','https://supplier.example',?,'Asia/Shanghai',1,1)`, secret)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("account-%04d", i)
		_, err = tx.Exec(`INSERT INTO accounts(id,account,archived) VALUES(?,?,0)`, id, id+"@example.test")
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(`INSERT INTO platform_links(id,account_id,connection_id,upstream_id,ingestion_state,created_at,updated_at) VALUES(?,?,'conn',?,'accepted',?,?)`, id, id, strconv.Itoa(i), now(), now())
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	day := time.Now().In(loc).Format("2006-01-02")
	return a, billSyncRequest{ConnectionID: "conn", Scope: "all_linked", From: day, To: day}
}
func goodBillResult(ids []string, from, to string, cost int64) supplier.BillQueryResult {
	r := supplier.BillQueryResult{Bills: []supplier.Bill{}, RequestedRange: supplier.BillDateRange{StartDate: from, EndDate: to}, ActualRange: supplier.BillDateRange{StartDate: from, EndDate: to}, RowErrors: map[string]string{}}
	for _, id := range ids {
		n, _ := strconv.ParseInt(id, 10, 64)
		r.Bills = append(r.Bills, supplier.Bill{ID: n, RequestCount: 1, PromptTokens: 4200000000, CompletionTokens: 3, CacheReadInputTokens: 4, CacheWriteInputTokens: 5, CostFen: cost})
	}
	return r
}
func runFixture(t *testing.T, a *App, input billSyncRequest, key string) string {
	t.Helper()
	result, err := a.createBillRun(context.Background(), input, key, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.runBillJob(context.Background()); err != nil {
		t.Fatal(err)
	}
	return result["run_id"].(string)
}

func TestBillBatchingRevisionAndPersistence(t *testing.T) {
	a, input := billFixture(t, 250)
	calls := 0
	cost := int64(1234)
	a.billFactory = func(string, string) (billSupplier, error) {
		return fakeBillCall(func(_ context.Context, ids []string, from, to string) (supplier.BillQueryResult, error) {
			calls++
			if len(ids) > 100 {
				t.Errorf("batch %d", len(ids))
			}
			return goodBillResult(ids, from, to, cost), nil
		}), nil
	}
	day, _ := time.Parse("2006-01-02", input.To)
	input.From = day.AddDate(0, 0, -2).Format("2006-01-02")
	id := runFixture(t, a, input, "batch-1")
	detail, err := a.billRunDetail(id)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 9 || detail["state"] != "succeeded" || detail["completed"] != 750 {
		t.Fatalf("calls=%d detail=%v", calls, detail)
	}
	reused, err := a.createBillRun(context.Background(), input, "batch-1", "")
	if err != nil || reused["run_id"] != id {
		t.Fatalf("idempotency=%v %v", reused, err)
	}
	runFixture(t, a, input, "batch-2")
	var revision, rows, verified int
	var sum int64
	if err = a.DB.QueryRow(`SELECT COUNT(*),MIN(revision),SUM(response_verified),SUM(cost_fen) FROM platform_bill_daily`).Scan(&rows, &revision, &verified, &sum); err != nil {
		t.Fatal(err)
	}
	if rows != 750 || revision != 1 || verified != 750 || sum != 750*1234 {
		t.Fatalf("rows=%d revision=%d verified=%d sum=%d", rows, revision, verified, sum)
	}
	cost = 2000
	runFixture(t, a, input, "batch-3")
	if err = a.DB.QueryRow(`SELECT MIN(revision) FROM platform_bill_daily`).Scan(&revision); err != nil || revision != 2 {
		t.Fatalf("revision=%d error=%v", revision, err)
	}
}

func TestBillPartialNoDataRetryAndLeaseRecovery(t *testing.T) {
	a, input := billFixture(t, 3)
	a.billFactory = func(string, string) (billSupplier, error) {
		return fakeBillCall(func(_ context.Context, ids []string, from, to string) (supplier.BillQueryResult, error) {
			r := goodBillResult([]string{"1"}, from, to, 100)
			r.RowErrors["2"] = "BILL_ROW_INVALID"
			return r, nil
		}), nil
	}
	queued, err := a.createBillRun(context.Background(), input, "recover", "")
	if err != nil {
		t.Fatal(err)
	}
	id := queued["run_id"].(string)
	if _, err = a.DB.Exec(`UPDATE sync_runs SET state='running',owner='crashed-worker',lease_until=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec(`UPDATE bill_sync_items SET state='running' WHERE run_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err = a.runBillJob(context.Background()); err != nil {
		t.Fatal(err)
	}
	detail, err := a.billRunDetail(id)
	if err != nil {
		t.Fatal(err)
	}
	counts := detail["counts"].(map[string]int)
	if detail["state"] != "partial" || counts["updated"] != 1 || counts["failed"] != 1 || counts["no_data"] != 1 {
		t.Fatalf("detail=%v", detail)
	}
	var rows int
	a.DB.QueryRow(`SELECT COUNT(*) FROM platform_bill_daily`).Scan(&rows)
	if rows != 1 {
		t.Fatalf("invented rows=%d", rows)
	}
	response := httptest.NewRecorder()
	a.retryBillRun(response, httptest.NewRequest("POST", "/retry-failed", strings.NewReader("{}")), id)
	if response.Code != 202 {
		t.Fatalf("retry status=%d body=%s", response.Code, response.Body.String())
	}
	var count int
	a.DB.QueryRow(`SELECT COUNT(*) FROM bill_sync_items i JOIN sync_runs r ON r.id=i.run_id WHERE r.parent_id=? AND i.account_id='account-0002'`, id).Scan(&count)
	if count != 1 {
		t.Fatalf("retry rows=%d", count)
	}
}

func TestBillSupersededResponseCannotOverwrite(t *testing.T) {
	a, input := billFixture(t, 1)
	entered := make(chan struct{})
	release := make(chan struct{})
	a.billFactory = func(string, string) (billSupplier, error) {
		return fakeBillCall(func(_ context.Context, ids []string, from, to string) (supplier.BillQueryResult, error) {
			close(entered)
			<-release
			return goodBillResult(ids, from, to, 100), nil
		}), nil
	}
	result, err := a.createBillRun(context.Background(), input, "old-response", "")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- a.runBillJob(context.Background()) }()
	<-entered
	tx, err := a.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	gen, err := reserveBillGeneration(tx, "conn", "account-0001", input.From)
	if err != nil {
		t.Fatal(err)
	}
	fresh := goodBillResult([]string{"1"}, input.From, input.To, 999)
	if _, err = storeVerifiedBill(tx, "conn", "account-0001", input.From, "Asia/Shanghai", gen, fresh.Bills[0], fresh); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	var amount int64
	if err = a.DB.QueryRow(`SELECT cost_fen FROM platform_bill_daily`).Scan(&amount); err != nil || amount != 999 {
		t.Fatalf("old result overwrote newer value: %d %v", amount, err)
	}
	detail, err := a.billRunDetail(result["run_id"].(string))
	if err != nil || detail["state"] != "interrupted" {
		t.Fatalf("detail=%v %v", detail, err)
	}
}

func TestBillQueryBeyond1000AndExactTotals(t *testing.T) {
	a, input := billFixture(t, 1200)
	a.billFactory = func(string, string) (billSupplier, error) {
		return fakeBillCall(func(_ context.Context, ids []string, from, to string) (supplier.BillQueryResult, error) {
			return goodBillResult(ids, from, to, 1), nil
		}), nil
	}
	runFixture(t, a, input, "many-rows")
	query := billQueryRequest{ConnectionID: "conn", From: input.From, To: input.To, Page: 12, PageSize: 100}
	result, err := a.readBills(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	page := result["pagination"].(map[string]int)
	totals := result["totals"].(map[string]any)
	if page["total"] != 1200 || len(result["items"].([]any)) != 100 || totals["cost_fen"] != "1200" || totals["prompt_tokens"] != "5040000000000" {
		t.Fatalf("page=%v totals=%v", page, totals)
	}
	if result["coverage"].(map[string]any)["complete"] != true {
		t.Fatalf("coverage=%v", result["coverage"])
	}
	query.Page = 1
	query.Q = "account-1200"
	result, err = a.readBills(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if result["pagination"].(map[string]int)["total"] != 1 || result["totals"].(map[string]any)["cost_fen"] != "1" {
		t.Fatalf("filtered=%v", result)
	}
}

func TestBillRoutesRequireAuthCSRFAndReturnQueued(t *testing.T) {
	a, input := billFixture(t, 1)
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	request, _ := http.NewRequest("POST", server.URL+"/api/v1/platform-usage/bills/query", strings.NewReader(`{}`))
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal(response.StatusCode)
	}
	response, err = client.Get(server.URL + "/api/v1/auth/session")
	if err != nil {
		t.Fatal(err)
	}
	var session struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		} `json:"data"`
	}
	json.NewDecoder(response.Body).Decode(&session)
	response.Body.Close()
	request, _ = http.NewRequest("POST", server.URL+"/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"local-dev-password"}`))
	request.Header.Set("X-CSRF-Token", session.Data.CSRF)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(response.Body).Decode(&session)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	data, _ := json.Marshal(input)
	request, _ = http.NewRequest("POST", server.URL+"/api/v1/platform-usage/bills/sync", bytes.NewReader(data))
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal(response.StatusCode)
	}
	request, _ = http.NewRequest("POST", server.URL+"/api/v1/platform-usage/bills/sync", bytes.NewReader(data))
	request.Header.Set("X-CSRF-Token", session.Data.CSRF)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 202 {
		t.Fatal(response.StatusCode)
	}
	var result struct {
		Data map[string]any `json:"data"`
	}
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Data["state"] != "queued" {
		t.Fatalf("result=%v", result)
	}
}

func TestBillActiveIdempotencyAliasAndConflict(t *testing.T) {
	a, input := billFixture(t, 1)
	first, err := a.createBillRun(context.Background(), input, "key-a", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.createBillRun(context.Background(), input, "key-b", "")
	if err != nil || first["run_id"] != second["run_id"] {
		t.Fatalf("dedupe=%v err=%v", second, err)
	}
	changed := input
	changed.Scope = "selected"
	changed.AccountIDs = []string{"account-0001"}
	if _, err = a.createBillRun(context.Background(), changed, "key-b", ""); err == nil {
		t.Fatal("reused alias accepted a different request")
	}
	if _, err = a.DB.Exec(`UPDATE platform_connections SET enabled=0,version=version+1 WHERE id='conn'`); err != nil {
		t.Fatal(err)
	}
	again, err := a.createBillRun(context.Background(), input, "key-a", "")
	if err != nil || again["run_id"] != first["run_id"] {
		t.Fatalf("same request lost its original task after config change: %v %v", again, err)
	}
}

func TestBillAuthorizationFailureStopsConnectionAndPreservesValues(t *testing.T) {
	a, input := billFixture(t, 101)
	calls := 0
	a.billFactory = func(string, string) (billSupplier, error) {
		return fakeBillCall(func(_ context.Context, ids []string, from, to string) (supplier.BillQueryResult, error) {
			calls++
			return supplier.BillQueryResult{}, &supplier.BillHTTPError{Status: 401}
		}), nil
	}
	id := runFixture(t, a, input, "unauthorized")
	detail, err := a.billRunDetail(id)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || detail["state"] != "failed" || detail["counts"].(map[string]int)["failed"] != 101 {
		t.Fatalf("calls=%d detail=%v", calls, detail)
	}
	result, err := a.readBills(context.Background(), billQueryRequest{ConnectionID: "conn", From: input.From, To: input.To})
	if err != nil {
		t.Fatal(err)
	}
	coverage := result["coverage"].(map[string]any)
	if coverage["failed_items"] != int64(101) || coverage["missing_items"] != int64(101) {
		t.Fatalf("coverage=%v", coverage)
	}
}

func TestBillRetryOnlyIncludesFailedAccountDays(t *testing.T) {
	a, input := billFixture(t, 2)
	today := input.To
	day, _ := time.Parse("2006-01-02", today)
	input.From = day.AddDate(0, 0, -1).Format("2006-01-02")
	a.billFactory = func(string, string) (billSupplier, error) {
		return fakeBillCall(func(_ context.Context, ids []string, from, to string) (supplier.BillQueryResult, error) {
			r := goodBillResult(ids, from, to, 100)
			bad := "1"
			if from == today {
				bad = "2"
			}
			r.RowErrors[bad] = "BILL_ROW_INVALID"
			return r, nil
		}), nil
	}
	id := runFixture(t, a, input, "two-days")
	response := httptest.NewRecorder()
	a.retryBillRun(response, httptest.NewRequest("POST", "/retry-failed", strings.NewReader("{}")), id)
	if response.Code != 202 {
		t.Fatalf("status=%d %s", response.Code, response.Body.String())
	}
	var count int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM bill_sync_items i JOIN sync_runs r ON r.id=i.run_id WHERE r.parent_id=?`, id).Scan(&count); err != nil || count != 2 {
		t.Fatalf("retry invented account/day combinations: %d %v", count, err)
	}
}

func TestBillEarlyFailuresAppearInCoverage(t *testing.T) {
	for _, scenario := range []string{"link_changed", "attempts_exhausted", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			a, input := billFixture(t, 1)
			result, err := a.createBillRun(context.Background(), input, "early-failure", "")
			if err != nil {
				t.Fatal(err)
			}
			run := result["run_id"].(string)
			switch scenario {
			case "link_changed":
				_, err = a.DB.Exec(`DELETE FROM platform_links WHERE connection_id='conn'`)
			case "attempts_exhausted":
				_, err = a.DB.Exec(`UPDATE bill_sync_items SET attempts=3 WHERE run_id=?`, run)
			case "expired":
				input.From, input.To = "2000-01-01", "2000-01-01"
				_, err = a.DB.Exec(`UPDATE bill_sync_items SET bill_date=? WHERE run_id=?`, input.From, run)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = a.runBillJob(context.Background()); err != nil {
				t.Fatal(err)
			}
			data, err := a.readBills(context.Background(), billQueryRequest{ConnectionID: "conn", From: input.From, To: input.To})
			if err != nil {
				t.Fatal(err)
			}
			coverage := data["coverage"].(map[string]any)
			if coverage["failed_items"] != int64(1) || coverage["expected_items"] != int64(1) || coverage["complete"] != false {
				t.Fatalf("coverage=%v", coverage)
			}
		})
	}
}

func TestSyncRunListPreservesMetricsHistory(t *testing.T) {
	a, input := billFixture(t, 1)
	if _, err := a.DB.Exec(`INSERT INTO sync_runs(id,connection_id,kind,state,started_at) VALUES('old-metrics','conn','platform-usage','succeeded',?)`, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.createBillRun(context.Background(), input, "list-bill", ""); err != nil {
		t.Fatal(err)
	}
	legacy := httptest.NewRecorder()
	a.billRunList(legacy, httptest.NewRequest("GET", "/sync-runs", nil))
	if legacy.Code != 200 || !strings.Contains(legacy.Body.String(), "old-metrics") {
		t.Fatalf("legacy=%s", legacy.Body.String())
	}
	bills := httptest.NewRecorder()
	a.billRunList(bills, httptest.NewRequest("GET", "/sync-runs?kind=bill", nil))
	if bills.Code != 200 || strings.Contains(bills.Body.String(), "old-metrics") || !strings.Contains(bills.Body.String(), "run_id") {
		t.Fatalf("bills=%s", bills.Body.String())
	}
}
