package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

type platformEnvelope struct {
	Data struct {
		Items []struct {
			ID             string          `json:"id"`
			Account        string          `json:"account"`
			PlatformStatus string          `json:"platform_status"`
			Platform       json.RawMessage `json:"platform"`
		} `json:"items"`
	} `json:"data"`
}

func decodePlatform(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	if string(raw) == "null" || len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// routesFixture syncs three remote accounts (one near its 5-hour limit, one
// banned, one without usage) and returns a logged-in admin client.
func routesFixture(t *testing.T) (*App, *webConsoleStub, *testClient) {
	t.Helper()
	a, stub := webFixture(t)
	stub.setAccounts(
		remoteAccount(428802, "one@example.test", "using", "2026-11-11 19:36", fixtureUsage(35209959, 0.05), nil),
		remoteAccount(411605, "hot@example.test", "using", "2026-11-10 00:14", fixtureUsage(4132689422, 0.84), nil),
		remoteAccount(385753, "banned@example.test", "ban", "2026-11-05 01:57", nil, nil),
	)
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	admin := newTestClient(t, a)
	admin.login("workflow-test-password")
	return a, stub, admin
}

func TestAccountListExposesPlatformBlock(t *testing.T) {
	a, _, admin := routesFixture(t)
	var list platformEnvelope
	res := admin.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=20", "")
	_ = jsonDecode(res, &list)
	if res.StatusCode != 200 || len(list.Data.Items) != 3 {
		t.Fatalf("status=%d items=%d", res.StatusCode, len(list.Data.Items))
	}
	byEmail := map[string]map[string]any{}
	for _, item := range list.Data.Items {
		byEmail[item.Account] = decodePlatform(t, item.Platform)
		if item.Account == "banned@example.test" && item.PlatformStatus != "ban" {
			t.Fatalf("platform_status for banned=%q", item.PlatformStatus)
		}
	}
	one := byEmail["one@example.test"]
	if one == nil || one["status"] != "using" || one["aid"] != "438802" || one["source_channel"] != "web" || one["locked"] != false {
		t.Fatalf("one platform=%v", one)
	}
	usage, _ := one["usage"].(map[string]any)
	if usage == nil || usage["cost_fen"] != float64(3521) || usage["cost_yuan"] != "35.21" || usage["input_tokens"] != float64(411638) || usage["five_hour_used_percent"] != float64(5) || usage["week_used_percent"] != float64(1) || usage["five_hour_reset_at"] != "2026-11-11T16:50:00Z" {
		t.Fatalf("one usage=%v", usage)
	}
	profile, _ := one["profile"].(map[string]any)
	if profile == nil || profile["balance"] != "100" || profile["payment_method"] != "google_pay" || profile["register_location"] != "US" {
		t.Fatalf("one profile=%v", profile)
	}
	banned := byEmail["banned@example.test"]
	if banned == nil || banned["usage"] != nil || !strings.Contains(fmt.Sprint(banned["reason_text"]), "385753") || banned["reason_kind"] == "" {
		t.Fatalf("banned platform=%v", banned)
	}
	// Filter and sort by the 5-hour window.
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=20&five_hour_min=80", ""), &list)
	if len(list.Data.Items) != 1 || list.Data.Items[0].Account != "hot@example.test" {
		t.Fatalf("five_hour_min filter items=%+v", list.Data.Items)
	}
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=20&sort=five_hour_desc", ""), &list)
	if len(list.Data.Items) != 3 || list.Data.Items[0].Account != "hot@example.test" || list.Data.Items[1].Account != "one@example.test" {
		t.Fatalf("sort order=%v", []string{list.Data.Items[0].Account, list.Data.Items[1].Account, list.Data.Items[2].Account})
	}
	// Default sort is platform ID high→low (aid prefers over upstream id).
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=20", ""), &list)
	if len(list.Data.Items) != 3 || list.Data.Items[0].Account != "one@example.test" || list.Data.Items[1].Account != "hot@example.test" || list.Data.Items[2].Account != "banned@example.test" {
		t.Fatalf("platform_id_desc order=%v", []string{list.Data.Items[0].Account, list.Data.Items[1].Account, list.Data.Items[2].Account})
	}
	assertEnvelope(t, admin.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=20&five_hour_min=abc", ""), http.StatusUnprocessableEntity, "QUERY_INVALID")
	// Detail carries the same block.
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	var detail struct {
		Data struct {
			Platform json.RawMessage `json:"platform"`
		} `json:"data"`
	}
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/accounts/"+id, ""), &detail)
	if p := decodePlatform(t, detail.Data.Platform); p == nil || p["status"] != "using" || p["upstream_id"] != "428802" {
		t.Fatalf("detail platform=%v", p)
	}
	// An account the platform never saw has no block.
	insertLocalAccount(t, a, "acc-local", "local@example.test")
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/accounts/acc-local", ""), &detail)
	if p := decodePlatform(t, detail.Data.Platform); p != nil {
		t.Fatalf("unlinked account platform=%v", p)
	}
}

func TestRemoteAccountsEndpoint(t *testing.T) {
	a, stub, admin := routesFixture(t)
	// A second delivery for the banned email supersedes the first; one row
	// without an email stays unmatched; then the hot account disappears.
	noEmail := remoteAccount(777, "", "using", "2026-11-09 00:00", nil, nil)
	noEmail["auth_config"] = ""
	stub.setAccounts(
		remoteAccount(428802, "one@example.test", "using", "2026-11-11 19:36", fixtureUsage(35209959, 0.05), map[string]any{"plan_end_time": "2027-01-01"}),
		remoteAccount(385753, "banned@example.test", "ban", "2026-11-05 01:57", nil, nil),
		remoteAccount(385999, "banned@example.test", "using", "2026-11-12 01:00", nil, nil),
		noEmail,
	)
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Data struct {
			List []struct {
				UpstreamID   string   `json:"upstream_id"`
				Email        string   `json:"email"`
				ExtStatus    string   `json:"ext_status"`
				AccountID    string   `json:"account_id"`
				AutoCreated  bool     `json:"auto_created"`
				SupersededBy string   `json:"superseded_by"`
				MissingSince string   `json:"missing_since"`
				ProfileDiff  []string `json:"profile_diff"`
				ReasonText   string   `json:"reason_text"`
			} `json:"list"`
			Summary       map[string]int `json:"summary"`
			CountsByState map[string]int `json:"counts_by_status"`
			Total         int            `json:"total"`
		} `json:"data"`
	}
	res := admin.do(http.MethodGet, "/api/v1/platform-connections/conn/remote-accounts?page=1&page_size=10", "")
	_ = jsonDecode(res, &out)
	if res.StatusCode != 200 || out.Data.Total != 5 || len(out.Data.List) != 5 {
		t.Fatalf("status=%d total=%d list=%d", res.StatusCode, out.Data.Total, len(out.Data.List))
	}
	s := out.Data.Summary
	if s["total"] != 5 || s["linked"] != 4 || s["auto_created"] != 3 || s["unmatched"] != 1 || s["superseded"] != 1 || s["missing"] != 1 {
		t.Fatalf("summary=%v", s)
	}
	rows := map[string]int{}
	for i, r := range out.Data.List {
		rows[r.UpstreamID] = i
	}
	if r := out.Data.List[rows["385753"]]; r.SupersededBy != "385999" || !strings.Contains(r.ReasonText, "385753") {
		t.Fatalf("superseded row=%+v", r)
	}
	if r := out.Data.List[rows["411605"]]; r.MissingSince == "" {
		t.Fatalf("missing row=%+v", r)
	}
	if r := out.Data.List[rows["777"]]; r.AccountID != "" || r.Email != "" {
		t.Fatalf("unmatched row=%+v", r)
	}
	// Local plan_end_time (2026-12-31 from insertLocalAccount) vs remote 2027-01-01.
	insertLocalAccount(t, a, "acc-diff", "diff@example.test")
	stub.setAccounts(remoteAccount(500, "diff@example.test", "using", "2026-11-12 02:00", nil, map[string]any{"plan_end_time": "2027-01-01"}))
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/platform-connections/conn/remote-accounts?q=diff@", ""), &out)
	if len(out.Data.List) != 1 || out.Data.List[0].AutoCreated || fmt.Sprint(out.Data.List[0].ProfileDiff) != "[plan_end_time]" {
		t.Fatalf("diff row=%+v", out.Data.List)
	}
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/platform-connections/conn/remote-accounts?status=ban", ""), &out)
	if len(out.Data.List) != 1 || out.Data.List[0].UpstreamID != "385753" || out.Data.CountsByState["ban"] != 1 {
		t.Fatalf("status filter=%+v counts=%v", out.Data.List, out.Data.CountsByState)
	}
	if res := admin.do(http.MethodGet, "/api/v1/platform-connections/nope/remote-accounts", ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown connection status=%d", res.StatusCode)
	}
}

func TestBillModelsEndpoints(t *testing.T) {
	a, stub, admin := routesFixture(t)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	today := time.Now().In(loc).Format("2006-01-02")
	yesterday := time.Now().In(loc).AddDate(0, 0, -1).Format("2006-01-02")
	stub.mu.Lock()
	stub.bills = func(ids []string, start, end string) string {
		return billBody([]string{billRow(428802, today, "claude-opus-5", "27.61"), billRow(428802, today, "claude-opus-5-5", "7.60"), billRow(411605, yesterday, "claude-opus-5-5", "100.00")}, start, end)
	}
	stub.mu.Unlock()
	if err := a.syncWebBills(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Data struct {
			List []struct {
				BillDate  string `json:"bill_date"`
				Account   string `json:"account"`
				ModelName string `json:"model_name"`
				CostFen   int64  `json:"cost_fen"`
				Final     bool   `json:"final"`
			} `json:"list"`
			ByAccount []struct {
				Account string `json:"account"`
				CostFen int64  `json:"cost_fen"`
			} `json:"by_account"`
			ByModel []struct {
				ModelName string `json:"model_name"`
				CostFen   int64  `json:"cost_fen"`
			} `json:"by_model"`
			TotalCostFen int64 `json:"total_cost_fen"`
			Coverage     struct {
				DaysRequested int  `json:"days_requested"`
				DaysPresent   int  `json:"days_present"`
				TodayFinal    bool `json:"today_final"`
			} `json:"coverage"`
			Total int `json:"total"`
		} `json:"data"`
	}
	from := time.Now().In(loc).AddDate(0, 0, -6).Format("2006-01-02")
	res := admin.do(http.MethodGet, "/api/v1/platform-usage/bills/models?connection_id=conn&from="+from+"&to="+today+"&page=1&page_size=20", "")
	_ = jsonDecode(res, &out)
	if res.StatusCode != 200 || out.Data.Total != 3 || len(out.Data.List) != 3 || out.Data.TotalCostFen != 13521 {
		t.Fatalf("status=%d total=%d rows=%d sum=%d", res.StatusCode, out.Data.Total, len(out.Data.List), out.Data.TotalCostFen)
	}
	if out.Data.List[0].BillDate != today || !strings.Contains(out.Data.List[0].Account, "@example.test") || out.Data.List[0].Final {
		t.Fatalf("first row=%+v", out.Data.List[0])
	}
	if len(out.Data.ByAccount) != 2 || len(out.Data.ByModel) != 2 || out.Data.ByModel[0].ModelName != "claude-opus-5-5" || out.Data.ByModel[0].CostFen != 10760 {
		t.Fatalf("by_account=%+v by_model=%+v", out.Data.ByAccount, out.Data.ByModel)
	}
	if out.Data.Coverage.DaysRequested != 7 || out.Data.Coverage.DaysPresent != 2 || out.Data.Coverage.TodayFinal {
		t.Fatalf("coverage=%+v", out.Data.Coverage)
	}
	oneID := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/platform-usage/bills/models?connection_id=conn&from="+from+"&to="+today+"&account_ids="+oneID, ""), &out)
	if out.Data.Total != 2 || out.Data.TotalCostFen != 3521 {
		t.Fatalf("account filter total=%d sum=%d", out.Data.Total, out.Data.TotalCostFen)
	}
	assertEnvelope(t, admin.do(http.MethodGet, "/api/v1/platform-usage/bills/models?connection_id=conn&from=2026-13-01&to="+today, ""), http.StatusUnprocessableEntity, "BILL_RANGE_INVALID")
	assertEnvelope(t, admin.do(http.MethodGet, "/api/v1/platform-usage/bills/models?from="+from+"&to="+today, ""), http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	// Manual sync: accepted, then refused while backing off.
	var started struct {
		Data struct {
			RunID string `json:"run_id"`
		} `json:"data"`
	}
	res = admin.do(http.MethodPost, "/api/v1/platform-usage/bills/models/sync", `{"connection_id":"conn"}`)
	_ = jsonDecode(res, &started)
	if res.StatusCode != http.StatusAccepted || started.Data.RunID == "" {
		t.Fatalf("sync status=%d run=%q", res.StatusCode, started.Data.RunID)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if state := queryString(t, a, `SELECT state FROM sync_runs WHERE id=?`, started.Data.RunID); state != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("manual sync never finished")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := a.DB.Exec(`UPDATE platform_connections SET updated_at=? WHERE id='conn'`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	// A failure newer than the manual run's success starts a backoff.
	insertKindRun(t, a, "conn", "web-bills", "failed", time.Now().Add(2*time.Second))
	env := assertEnvelope(t, admin.do(http.MethodPost, "/api/v1/platform-usage/bills/models/sync", `{"connection_id":"conn"}`), http.StatusConflict, "SYNC_IN_BACKOFF")
	if env.Error.Details["next_attempt_at"] == nil {
		t.Fatalf("backoff envelope lacks next_attempt_at: %+v", env)
	}
	if _, err := a.DB.Exec(`UPDATE platform_connections SET web_username='' WHERE id='conn'`); err != nil {
		t.Fatal(err)
	}
	assertEnvelope(t, admin.do(http.MethodPost, "/api/v1/platform-usage/bills/models/sync", `{"connection_id":"conn"}`), http.StatusUnprocessableEntity, "WEB_NOT_CONFIGURED")
}

func TestUsageHistoryEndpoint(t *testing.T) {
	a, stub, admin := routesFixture(t)
	stub.setAccounts(remoteAccount(428802, "one@example.test", "using", "2026-11-11 19:36", fixtureUsage(36000000, 0.09), nil))
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	var out struct {
		Data struct {
			List []struct {
				ObservedAt   string  `json:"observed_at"`
				CostFen      int64   `json:"cost_fen"`
				FiveHourUsed float64 `json:"five_hour_used_percent"`
			} `json:"list"`
		} `json:"data"`
	}
	res := admin.do(http.MethodGet, "/api/v1/accounts/"+id+"/platform-usage-history?days=7", "")
	_ = jsonDecode(res, &out)
	if res.StatusCode != 200 || len(out.Data.List) != 2 || out.Data.List[0].CostFen != 3521 || out.Data.List[1].CostFen != 3600 || out.Data.List[1].FiveHourUsed != 9 {
		t.Fatalf("status=%d list=%+v", res.StatusCode, out.Data.List)
	}
	assertEnvelope(t, admin.do(http.MethodGet, "/api/v1/accounts/"+id+"/platform-usage-history?days=400", ""), http.StatusUnprocessableEntity, "QUERY_INVALID")
}

func TestDashboardWebFields(t *testing.T) {
	a, _, admin := routesFixture(t)
	insertKindRun(t, a, "conn", "web-bills", "failed", time.Now().Add(-time.Minute))
	var d struct {
		Data struct {
			Platform struct {
				SyncStatus    string         `json:"sync_status"`
				NearLimit     int            `json:"near_limit_accounts"`
				RemoteCounts  map[string]int `json:"remote_counts_by_status"`
				TodayModelFen int64          `json:"today_model_cost_fen"`
				Web           struct {
					SyncStatus string `json:"sync_status"`
					ErrorCode  string `json:"error_code"`
				} `json:"web_sync_health"`
			} `json:"platform"`
		} `json:"data"`
	}
	res := admin.do(http.MethodGet, "/api/v1/dashboard", "")
	_ = jsonDecode(res, &d)
	p := d.Data.Platform
	if res.StatusCode != 200 || p.NearLimit != 1 || p.RemoteCounts["using"] != 2 || p.RemoteCounts["ban"] != 1 || p.Web.SyncStatus != "failing" || p.Web.ErrorCode != "WEB_LOGIN_FAILED" {
		t.Fatalf("status=%d platform=%+v", res.StatusCode, p)
	}
	if p.SyncStatus != "never" {
		t.Fatalf("metrics health must stay separate: %q", p.SyncStatus)
	}
}

func TestConnectionLocationFallsBackToUTC(t *testing.T) {
	if connectionLocation("") != time.UTC || connectionLocation("Nowhere/Zone") != time.UTC || connectionLocation("Local") != time.UTC {
		t.Fatal("empty or invalid zones must fall back to UTC")
	}
	if connectionLocation("Asia/Shanghai").String() != "Asia/Shanghai" {
		t.Fatal("valid zone not loaded")
	}
}

func TestDashboardSpendFields(t *testing.T) {
	a, stub, today, yesterday := billsFixture(t)
	stub.mu.Lock()
	stub.bills = func(ids []string, start, end string) string {
		return billBody([]string{billRow(428802, today, "claude-opus-5", "27.61"), billRow(428802, today, "claude-opus-5-5", "7.60"), billRow(428242, yesterday, "claude-opus-5-5", "100.00"), billRow(428242, yesterday, "claude-opus-5", "0.92")}, start, end)
	}
	stub.mu.Unlock()
	if err := a.syncWebBills(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	old := time.Now().In(loc).AddDate(0, 0, -8).Format("2006-01-02")
	if _, err := a.DB.Exec(`INSERT INTO platform_bill_models(id,connection_id,upstream_id,account_id,bill_date,model_name,bill_model_name,cost_fen,fetched_at,generation,final) VALUES(?,'conn','428802','',?,'claude-opus-5','claude-opus-5',99999,?,1,1)`, newID(), old, now()); err != nil {
		t.Fatal(err)
	}
	spend, err := a.webDashboard(context.Background(), ownershipScope{Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	if spend["today_model_cost_fen"] != int64(3521) || spend["week_model_cost_fen"] != int64(13613) {
		t.Fatalf("today=%v week=%v", spend["today_model_cost_fen"], spend["week_model_cost_fen"])
	}
	days, _ := spend["cost_by_day"].([]map[string]any)
	if len(days) != 7 || days[6]["date"] != today || days[6]["final"] != false || days[6]["cost_fen"] != int64(3521) || days[5]["date"] != yesterday || days[5]["final"] != true || days[5]["cost_fen"] != int64(10092) || days[0]["cost_fen"] != int64(0) {
		t.Fatalf("cost_by_day=%v", days)
	}
	models, _ := spend["cost_by_model"].([]map[string]any)
	if len(models) != 2 || models[0]["model_name"] != "claude-opus-5-5" || models[0]["cost_fen"] != int64(10760) {
		t.Fatalf("cost_by_model=%v", models)
	}
	accounts, _ := spend["cost_by_account"].([]map[string]any)
	if len(accounts) != 2 || accounts[0]["account"] != "two@example.test" || accounts[0]["cost_fen"] != int64(10092) {
		t.Fatalf("cost_by_account=%v", accounts)
	}
}
