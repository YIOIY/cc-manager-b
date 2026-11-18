package app

import (
	"net/http"
	"testing"
)

func TestAccountListPrefersPlatformAidOverDeliveryID(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "correct-horse-battery")
	a, err := newApp(t.TempDir()+"/platform-id.db", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	stamp := now()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO accounts(id,account,plan,created_at,updated_at) VALUES('acc','sync@example.test','claude/cc-pro',?,?)`, []any{stamp, stamp}},
		{`INSERT INTO platform_connections(id,name,base_url,timezone,enabled,created_at,updated_at) VALUES('conn','c','https://example.com','Asia/Shanghai',1,?,?)`, []any{stamp, stamp}},
		// Web sync already recorded the real platform account id on the link.
		{`INSERT INTO platform_links(id,account_id,connection_id,upstream_id,upstream_aid,runtime_status,ingestion_state,created_at,updated_at) VALUES('l1','acc','conn','428802','20204','sync_failed','accepted',?,?)`, []any{stamp, stamp}},
		// OpenAPI metrics kept the delivery id and a zero/empty aid (common for sync_failed).
		{`INSERT INTO platform_metric_snapshots(id,account_id,connection_id,upstream_id,upstream_aid,runtime_status,bucket_key,window_start,window_end,fetched_at) VALUES('m1','acc','conn','428802','0','sync_failed','2026-11-18',?,?,?)`, []any{stamp, stamp, stamp}},
	} {
		if _, err := a.DB.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("%s: %v", stmt.sql, err)
		}
	}
	c := newTestClient(t, a)
	c.login("correct-horse-battery")
	var out struct{ Data []map[string]any }
	_ = jsonDecode(c.do(http.MethodGet, "/api/v1/accounts", ""), &out)
	if len(out.Data) != 1 {
		t.Fatalf("items=%d", len(out.Data))
	}
	if out.Data[0]["platform_account_id"] != "20204" {
		t.Fatalf("platform_account_id=%v, want platform aid 20204 not delivery id 428802", out.Data[0]["platform_account_id"])
	}
}
