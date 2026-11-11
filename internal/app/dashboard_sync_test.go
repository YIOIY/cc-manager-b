package app

import (
	"net/http"
	"testing"
	"time"

	"cc-manager/internal/errs"
)

type dashPlatform struct {
	Data struct {
		Platform struct {
			LastSyncAt          string `json:"last_sync_at"`
			LastSyncState       string `json:"last_sync_state"`
			SyncStatus          string `json:"sync_status"`
			ErrorCode           string `json:"error_code"`
			ErrorMessage        string `json:"error_message"`
			ErrorHint           string `json:"error_hint"`
			ConsecutiveFailures int    `json:"consecutive_failures"`
			NextAttemptAt       string `json:"next_attempt_at"`
		} `json:"platform"`
	} `json:"data"`
}

func dashboardPlatform(t *testing.T, admin *testClient) dashPlatform {
	t.Helper()
	var d dashPlatform
	if res := admin.do(http.MethodGet, "/api/v1/dashboard", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("dashboard=%d", res.StatusCode)
	} else {
		_ = jsonDecode(res, &d)
	}
	return d
}

func seedConnection(t *testing.T, a *App) {
	t.Helper()
	old := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := a.DB.Exec(`INSERT INTO platform_connections(id,name,base_url,token_secret,timezone,enabled,version,created_at,updated_at) VALUES('conn','tx','https://s.example','x','Asia/Shanghai',1,1,?,?)`, old, old); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardReportsFailingMetricsSync(t *testing.T) {
	a, admin := rbacApp(t)
	seedConnection(t, a)
	if d := dashboardPlatform(t, admin); d.Data.Platform.SyncStatus != "never" {
		t.Fatalf("empty status=%q", d.Data.Platform.SyncStatus)
	}
	// two failures, the latest one minute ago: the 4-minute backoff is still running
	for i, at := range []time.Time{time.Now().Add(-10 * time.Minute), time.Now().Add(-1 * time.Minute)} {
		ts := at.UTC().Format(time.RFC3339)
		if _, err := a.DB.Exec(`INSERT INTO sync_runs(id,connection_id,kind,state,started_at,finished_at,created_at,updated_at,error_code,error_message) VALUES(?,?,?,?,?,?,?,?,?,?)`, newID(), "conn", "platform-usage", "partial", ts, ts, ts, ts, "PLATFORM_QUERY_DENIED", "supplier metrics HTTP 404"); err != nil {
			t.Fatal(i, err)
		}
	}
	// a newer successful bill run must not mask the failing metrics sync
	nowTS := time.Now().UTC().Format(time.RFC3339)
	if _, err := a.DB.Exec(`INSERT INTO sync_runs(id,connection_id,kind,state,started_at,finished_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, newID(), "conn", "bill", "succeeded", nowTS, nowTS, nowTS, nowTS); err != nil {
		t.Fatal(err)
	}
	p := dashboardPlatform(t, admin).Data.Platform
	def, _ := errs.Lookup("PLATFORM_QUERY_DENIED")
	if p.SyncStatus != "failing" || p.LastSyncState != "partial" || p.ErrorCode != "PLATFORM_QUERY_DENIED" || p.ErrorMessage != def.Message || p.ErrorHint != def.Hint || p.ConsecutiveFailures != 2 || p.NextAttemptAt == "" {
		t.Fatalf("platform=%+v", p)
	}
}

func TestDashboardFallsBackForLegacyRuns(t *testing.T) {
	a, admin := rbacApp(t)
	seedConnection(t, a)
	for code, msg := range map[string]string{
		"PLATFORM_QUERY_DENIED": "supplier metrics HTTP 404: 账号不存在或无权访问",
		"PLATFORM_UNAVAILABLE":  `Post "https://x": proxyconnect tcp: dial tcp 127.0.0.1:7897: connect: connection refused`,
	} {
		if _, err := a.DB.Exec(`DELETE FROM sync_runs`); err != nil {
			t.Fatal(err)
		}
		ts := time.Now().UTC().Format(time.RFC3339)
		if _, err := a.DB.Exec(`INSERT INTO sync_runs(id,connection_id,kind,state,started_at,finished_at,created_at,updated_at,error_message) VALUES(?,?,?,?,?,?,?,?,?)`, newID(), "conn", "platform-usage", "partial", ts, ts, ts, ts, msg); err != nil {
			t.Fatal(err)
		}
		if p := dashboardPlatform(t, admin).Data.Platform; p.ErrorCode != code {
			t.Fatalf("legacy %q → %q, want %s", msg, p.ErrorCode, code)
		}
	}
}

func insertRun(t *testing.T, a *App, conn, state, code string, at time.Time) {
	t.Helper()
	ts := at.UTC().Format(time.RFC3339)
	if _, err := a.DB.Exec(`INSERT INTO sync_runs(id,connection_id,kind,state,started_at,finished_at,created_at,updated_at,error_code,error_message) VALUES(?,?,?,?,?,?,?,?,?,?)`, newID(), conn, "platform-usage", state, ts, ts, ts, ts, code, ""); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardPrefersFailingConnection(t *testing.T) {
	a, admin := rbacApp(t)
	seedConnection(t, a)
	old := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := a.DB.Exec(`INSERT INTO platform_connections(id,name,base_url,token_secret,timezone,enabled,version,created_at,updated_at) VALUES('conn-b','b','https://b.example','x','Asia/Shanghai',1,1,?,?)`, old, old); err != nil {
		t.Fatal(err)
	}
	insertRun(t, a, "conn", "partial", "PLATFORM_QUERY_DENIED", time.Now().Add(-3*time.Minute))
	insertRun(t, a, "conn", "partial", "PLATFORM_QUERY_DENIED", time.Now().Add(-2*time.Minute))
	insertRun(t, a, "conn-b", "succeeded", "", time.Now())
	p := dashboardPlatform(t, admin).Data.Platform
	if p.SyncStatus != "failing" || p.ConsecutiveFailures != 2 || p.ErrorCode != "PLATFORM_QUERY_DENIED" {
		t.Fatalf("a healthy connection hid a failing one: %+v", p)
	}
}

func TestDashboardSurvivesDeletedConnection(t *testing.T) {
	a, admin := rbacApp(t)
	insertRun(t, a, "deleted-conn", "failed", "CONNECTION_INVALID", time.Now())
	p := dashboardPlatform(t, admin).Data.Platform
	if p.SyncStatus != "never" {
		t.Fatalf("runs of a deleted connection must be ignored: %+v", p)
	}
}

func TestDashboardOmitsRetryTimeOnceDue(t *testing.T) {
	a, admin := rbacApp(t)
	seedConnection(t, a)
	insertRun(t, a, "conn", "partial", "PLATFORM_QUERY_DENIED", time.Now().Add(-30*time.Minute))
	p := dashboardPlatform(t, admin).Data.Platform
	if p.SyncStatus != "failing" || p.NextAttemptAt != "" {
		t.Fatalf("a due retry must not show a past time: %+v", p)
	}
}
