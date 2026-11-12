package app

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func attentionKinds(items []attentionItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Kind+":"+it.Account)
	}
	return out
}

func TestDashboardAttentionOrdersAndCounts(t *testing.T) {
	a, stub, admin := routesFixture(t)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	in10 := time.Now().In(loc).AddDate(0, 0, 10).Format("2006-01-02")
	// The fixture profile ends 2026-12-10, inside the 30-day window; push
	// every account that is not meant to expire far out.
	far := map[string]any{"plan_end_time": "2027-03-01"}
	insertLocalAccount(t, a, "acc-diff", "diff@example.test")
	stub.setAccounts(
		remoteAccount(428802, "one@example.test", "using", "2026-11-11 19:36", fixtureUsage(35209959, 0.05), far),
		remoteAccount(411605, "hot@example.test", "using", "2026-11-10 00:14", fixtureUsage(4132689422, 0.84), far),
		remoteAccount(385753, "banned@example.test", "ban", "2026-11-05 01:57", nil, far),
		remoteAccount(500001, "crit@example.test", "using", "2026-11-09 00:00", fixtureUsage(1, 0.97), far),
		remoteAccount(500002, "exp@example.test", "using", "2026-11-09 00:00", nil, map[string]any{"plan_end_time": in10}),
		remoteAccount(500003, "gone@example.test", "using", "2026-11-09 00:00", nil, far),
		remoteAccount(500004, "diff@example.test", "using", "2026-11-09 00:00", nil, map[string]any{"plan_end_time": "2027-01-01"}),
	)
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	// gone disappears from the platform.
	stub.setAccounts(
		remoteAccount(428802, "one@example.test", "using", "2026-11-11 19:36", fixtureUsage(35209959, 0.05), far),
		remoteAccount(411605, "hot@example.test", "using", "2026-11-10 00:14", fixtureUsage(4132689422, 0.84), far),
		remoteAccount(385753, "banned@example.test", "ban", "2026-11-05 01:57", nil, far),
		remoteAccount(500001, "crit@example.test", "using", "2026-11-09 00:00", fixtureUsage(1, 0.97), far),
		remoteAccount(500002, "exp@example.test", "using", "2026-11-09 00:00", nil, map[string]any{"plan_end_time": in10}),
		remoteAccount(500004, "diff@example.test", "using", "2026-11-09 00:00", nil, map[string]any{"plan_end_time": "2027-01-01"}),
	)
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	// routesFixture created these locals with the 2026-12-10 fixture date;
	// align them with the far remote date so only "diff" differs.
	if _, err := a.DB.Exec(`UPDATE accounts SET plan_end_time='2027-03-01' WHERE account IN ('one@example.test','hot@example.test','banned@example.test','crit@example.test','gone@example.test')`); err != nil {
		t.Fatal(err)
	}
	// A never-onboarded local draft expiring tomorrow must not appear.
	if _, err := a.DB.Exec(`INSERT INTO accounts(id,account,plan,plan_end_time,created_at,updated_at) VALUES('draft','draft@example.test','claude/cc-pro',?,?,?)`, time.Now().AddDate(0, 0, 1).Format("2006-01-02"), now(), now()); err != nil {
		t.Fatal(err)
	}
	items, counts, err := a.attentionItems(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := "[near_limit:crit@example.test near_limit:hot@example.test banned:banned@example.test expiring:exp@example.test missing:gone@example.test profile_diff:diff@example.test]"
	if got := fmt.Sprint(attentionKinds(items)); got != want {
		t.Fatalf("order\n got %s\nwant %s", got, want)
	}
	if items[0].Severity != "danger" || items[0].Value != 97 || items[1].Severity != "warn" || items[1].ResetAt != "2026-11-11T16:50:00Z" {
		t.Fatalf("near_limit items=%+v", items[:2])
	}
	if items[2].Severity != "danger" || items[2].Detail == "" || items[3].Value != 10 || items[3].ExpiresAt != in10 || items[3].Severity != "warn" {
		t.Fatalf("banned/expiring items=%+v", items[2:4])
	}
	if items[5].Detail != "plan_end_time" {
		t.Fatalf("profile_diff detail=%q", items[5].Detail)
	}
	if counts["near_limit"] != 2 || counts["banned"] != 1 || counts["expiring"] != 1 || counts["missing"] != 1 || counts["profile_diff"] != 1 {
		t.Fatalf("counts=%v", counts)
	}
	// Already expired accounts come first among expiring with a negative value.
	if _, err := a.DB.Exec(`UPDATE platform_remote_accounts SET plan_end_time=? WHERE upstream_id='428802'`, time.Now().In(loc).AddDate(0, 0, -3).Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
	items, _, _ = a.attentionItems(context.Background(), time.Now())
	if items[3].Account != "one@example.test" || items[3].Value != -3 || items[3].Severity != "danger" {
		t.Fatalf("expired item=%+v", items[3])
	}
	// The list is capped at 8 while counts stay complete.
	var many []map[string]any
	for i := 1; i <= 10; i++ {
		many = append(many, remoteAccount(600000+i, fmt.Sprintf("n%d@example.test", i), "using", "2026-11-09 00:00", fixtureUsage(1, 0.9), far))
	}
	stub.setAccounts(append(many, remoteAccount(428802, "one@example.test", "using", "2026-11-11 19:36", fixtureUsage(35209959, 0.05), far))...)
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Data struct {
			Items  []attentionItem `json:"items"`
			Counts map[string]int  `json:"counts"`
		} `json:"data"`
	}
	res := admin.do(http.MethodGet, "/api/v1/dashboard/attention", "")
	_ = jsonDecode(res, &out)
	if res.StatusCode != 200 || len(out.Data.Items) != 8 || out.Data.Counts["near_limit"] != 10 {
		t.Fatalf("status=%d items=%d counts=%v", res.StatusCode, len(out.Data.Items), out.Data.Counts)
	}
}

func TestDashboardAttentionWithoutWebChannel(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	linkFixtureAccount(t, a)
	items, counts, err := a.attentionItems(context.Background(), time.Now())
	if err != nil || len(items) != 0 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	for k, v := range counts {
		if v != 0 {
			t.Fatalf("count %s=%d", k, v)
		}
	}
	// 20:00 UTC is already the next day in the connection's Asia/Shanghai
	// zone: 3 days there, 4 in UTC. The fixed instant keeps the test honest
	// on any runner's clock.
	at := time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC)
	if _, err := a.DB.Exec(`UPDATE accounts SET plan_end_time='2026-11-16' WHERE id='acc-m'`); err != nil {
		t.Fatal(err)
	}
	items, counts, err = a.attentionItems(context.Background(), at)
	if err != nil || len(items) != 1 || items[0].Kind != "expiring" || items[0].Value != 3 || counts["expiring"] != 1 {
		t.Fatalf("local fallback items=%+v counts=%v err=%v", items, counts, err)
	}
}
