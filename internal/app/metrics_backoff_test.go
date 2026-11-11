package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMetricsBackoffDelay(t *testing.T) {
	for n, want := range map[int]time.Duration{0: 0, 1: 2 * time.Minute, 2: 4 * time.Minute, 3: 8 * time.Minute, 5: 32 * time.Minute, 6: 60 * time.Minute, 20: 60 * time.Minute} {
		if got := metricsBackoffDelay(n); got != want {
			t.Errorf("delay(%d)=%v want %v", n, got, want)
		}
	}
}

func insertMetricRun(t *testing.T, a *App, conn, state string, at time.Time) {
	t.Helper()
	ts := at.UTC().Format(time.RFC3339)
	if _, err := a.DB.Exec(`INSERT INTO sync_runs(id,connection_id,kind,state,started_at,finished_at,created_at,updated_at,error_message) VALUES(?,?,?,?,?,?,?,?,?)`, newID(), conn, "platform-usage", state, ts, ts, ts, ts, "supplier metrics HTTP 404: 账号不存在或无权访问"); err != nil {
		t.Fatal(err)
	}
}

func TestMetricsBackoffCountsSinceLastSuccessAndConnectionEdit(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	now := time.Now()
	if _, err := a.DB.Exec(`UPDATE platform_connections SET updated_at=? WHERE id='conn'`, now.Add(-time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	insertMetricRun(t, a, "conn", "succeeded", now.Add(-30*time.Minute))
	insertMetricRun(t, a, "conn", "partial", now.Add(-10*time.Minute))
	insertMetricRun(t, a, "conn", "partial", now.Add(-5*time.Minute))
	last := now.Add(-1 * time.Minute)
	insertMetricRun(t, a, "conn", "partial", last)
	n, next, skip, err := a.metricsBackoff(context.Background(), "conn", now)
	if err != nil || n != 3 || !skip {
		t.Fatalf("n=%d skip=%v err=%v", n, skip, err)
	}
	if want := last.Add(8 * time.Minute).UTC().Truncate(time.Second); !next.Equal(want) {
		t.Fatalf("next=%v want %v", next, want)
	}
	// once the delay has passed the connection is due again
	if _, _, skip, _ = a.metricsBackoff(context.Background(), "conn", last.Add(9*time.Minute)); skip {
		t.Fatal("should be due after the delay")
	}
	// editing the connection resets the streak immediately
	if _, err := a.DB.Exec(`UPDATE platform_connections SET updated_at=? WHERE id='conn'`, now.UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if n, _, skip, _ = a.metricsBackoff(context.Background(), "conn", now.Add(time.Second)); n != 0 || skip {
		t.Fatalf("after edit n=%d skip=%v", n, skip)
	}
}

func linkFixtureAccount(t *testing.T, a *App) {
	t.Helper()
	stamp := now()
	if _, err := a.DB.Exec(`INSERT INTO accounts(id,account,plan,created_at,updated_at) VALUES('acc-m','m@example.test','claude/cc-pro',?,?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO platform_links(id,account_id,connection_id,upstream_id,ingestion_state,created_at,updated_at) VALUES('l-m','acc-m','conn','385753','accepted',?,?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func countMetricRuns(t *testing.T, a *App) int {
	t.Helper()
	var n int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM sync_runs WHERE kind='platform-usage'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSyncSkipsConnectionInBackoffWithoutWritingRuns(t *testing.T) {
	a, _, reads, _ := deliveryFixture(t)
	linkFixtureAccount(t, a)
	old := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := a.DB.Exec(`UPDATE platform_connections SET updated_at=? WHERE id='conn'`, old); err != nil {
		t.Fatal(err)
	}
	for i := 5; i >= 1; i-- {
		insertMetricRun(t, a, "conn", "partial", time.Now().Add(-time.Duration(i)*time.Second))
	}
	before := countMetricRuns(t, a)
	if err := a.syncAllPlatformUsage(context.Background()); err != nil {
		t.Fatalf("a skipped round must not report an error (it would be logged as WARN): %v", err)
	}
	if after := countMetricRuns(t, a); after != before {
		t.Fatalf("backoff wrote a run: %d → %d", before, after)
	}
	if reads.Load() != 0 {
		t.Fatalf("backoff still called the supplier %d times", reads.Load())
	}
}

func TestMetricsRunStoresErrorCode(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	linkFixtureAccount(t, a)
	_ = a.syncAllPlatformUsage(context.Background())
	var code, state string
	if err := a.DB.QueryRow(`SELECT error_code,state FROM sync_runs WHERE kind='platform-usage' ORDER BY started_at DESC LIMIT 1`).Scan(&code, &state); err != nil {
		t.Fatal(err)
	}
	if state != "partial" || code != "PLATFORM_QUERY_DENIED" {
		t.Fatalf("state=%s code=%q", state, code)
	}
}

func TestMetricsBackoffCountIsNotCapped(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	if _, err := a.DB.Exec(`UPDATE platform_connections SET updated_at='' WHERE id='conn'`); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-5 * time.Hour)
	for i := 0; i < 100; i++ {
		insertMetricRun(t, a, "conn", "partial", base.Add(time.Duration(i)*time.Minute))
	}
	n, _, _, err := a.metricsBackoff(context.Background(), "conn", time.Now())
	if err != nil || n != 100 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestMetricsBackoffToleratesMissingConnection(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	n, next, skip, err := a.metricsBackoff(context.Background(), "gone", time.Now())
	if err != nil || n != 0 || skip || !next.IsZero() {
		t.Fatalf("n=%d skip=%v err=%v", n, skip, err)
	}
}

func TestLoadConfigRejectsInvalidSupplierProxy(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "config-test-password")
	t.Setenv("CC_MANAGER_SUPPLIER_PROXY", "ftp://10.0.0.9:21")
	_, err := LoadConfig()
	if err == nil || !strings.Contains(err.Error(), "CC_MANAGER_SUPPLIER_PROXY") {
		t.Fatalf("err=%v", err)
	}
	t.Setenv("CC_MANAGER_SUPPLIER_PROXY", "socks5h://127.0.0.1:1080")
	if _, err = LoadConfig(); err != nil {
		t.Fatalf("valid proxy rejected: %v", err)
	}
}
