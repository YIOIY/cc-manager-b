package app

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestJobsAndAuditSupportServerSidePaging(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "correct-horse-battery")
	a, err := newApp(t.TempDir()+"/paging.db", false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for i := 0; i < 25; i++ {
		state := "failed"
		if i%5 == 0 {
			state = "succeeded"
		}
		if _, err := a.DB.Exec(`INSERT INTO jobs(id,kind,state,account_id,connection_id,result_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, "job-"+string(rune('a'+i)), "account.submit", state, "acc-"+string(rune('a'+i%3)), "conn", `{"code":"X"}`, "2026-11-0"+string(rune('1'+i%9))+"T00:00:00Z", now()); err != nil {
			t.Fatal(err)
		}
	}
	c := newTestClient(t, a)
	c.login("correct-horse-battery")
	var legacy struct{ Data []map[string]any }
	_ = jsonDecode(c.do(http.MethodGet, "/api/v1/jobs", ""), &legacy)
	if len(legacy.Data) != 25 {
		t.Fatalf("legacy array length=%d", len(legacy.Data))
	}
	var paged struct {
		Data struct {
			Items      []map[string]any `json:"items"`
			Pagination struct{ Page, PageSize, Total int }
		}
	}
	_ = jsonDecode(c.do(http.MethodGet, "/api/v1/jobs?page=2&page_size=10&state=failed", ""), &paged)
	if paged.Data.Pagination.Total != 20 || len(paged.Data.Items) != 10 || paged.Data.Pagination.Page != 2 {
		t.Fatalf("paged=%+v", paged.Data.Pagination)
	}
	_ = jsonDecode(c.do(http.MethodGet, "/api/v1/jobs?page=1&page_size=10&q=acc-a+failed&created_to=2026-11-03", ""), &paged)
	for _, item := range paged.Data.Items {
		if item["account_id"] != "acc-a" || item["state"] != "failed" || item["created_at"].(string) > "2026-11-03T99" {
			t.Fatalf("filter leaked row %v", item)
		}
	}
	if paged.Data.Pagination.Total == 0 {
		t.Fatal("keyword filter returned nothing")
	}
	if res := c.do(http.MethodGet, "/api/v1/jobs?page=0", ""); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid page status=%d", res.StatusCode)
	}
	if res := c.do(http.MethodGet, "/api/v1/jobs?page_size=7", ""); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid page size status=%d", res.StatusCode)
	}
	var audit struct {
		Data struct {
			Items      []map[string]any `json:"items"`
			Pagination struct{ Total int }
		}
	}
	_ = jsonDecode(c.do(http.MethodGet, "/api/v1/audit-events?page=1&page_size=10&action=auth.login", ""), &audit)
	if audit.Data.Pagination.Total != 1 || audit.Data.Items[0]["action"] != "auth.login" {
		raw, _ := json.Marshal(audit)
		t.Fatalf("audit paging: %s", raw)
	}
}

func TestAccountsSupportServerSidePagingAndFilters(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "correct-horse-battery")
	a, err := newApp(t.TempDir()+"/accounts-paging.db", false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for i := 0; i < 23; i++ {
		id := "acc-" + string(rune('a'+i))
		proxy, archived, plan := "", 0, "claude/cc-pro"
		if i%2 == 0 {
			proxy = "proxy-1"
		}
		if i%4 == 0 {
			archived = 1
		}
		if i%3 == 0 {
			plan = "claude/cc-max-5x"
		}
		if _, err := a.DB.Exec(`INSERT INTO accounts(id,account,plan,register_location,plan_end_time,proxy_id,archived,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, "user"+string(rune('a'+i))+"@example.com", plan, "US", "2026-12-"+string(rune('0'+i%2))+"5", proxy, archived, "2026-11-0"+string(rune('1'+i%9))+"T00:00:00Z", now()); err != nil {
			t.Fatal(err)
		}
	}
	// Derived onboarding states: a linked account, a failed one, a queued one and an unknown one.
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO platform_connections(id,name,base_url,timezone,enabled,created_at,updated_at) VALUES('conn','c','https://example.com','Asia/Shanghai',1,?,?)`, []any{now(), now()}},
		{`INSERT INTO platform_links(id,account_id,connection_id,upstream_id,ingestion_state,created_at,updated_at) VALUES('l1','acc-a','conn','up-1','accepted',?,?)`, []any{now(), now()}},
		{`INSERT INTO jobs(id,kind,state,account_id,connection_id,result_json,created_at,updated_at) VALUES('j1','account.submit','failed','acc-b','conn','{"code":"X"}',?,?)`, []any{now(), now()}},
		{`INSERT INTO jobs(id,kind,state,account_id,connection_id,result_json,created_at,updated_at) VALUES('j2','account.submit','queued','acc-c','conn','{}',?,?)`, []any{now(), now()}},
		{`INSERT INTO deliveries(id,account_id,connection_id,ingestion_state,created_at,updated_at) VALUES('d1','acc-d','conn','unknown',?,?)`, []any{now(), now()}},
	} {
		if _, err := a.DB.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("%s: %v", stmt.sql, err)
		}
	}
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,name,scheme,host,port,enabled,created_at,updated_at) VALUES('proxy-1','exit','http','203.0.113.10',1080,1,?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	c := newTestClient(t, a)
	c.login("correct-horse-battery")
	const allTotal = 23
	const normalTotal = 21 // excludes failed/unknown onboarding acc-b/acc-d
	var legacy struct{ Data []map[string]any }
	_ = jsonDecode(c.do(http.MethodGet, "/api/v1/accounts", ""), &legacy)
	if len(legacy.Data) != allTotal {
		t.Fatalf("legacy array length=%d want %d", len(legacy.Data), allTotal)
	}
	foundHost := false
	for _, row := range legacy.Data {
		if row["proxy_id"] == "proxy-1" {
			if row["proxy_host"] != "203.0.113.10" {
				t.Fatalf("proxy_host=%v for bound account", row["proxy_host"])
			}
			foundHost = true
		}
	}
	if !foundHost {
		t.Fatal("expected a bound account to expose proxy_host")
	}
	type paged struct {
		Data struct {
			Items      []map[string]any `json:"items"`
			Pagination struct{ Page, PageSize, Total int }
			TabCounts  map[string]int `json:"tab_counts"`
		}
	}
	var p paged
	_ = jsonDecode(c.do(http.MethodGet, "/api/v1/accounts?page=2&page_size=10", ""), &p)
	if p.Data.Pagination.Total != allTotal || len(p.Data.Items) != 10 || p.Data.Pagination.Page != 2 {
		t.Fatalf("paged=%+v items=%d", p.Data.Pagination, len(p.Data.Items))
	}
	_ = jsonDecode(c.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=100&abnormal=0", ""), &p)
	if p.Data.Pagination.Total != normalTotal {
		t.Fatalf("abnormal=0 total=%d want %d", p.Data.Pagination.Total, normalTotal)
	}
	if p.Data.TabCounts["all"] != normalTotal || p.Data.TabCounts["abnormal"] != allTotal-normalTotal {
		t.Fatalf("tab_counts=%v want all=%d abnormal=%d", p.Data.TabCounts, normalTotal, allTotal-normalTotal)
	}
	for _, item := range p.Data.Items {
		if id, _ := item["id"].(string); id == "acc-b" || id == "acc-d" {
			t.Fatalf("abnormal=0 leaked %s", id)
		}
	}
	cases := map[string]func(map[string]any) bool{
		"archived=true":                func(m map[string]any) bool { return m["archived"] == true },
		"archived=false":               func(m map[string]any) bool { return m["archived"] == false },
		"proxy=present":                func(m map[string]any) bool { return m["proxy_id"] == "proxy-1" },
		"proxy=absent":                 func(m map[string]any) bool { return m["proxy_id"] == "" },
		"plan=claude%2Fcc-max-5x":      func(m map[string]any) bool { return m["plan"] == "claude/cc-max-5x" },
		"end_from=2026-12-10":          func(m map[string]any) bool { return m["plan_end_time"].(string) >= "2026-12-10" },
		"created_to=2026-11-03":        func(m map[string]any) bool { return m["created_at"].(string) < "2026-11-04" },
		"q=usera":                      func(m map[string]any) bool { return m["account"] == "usera@example.com" },
		"onboarding_state=linked":      func(m map[string]any) bool { return m["id"] == "acc-a" && m["onboarding_state"] == "linked" },
		"onboarding_state=failed":      func(m map[string]any) bool { return m["id"] == "acc-b" && m["onboarding_state"] == "failed" },
		"onboarding_state=submitting":  func(m map[string]any) bool { return m["id"] == "acc-c" && m["onboarding_state"] == "submitting" },
		"onboarding_state=unknown":     func(m map[string]any) bool { return m["id"] == "acc-d" && m["onboarding_state"] == "unknown" },
		"onboarding_state=pending":     func(m map[string]any) bool { return m["onboarding_state"] == "pending" },
		"archived=false&proxy=present": func(m map[string]any) bool { return m["archived"] == false && m["proxy_id"] == "proxy-1" },
	}
	for query, want := range cases {
		var res paged
		_ = jsonDecode(c.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=100&"+query, ""), &res)
		if len(res.Data.Items) == 0 || res.Data.Pagination.Total != len(res.Data.Items) {
			t.Fatalf("%s: items=%d total=%d", query, len(res.Data.Items), res.Data.Pagination.Total)
		}
		for _, item := range res.Data.Items {
			if !want(item) {
				t.Fatalf("%s leaked row %v", query, item)
			}
		}
	}
	var abnormal paged
	_ = jsonDecode(c.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=100&abnormal=1", ""), &abnormal)
	if abnormal.Data.Pagination.Total != 2 {
		t.Fatalf("abnormal total=%d want 2", abnormal.Data.Pagination.Total)
	}
	for _, item := range abnormal.Data.Items {
		id, _ := item["id"].(string)
		if id != "acc-b" && id != "acc-d" {
			t.Fatalf("abnormal leaked %s", id)
		}
	}
	// Seed a linked account with a failure platform status and a missing remote row.
	stamp := now()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO platform_links(id,account_id,connection_id,upstream_id,runtime_status,last_observed_at,ingestion_state,created_at,updated_at) VALUES('l-fail','acc-e','conn','up-fail','login_failed',?,'accepted',?,?)`, []any{stamp, stamp, stamp}},
		{`INSERT INTO platform_remote_accounts(connection_id,upstream_id,account_id,account_email,ext_status,a1_status,missing_since,first_seen_at,last_seen_at) VALUES('conn','up-fail','acc-e','usere@example.com','login_failed','','',?,?)`, []any{stamp, stamp}},
		{`INSERT INTO platform_links(id,account_id,connection_id,upstream_id,runtime_status,last_observed_at,ingestion_state,created_at,updated_at) VALUES('l-miss','acc-f','conn','up-miss','using',?,'accepted',?,?)`, []any{stamp, stamp, stamp}},
		{`INSERT INTO platform_remote_accounts(connection_id,upstream_id,account_id,account_email,ext_status,a1_status,missing_since,first_seen_at,last_seen_at) VALUES('conn','up-miss','acc-f','userf@example.com','using','',?,?,?)`, []any{stamp, stamp, stamp}},
	} {
		if _, err := a.DB.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("%s: %v", stmt.sql, err)
		}
	}
	_ = jsonDecode(c.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=100&abnormal=1", ""), &abnormal)
	ids := map[string]bool{}
	for _, item := range abnormal.Data.Items {
		ids[item["id"].(string)] = true
	}
	for _, wantID := range []string{"acc-b", "acc-d", "acc-e", "acc-f"} {
		if !ids[wantID] {
			t.Fatalf("abnormal missing %s in %v", wantID, ids)
		}
	}
	_ = jsonDecode(c.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=100&abnormal=0&platform_status=login_failed", ""), &abnormal)
	if len(abnormal.Data.Items) != 0 {
		t.Fatalf("default mode should hide login_failed rows, got %d", len(abnormal.Data.Items))
	}
	_ = jsonDecode(c.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=100&abnormal=1&platform_status=login_failed", ""), &abnormal)
	if len(abnormal.Data.Items) != 1 || abnormal.Data.Items[0]["id"] != "acc-e" {
		t.Fatalf("platform_status=login_failed got %#v", abnormal.Data.Items)
	}
	// The SQL-derived state must agree with the Go decoration across both modes.
	want := map[string]string{"acc-a": "linked", "acc-b": "failed", "acc-c": "submitting", "acc-d": "unknown"}
	for _, mode := range []string{"abnormal=0", "abnormal=1"} {
		var all paged
		_ = jsonDecode(c.do(http.MethodGet, "/api/v1/accounts?page=1&page_size=100&"+mode, ""), &all)
		for _, item := range all.Data.Items {
			if expected, ok := want[item["id"].(string)]; ok && item["onboarding_state"] != expected {
				t.Fatalf("%s %s state=%v want %s", mode, item["id"], item["onboarding_state"], expected)
			}
		}
	}
	for _, bad := range []string{"archived=maybe", "proxy=x", "onboarding_state=nope", "abnormal=maybe"} {
		if res := c.do(http.MethodGet, "/api/v1/accounts?"+bad, ""); res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s status=%d", bad, res.StatusCode)
		}
	}
}
