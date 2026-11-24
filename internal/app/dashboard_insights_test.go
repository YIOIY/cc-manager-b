package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

type insightsReply struct {
	USDRate *float64 `json:"usd_cny_rate"`
	Fleet   struct {
		Accounts     int     `json:"accounts"`
		CostFenTotal int64   `json:"cost_fen_total"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		AvgFiveHour  float64 `json:"avg_five_hour_percent"`
		AvgWeek      float64 `json:"avg_week_percent"`
	} `json:"fleet"`
	Quota []struct {
		AccountID         string   `json:"account_id"`
		Account           string   `json:"account"`
		Alias             string   `json:"alias"`
		PlatformAccountID string   `json:"platform_account_id"`
		CreatedByUserID   string   `json:"created_by_user_id"`
		CreatedByNickname string   `json:"created_by_nickname"`
		CreatedByUsername string   `json:"created_by_username"`
		Status            string   `json:"status"`
		FiveHour          *float64 `json:"five_hour_percent"`
		FiveReset         string   `json:"five_hour_reset_at"`
		Week              *float64 `json:"week_percent"`
		WeekReset         string   `json:"week_reset_at"`
		CostFen           *int64   `json:"cost_fen"`
	} `json:"quota"`
	Hourly []struct {
		Hour    string `json:"hour"`
		CostFen int64  `json:"cost_fen"`
	} `json:"hourly"`
	Composition struct {
		ByDelivery map[string]int `json:"by_delivery"`
		Expiry     map[string]int `json:"expiry"`
		NextExpiry string         `json:"next_expiry"`
	} `json:"composition"`
	Idle []struct {
		Account           string   `json:"account"`
		PlatformAccountID string   `json:"platform_account_id"`
		Week              *float64 `json:"week_percent"`
		PlanEnd           string   `json:"plan_end"`
		DaysLeft          *int     `json:"days_left"`
	} `json:"idle"`
	RecentJobs []struct {
		ID         string `json:"id"`
		AccountID  string `json:"account_id"`
		Account    string `json:"account"`
		Connection string `json:"connection"`
		State      string `json:"state"`
		Code       string `json:"code"`
		UpdatedAt  string `json:"updated_at"`
	} `json:"recent_jobs"`
}

func insightsOf(t *testing.T, a *App, at time.Time, jobs bool) insightsReply {
	t.Helper()
	data, err := a.dashboardInsightsData(context.Background(), at, jobs, ownershipScope{Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(data)
	var out insightsReply
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

var insightsAt = time.Date(2026, 11, 13, 4, 30, 0, 0, time.UTC)

func TestDashboardInsightsFleetQuotaAndComposition(t *testing.T) {
	a, _, admin := routesFixture(t)
	insertLocalAccount(t, a, "acc-local", "local@example.test") // never onboarded
	out := insightsOf(t, a, insightsAt, true)

	if out.USDRate == nil || *out.USDRate != 7.2 {
		t.Fatalf("usd rate=%v", out.USDRate)
	}
	f := out.Fleet
	// one: ¥35.21, 5% / 1%; hot: ¥4132.69, 84% / 1%; banned has no usage.
	if f.Accounts != 3 || f.CostFenTotal != 3521+413269 || f.InputTokens != 2*411638 || f.OutputTokens != 2*82648 || f.AvgFiveHour != 44.5 || f.AvgWeek != 1 {
		t.Fatalf("fleet=%+v", f)
	}
	if len(out.Quota) != 3 || out.Quota[0].Account != "hot@example.test" || out.Quota[1].Account != "one@example.test" || out.Quota[2].Account != "banned@example.test" {
		t.Fatalf("quota order=%+v", out.Quota)
	}
	hot, banned := out.Quota[0], out.Quota[2]
	if hot.FiveHour == nil || *hot.FiveHour != 84 || hot.Week == nil || *hot.Week != 1 || hot.CostFen == nil || *hot.CostFen != 413269 || hot.Status != "using" || hot.FiveReset != "2026-11-11T16:50:00Z" || hot.AccountID == "" || hot.Alias == "" {
		t.Fatalf("hot=%+v", hot)
	}
	// remoteAccount sets a1_id = upstream_id + 10000 (411605 → 421605).
	if hot.PlatformAccountID != "421605" {
		t.Fatalf("hot platform_account_id=%q", hot.PlatformAccountID)
	}
	if banned.FiveHour != nil || banned.Week != nil || banned.CostFen != nil || banned.Status != "ban" {
		t.Fatalf("banned=%+v", banned)
	}
	if d := out.Composition.ByDelivery; d["linked"] != 3 || d["pending"] != 1 || d["failed"] != 0 || d["submitting"] != 0 || d["unknown"] != 0 {
		t.Fatalf("by_delivery=%v", d)
	}
	// The fixture plans end 2026-12-10: 27 days after the fixed "today".
	if e := out.Composition.Expiry; e["within_30"] != 3 || e["within_7"] != 0 || e["expired"] != 0 || e["later"] != 0 || e["unknown"] != 0 {
		t.Fatalf("expiry=%v", e)
	}
	if out.Composition.NextExpiry != "2026-12-10" {
		t.Fatalf("next expiry=%q", out.Composition.NextExpiry)
	}
	// hot uses 1% of its week but 84% of its 5-hour window: busy, not idle.
	// banned has no usage and is not in use.
	if len(out.Idle) != 1 || out.Idle[0].Account != "one@example.test" || out.Idle[0].Week == nil || *out.Idle[0].Week != 1 ||
		out.Idle[0].PlanEnd != "2026-12-10" || out.Idle[0].DaysLeft == nil || *out.Idle[0].DaysLeft != 27 ||
		out.Idle[0].PlatformAccountID != "438802" {
		t.Fatalf("idle=%+v", out.Idle)
	}
	if out.RecentJobs == nil || len(out.RecentJobs) != 0 {
		t.Fatalf("recent jobs=%v", out.RecentJobs)
	}

	// Expiry buckets follow the platform date and the connection's day.
	for upstream, end := range map[string]string{"428802": "2026-11-12", "411605": "2026-11-20", "385753": "2027-06-01"} {
		if _, err := a.DB.Exec(`UPDATE platform_remote_accounts SET plan_end_time=? WHERE upstream_id=?`, end, upstream); err != nil {
			t.Fatal(err)
		}
	}
	edited := insightsOf(t, a, insightsAt, true).Composition
	if e := edited.Expiry; e["expired"] != 1 || e["within_7"] != 1 || e["later"] != 1 || e["within_30"] != 0 {
		t.Fatalf("expiry after edit=%v", e)
	}
	// The expired plan is skipped: the earliest end still ahead wins.
	if edited.NextExpiry != "2026-11-20" {
		t.Fatalf("next expiry after edit=%q", edited.NextExpiry)
	}

	// The endpoint wraps the same data.
	var env struct {
		Data insightsReply `json:"data"`
	}
	res := admin.do(http.MethodGet, "/api/v1/dashboard/insights", "")
	if err := jsonDecode(res, &env); err != nil || res.StatusCode != 200 || env.Data.Fleet.Accounts != 3 || len(env.Data.Hourly) != 24 || env.Data.RecentJobs == nil {
		t.Fatalf("endpoint: status=%d fleet=%+v hourly=%d err=%v", res.StatusCode, env.Data.Fleet, len(env.Data.Hourly), err)
	}
}

func TestDashboardInsightsHourlyBurn(t *testing.T) {
	a, _, _ := routesFixture(t)
	if _, err := a.DB.Exec(`DELETE FROM platform_usage_snapshots`); err != nil {
		t.Fatal(err)
	}
	snap := func(upstream string, at time.Time, micro int64) {
		t.Helper()
		if _, err := a.DB.Exec(`INSERT INTO platform_usage_snapshots(id,connection_id,upstream_id,account_id,observed_at,usage_cost_micro) VALUES(?,?,?,?,?,?)`, newID(), "conn", upstream, "", at.UTC().Format(time.RFC3339), micro); err != nil {
			t.Fatal(err)
		}
	}
	at := insightsAt                                          // 04:30
	snap("X", at.Add(-30*time.Hour), 1_000_000)               // baseline before the window
	snap("X", at.Add(-3*time.Hour-10*time.Minute), 3_000_000) // 01:20  +¥2.00
	snap("X", at.Add(-65*time.Minute), 3_500_000)             // 03:25  +¥0.50
	snap("Y", at.Add(-2*time.Hour), 9_000_000)                // first ever: no increment known
	snap("Y", at.Add(-80*time.Minute), 9_250_000)             // 03:10  +¥0.25
	snap("Y", at.Add(-30*time.Minute), 100)                   // counter reset: ignored
	snap("Y", at.Add(-10*time.Minute), 1_000_100)             // 04:20  +¥1.00 after the reset
	out := insightsOf(t, a, at, false)
	if len(out.Hourly) != 24 || out.Hourly[23].Hour != "2026-11-13T04:00:00Z" || out.Hourly[0].Hour != "2026-11-12T05:00:00Z" {
		t.Fatalf("hours: n=%d first=%v last=%v", len(out.Hourly), out.Hourly[0], out.Hourly[len(out.Hourly)-1])
	}
	got := map[string]int64{}
	var total int64
	for _, h := range out.Hourly {
		if h.CostFen != 0 {
			got[h.Hour[11:13]] = h.CostFen
		}
		total += h.CostFen
	}
	if got["01"] != 200 || got["03"] != 75 || got["04"] != 100 || total != 375 || len(got) != 3 {
		t.Fatalf("hourly=%v total=%d", got, total)
	}
	if out.RecentJobs != nil {
		t.Fatalf("recent jobs must be withheld without jobs.view: %v", out.RecentJobs)
	}
}

func TestDashboardInsightsRecentJobs(t *testing.T) {
	a, _, _ := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	for i, j := range []struct{ state, result string }{
		{"succeeded", `{"code":"ACCEPTED_PENDING_VERIFICATION"}`},
		{"failed", `{"code":"PLATFORM_AUTH_FAILED","message":"secret detail must not leak"}`},
	} {
		stamp := time.Date(2026, 11, 13, 1, i, 0, 0, time.UTC).Format(time.RFC3339)
		if _, err := a.DB.Exec(`INSERT INTO jobs(id,kind,state,account_id,connection_id,payload_json,result_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, newID(), "account.submit", j.state, id, "conn", `{}`, j.result, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	jobs := insightsOf(t, a, insightsAt, true).RecentJobs
	if len(jobs) != 2 || jobs[0].State != "failed" || jobs[0].Code != "PLATFORM_AUTH_FAILED" || jobs[0].Account != "one@example.test" || jobs[0].Connection != "Fixture" || jobs[1].State != "succeeded" || jobs[0].AccountID != id {
		t.Fatalf("recent jobs=%+v", jobs)
	}
	raw, _ := json.Marshal(jobs)
	if string(raw) == "" || json.Valid(raw) && containsString(string(raw), "secret detail") {
		t.Fatalf("job message leaked: %s", raw)
	}
}

func containsString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestDashboardInsightsWithoutWebChannel(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	linkFixtureAccount(t, a)
	out := insightsOf(t, a, insightsAt, true)
	if out.Fleet.Accounts != 0 || out.Fleet.CostFenTotal != 0 || out.Quota == nil || len(out.Quota) != 0 || len(out.Hourly) != 24 {
		t.Fatalf("fleet=%+v quota=%v hourly=%d", out.Fleet, out.Quota, len(out.Hourly))
	}
	if d := out.Composition.ByDelivery; d["linked"] != 1 {
		t.Fatalf("by_delivery=%v", d)
	}
	// No platform record and no local end date: the expiry is unknown.
	if e := out.Composition.Expiry; e["unknown"] != 1 {
		t.Fatalf("expiry=%v", e)
	}
	// Connections that disagree on the rate: show RMB only.
	if _, err := a.DB.Exec(`INSERT INTO platform_connections(id,name,base_url,token_secret,timezone,enabled,version,usd_cny_rate) VALUES('other','Other','https://o.example','x','Asia/Shanghai',1,1,7.3)`); err != nil {
		t.Fatal(err)
	}
	if rate := insightsOf(t, a, insightsAt, true).USDRate; rate != nil {
		t.Fatalf("mixed rates must give null, got %v", *rate)
	}
}
