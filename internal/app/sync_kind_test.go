package app

import (
	"context"
	"testing"
	"time"
)

func insertKindRun(t *testing.T, a *App, conn, kind, state string, at time.Time) {
	t.Helper()
	ts := at.UTC().Format(time.RFC3339)
	if _, err := a.DB.Exec(`INSERT INTO sync_runs(id,connection_id,kind,state,started_at,finished_at,created_at,updated_at,error_code,error_message) VALUES(?,?,?,?,?,?,?,?,?,?)`, newID(), conn, kind, state, ts, ts, ts, ts, "WEB_LOGIN_FAILED", "supplier web HTTP 401"); err != nil {
		t.Fatal(err)
	}
}

func TestSyncBackoffIsPerKind(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	now := time.Now()
	if _, err := a.DB.Exec(`UPDATE platform_connections SET updated_at=? WHERE id='conn'`, now.Add(-time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	for i := 3; i >= 1; i-- {
		insertKindRun(t, a, "conn", "web-accounts", "failed", now.Add(-time.Duration(i)*time.Minute))
	}
	n, _, skip, err := a.syncBackoff(context.Background(), "conn", "web-accounts", now)
	if err != nil || n != 3 || !skip {
		t.Fatalf("web-accounts: n=%d skip=%v err=%v", n, skip, err)
	}
	n, _, skip, err = a.syncBackoff(context.Background(), "conn", "platform-usage", now)
	if err != nil || n != 0 || skip {
		t.Fatalf("platform-usage must not see web runs: n=%d skip=%v err=%v", n, skip, err)
	}
	n, _, skip, err = a.syncBackoff(context.Background(), "conn", "web-bills", now)
	if err != nil || n != 0 || skip {
		t.Fatalf("web-bills must not see web-accounts runs: n=%d skip=%v err=%v", n, skip, err)
	}
}

func TestSyncHealthPerKind(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	now := time.Now()
	for i := 3; i >= 1; i-- {
		insertKindRun(t, a, "conn", "web-accounts", "failed", now.Add(-time.Duration(i)*time.Minute))
	}
	web, err := a.syncHealth(context.Background(), "web-accounts")
	if err != nil || web["sync_status"] != "failing" || web["error_code"] != "WEB_LOGIN_FAILED" || web["consecutive_failures"] != 3 {
		t.Fatalf("web health=%v err=%v", web, err)
	}
	if web["error_message"] == "" || web["next_attempt_at"] == "" {
		t.Fatalf("web health lacks message or retry time: %v", web)
	}
	metrics, err := a.metricsSyncHealth(context.Background())
	if err != nil || metrics["sync_status"] != "never" {
		t.Fatalf("metrics health=%v err=%v", metrics, err)
	}
}

func TestCleanupPrunesUsageSnapshotsAndWebRuns(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	old := time.Now().Add(-91 * 24 * time.Hour).UTC().Format(time.RFC3339)
	fresh := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	for _, at := range []string{old, fresh} {
		if _, err := a.DB.Exec(`INSERT INTO platform_usage_snapshots(id,connection_id,upstream_id,account_id,observed_at) VALUES(?,'conn','1','acc',?)`, newID(), at); err != nil {
			t.Fatal(err)
		}
	}
	insertKindRun(t, a, "conn", "web-bills", "failed", time.Now().Add(-8*24*time.Hour))
	insertKindRun(t, a, "conn", "web-bills", "failed", time.Now().Add(-time.Hour))
	if err := a.cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	var snapshots, runs int
	if err := a.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM platform_usage_snapshots),(SELECT COUNT(*) FROM sync_runs WHERE kind='web-bills')`).Scan(&snapshots, &runs); err != nil {
		t.Fatal(err)
	}
	if snapshots != 1 || runs != 1 {
		t.Fatalf("snapshots=%d web runs=%d after cleanup", snapshots, runs)
	}
}
