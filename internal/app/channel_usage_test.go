package app

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFenToUsdCents(t *testing.T) {
	if got := fenToUsdCents(75104, 7.2); got != 10431 {
		t.Fatalf("75104/7.2=%d", got)
	}
	if got := fenToUsdCents(720, 7.2); got != 100 {
		t.Fatalf("720/7.2=%d", got)
	}
	if fenToUsdCents(100, 0) != 0 || fenToUsdCents(100, -1) != 0 {
		t.Fatal("invalid rate should yield 0")
	}
}

func TestChannelUsageRequiresOwnPermission(t *testing.T) {
	a, admin := rbacApp(t)
	var role struct {
		Data struct{ ID string }
	}
	_ = jsonDecode(admin.do(http.MethodPost, "/api/v1/roles", `{"name":"仅离线账单","permissions":["bills.view"]}`), &role)
	_ = jsonDecode(admin.do(http.MethodPost, "/api/v1/users", `{"username":"bills-only","password":"bills-only-pass-1","role_ids":["`+role.Data.ID+`"]}`), &struct{ Data struct{ ID string } }{})
	c := newTestClient(t, a)
	c.loginAs("bills-only", "bills-only-pass-1")
	assertEnvelope(t, c.do(http.MethodGet, "/api/v1/platform-usage/channel-usage/accounts?connection_id=x", ""), http.StatusForbidden, "PERMISSION_DENIED")

	_ = jsonDecode(admin.do(http.MethodPost, "/api/v1/roles", `{"name":"仅渠道用量","permissions":["channel_usage.view"]}`), &role)
	_ = jsonDecode(admin.do(http.MethodPost, "/api/v1/users", `{"username":"cu-only","password":"cu-only-pass-1","role_ids":["`+role.Data.ID+`"]}`), &struct{ Data struct{ ID string } }{})
	cu := newTestClient(t, a)
	cu.loginAs("cu-only", "cu-only-pass-1")
	assertEnvelope(t, cu.do(http.MethodGet, "/api/v1/platform-usage/channel-usage/accounts", ""), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
}

func TestChannelUsageEndpoints(t *testing.T) {
	a, stub, admin := routesFixture(t)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	today := time.Now().In(loc).Format("2006-01-02")
	yesterday := time.Now().In(loc).AddDate(0, 0, -1).Format("2006-01-02")
	stub.mu.Lock()
	stub.bills = func(ids []string, start, end string) string {
		return billBody([]string{
			billRow(428802, today, "claude-opus-5", "27.61"),
			billRow(428802, today, "claude-opus-5-5", "7.60"),
			billRow(411605, yesterday, "claude-opus-5-5", "100.00"),
		}, start, end)
	}
	stub.mu.Unlock()
	if err := a.syncWebBills(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}

	var accounts struct {
		Data struct {
			List []struct {
				UpstreamID        string `json:"upstream_id"`
				PlatformAccountID string `json:"platform_account_id"`
				Account           string `json:"account"`
			} `json:"list"`
			Owners []struct {
				ID string `json:"id"`
			} `json:"owners"`
		} `json:"data"`
	}
	res := admin.do(http.MethodGet, "/api/v1/platform-usage/channel-usage/accounts?connection_id=conn", "")
	_ = jsonDecode(res, &accounts)
	if res.StatusCode != 200 || len(accounts.Data.List) < 3 {
		t.Fatalf("accounts status=%d list=%d", res.StatusCode, len(accounts.Data.List))
	}
	byUpstream := map[string]string{}
	for _, row := range accounts.Data.List {
		byUpstream[row.UpstreamID] = row.PlatformAccountID
	}
	// Fixture aid for 428802 is 438802 (see TestAccountListExposesPlatformBlock).
	if byUpstream["428802"] != "438802" {
		t.Fatalf("platform_account_id for 428802=%q want 438802", byUpstream["428802"])
	}

	from := time.Now().In(loc).AddDate(0, 0, -6).Format("2006-01-02")
	var out struct {
		Data struct {
			List []struct {
				BillDate          string `json:"bill_date"`
				UpstreamID        string `json:"upstream_id"`
				PlatformAccountID string `json:"platform_account_id"`
				Account           string `json:"account"`
				CostFen           int64  `json:"cost_fen"`
				CostUsd           string `json:"cost_usd"`
				Final             bool   `json:"final"`
			} `json:"list"`
			ByDay []struct {
				BillDate string `json:"bill_date"`
				CostFen  int64  `json:"cost_fen"`
				CostUsd  string `json:"cost_usd"`
			} `json:"by_day"`
			Total        int     `json:"total"`
			TotalCostFen int64   `json:"total_cost_fen"`
			TotalCostUsd string  `json:"total_cost_usd"`
			USDRate      float64 `json:"usd_cny_rate"`
		} `json:"data"`
	}
	res = admin.do(http.MethodGet, "/api/v1/platform-usage/channel-usage?connection_id=conn&from="+from+"&to="+today+"&page=1&page_size=20", "")
	_ = jsonDecode(res, &out)
	if res.StatusCode != 200 || out.Data.Total != 2 || out.Data.TotalCostFen != 13521 {
		t.Fatalf("status=%d total=%d sum=%d list=%+v", res.StatusCode, out.Data.Total, out.Data.TotalCostFen, out.Data.List)
	}
	if out.Data.USDRate != 7.2 {
		t.Fatalf("rate=%v", out.Data.USDRate)
	}
	// 13521 fen / 7.2 ≈ 1878 cents → 18.78
	if out.Data.TotalCostUsd != "18.78" {
		t.Fatalf("total usd=%q", out.Data.TotalCostUsd)
	}
	if len(out.Data.ByDay) != 2 {
		t.Fatalf("by_day=%+v", out.Data.ByDay)
	}
	byDay := map[string]int64{}
	for _, d := range out.Data.ByDay {
		byDay[d.BillDate] = d.CostFen
	}
	if byDay[today] != 3521 || byDay[yesterday] != 10000 {
		t.Fatalf("by_day totals=%v", byDay)
	}
	foundToday := false
	for _, row := range out.Data.List {
		if row.UpstreamID == "428802" && row.BillDate == today {
			foundToday = true
			if row.CostFen != 3521 || row.Final || row.PlatformAccountID != "438802" {
				t.Fatalf("today row=%+v", row)
			}
		}
	}
	if !foundToday {
		t.Fatalf("missing today aggregate: %+v", out.Data.List)
	}

	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/platform-usage/channel-usage?connection_id=conn&from="+from+"&to="+today+"&upstream_ids=428802", ""), &out)
	if out.Data.Total != 1 || out.Data.TotalCostFen != 3521 {
		t.Fatalf("upstream filter total=%d sum=%d", out.Data.Total, out.Data.TotalCostFen)
	}

	assertEnvelope(t, admin.do(http.MethodGet, "/api/v1/platform-usage/channel-usage?connection_id=conn&from=2026-13-01&to="+today, ""), http.StatusUnprocessableEntity, "BILL_RANGE_INVALID")
	assertEnvelope(t, admin.do(http.MethodGet, "/api/v1/platform-usage/channel-usage?from="+from+"&to="+today, ""), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	assertEnvelope(t, admin.do(http.MethodGet, "/api/v1/platform-usage/channel-usage/accounts", ""), http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	export := admin.do(http.MethodGet, "/api/v1/platform-usage/channel-usage/export?connection_id=conn&from="+from+"&to="+today, "")
	defer export.Body.Close()
	if export.StatusCode != 200 {
		t.Fatalf("export status=%d", export.StatusCode)
	}
	ct := export.Header.Get("Content-Type")
	if !strings.Contains(ct, "spreadsheetml") {
		t.Fatalf("export content-type=%q", ct)
	}
	cd := export.Header.Get("Content-Disposition")
	if !strings.Contains(cd, "channel-usage-") || !strings.Contains(cd, ".xlsx") {
		t.Fatalf("disposition=%q", cd)
	}
	body, err := io.ReadAll(export.Body)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	var sheet string
	for _, f := range zr.File {
		if f.Name == "xl/worksheets/sheet1.xml" {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			sheet = string(raw)
			break
		}
	}
	if sheet == "" {
		t.Fatal("missing sheet1.xml")
	}
	for _, want := range []string{"日期", "账号 ID", "消耗额度（美元）", "438802", "18.78", "合计"} {
		if !strings.Contains(sheet, want) {
			t.Fatalf("export sheet missing %q", want)
		}
	}
	assertEnvelope(t, admin.do(http.MethodGet, "/api/v1/platform-usage/channel-usage/export?from="+from+"&to="+today, ""), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
}

func TestChannelUsageRespectsOwnership(t *testing.T) {
	a, admin, opsA, opsB := ownershipUsers(t)
	if _, err := a.DB.Exec(`INSERT INTO platform_connections(id,name,base_url,timezone,enabled,version,usd_cny_rate,created_at,updated_at) VALUES('cu-conn','CU','https://x.example','Asia/Shanghai',1,1,7.2,?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	accA := createOwnedAccount(t, opsA, "cu-a@example.test", "")
	accB := createOwnedAccount(t, opsB, "cu-b@example.test", "")
	day := "2026-11-20"
	stamp := now()
	for _, row := range []struct {
		id, upstream, account string
		fen                   int64
	}{
		{"cu1", "1001", accA, 720},
		{"cu2", "1002", accB, 1440},
	} {
		if _, err := a.DB.Exec(`INSERT INTO platform_remote_accounts(connection_id,upstream_id,upstream_aid,account_id,account_email,ext_status,missing_since,first_seen_at,last_seen_at) VALUES('cu-conn',?,?,?,?, 'using','',?,?)`, row.upstream, row.upstream, row.account, "u"+row.upstream+"@example.test", stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err := a.DB.Exec(`INSERT INTO platform_bill_models(id,connection_id,upstream_id,account_id,bill_date,model_name,bill_model_name,cost_fen,fetched_at,generation,final) VALUES(?,'cu-conn',?,?,?,'m','m',?,?,1,1)`, row.id, row.upstream, row.account, day, row.fen, stamp); err != nil {
			t.Fatal(err)
		}
	}

	var meA struct {
		Data struct {
			User struct{ ID string }
		}
	}
	_ = jsonDecode(opsA.do(http.MethodGet, "/api/v1/auth/session", ""), &meA)

	var out struct {
		Data struct {
			List []struct {
				UpstreamID string `json:"upstream_id"`
				CostFen    int64  `json:"cost_fen"`
			} `json:"list"`
			Total        int   `json:"total"`
			TotalCostFen int64 `json:"total_cost_fen"`
		} `json:"data"`
	}
	res := opsA.do(http.MethodGet, "/api/v1/platform-usage/channel-usage?connection_id=cu-conn&from="+day+"&to="+day, "")
	_ = jsonDecode(res, &out)
	if res.StatusCode != 200 || out.Data.Total != 1 || out.Data.TotalCostFen != 720 || out.Data.List[0].UpstreamID != "1001" {
		t.Fatalf("opsA usage=%+v status=%d", out.Data, res.StatusCode)
	}

	var accOut struct {
		Data struct {
			List []struct {
				UpstreamID string `json:"upstream_id"`
			} `json:"list"`
			Owners []any `json:"owners"`
		} `json:"data"`
	}
	_ = jsonDecode(opsA.do(http.MethodGet, "/api/v1/platform-usage/channel-usage/accounts?connection_id=cu-conn", ""), &accOut)
	if len(accOut.Data.List) != 1 || accOut.Data.List[0].UpstreamID != "1001" || accOut.Data.Owners != nil {
		t.Fatalf("opsA accounts=%+v", accOut.Data)
	}

	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/platform-usage/channel-usage?connection_id=cu-conn&from="+day+"&to="+day+"&owner_user_id="+meA.Data.User.ID, ""), &out)
	if out.Data.Total != 1 || out.Data.TotalCostFen != 720 {
		t.Fatalf("admin owner filter=%+v", out.Data)
	}
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/platform-usage/channel-usage?connection_id=cu-conn&from="+day+"&to="+day, ""), &out)
	if out.Data.Total != 2 || out.Data.TotalCostFen != 2160 {
		t.Fatalf("admin all=%+v", out.Data)
	}

	export := opsA.do(http.MethodGet, "/api/v1/platform-usage/channel-usage/export?connection_id=cu-conn&from="+day+"&to="+day, "")
	defer export.Body.Close()
	if export.StatusCode != 200 {
		t.Fatalf("opsA export status=%d", export.StatusCode)
	}
	raw, err := io.ReadAll(export.Body)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var sheet string
	for _, f := range zr.File {
		if f.Name != "xl/worksheets/sheet1.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		sheet = string(body)
	}
	if !strings.Contains(sheet, "1001") || strings.Contains(sheet, "1002") {
		t.Fatalf("opsA export should only include owned upstream: %s", sheet)
	}
}
