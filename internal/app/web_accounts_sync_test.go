package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

func insertLocalAccount(t *testing.T, a *App, id, email string) {
	t.Helper()
	stamp := now()
	if _, err := a.DB.Exec(`INSERT INTO accounts(id,account,plan,plan_end_time,created_at,updated_at) VALUES(?,?,'claude/cc-max-20x','2026-12-31',?,?)`, id, email, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func queryInt(t *testing.T, a *App, query string, args ...any) int {
	t.Helper()
	var n int
	if err := a.DB.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func queryString(t *testing.T, a *App, query string, args ...any) string {
	t.Helper()
	var s string
	if err := a.DB.QueryRow(query, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return s
}

func lastRun(t *testing.T, a *App, kind string) (state, code string, processed, failed int) {
	t.Helper()
	if err := a.DB.QueryRow(`SELECT state,error_code,processed,failed FROM sync_runs WHERE kind=? ORDER BY started_at DESC,rowid DESC LIMIT 1`, kind).Scan(&state, &code, &processed, &failed); err != nil {
		t.Fatal(err)
	}
	return
}

func TestWebAccountsSyncAutoCreatesAndLinks(t *testing.T) {
	a, stub := webFixture(t)
	insertLocalAccount(t, a, "acc-a", "legacy@example.test")
	stub.setAccounts(
		remoteAccount(1001, "legacy@example.test", "using", "2026-11-01 10:00", fixtureUsage(35209959, 0.05), nil),
		remoteAccount(1002, "fresh@example.test", "using", "2026-11-02 10:00", fixtureUsage(100, 0.1), nil),
		remoteAccount(1003, "bare@example.test", "using", "2026-11-03 10:00", nil, nil),
	)
	stub.mu.Lock()
	bare := stub.accounts[2]
	bare["auth_config"] = ""
	stub.mu.Unlock()
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	state, code, processed, failed := lastRun(t, a, "web-accounts")
	if state != "succeeded" || code != "" || processed != 3 || failed != 0 {
		t.Fatalf("run state=%s code=%q processed=%d failed=%d", state, code, processed, failed)
	}
	// A: existing local account linked by email.
	if got := queryString(t, a, `SELECT upstream_id||'/'||COALESCE(upstream_aid,'')||'/'||COALESCE(alias,'')||'/'||COALESCE(runtime_status,'') FROM platform_links WHERE account_id='acc-a' AND connection_id='conn'`); got != "1001/11001/claude-fixture-1001/using" {
		t.Fatalf("link for A: %s", got)
	}
	// B: auto-created with profile data, no secrets.
	var bID, plan, metadata, credentials string
	if err := a.DB.QueryRow(`SELECT id,plan,metadata_json,credentials_json FROM accounts WHERE account='fresh@example.test'`).Scan(&bID, &plan, &metadata, &credentials); err != nil {
		t.Fatal(err)
	}
	if plan != "claude/cc-max-20x" || credentials != "{}" || !strings.Contains(metadata, `"source":"platform_sync"`) || !strings.Contains(metadata, `"balance":"100"`) || !strings.Contains(metadata, `"platform_upstream_id":"1002"`) {
		t.Fatalf("B plan=%q credentials=%q metadata=%s", plan, credentials, metadata)
	}
	if queryInt(t, a, `SELECT COUNT(*) FROM account_secrets WHERE account_id=?`, bID) != 0 {
		t.Fatal("auto-created account must not get a password secret")
	}
	if queryInt(t, a, `SELECT COUNT(*) FROM platform_links WHERE account_id=? AND upstream_id='1002' AND ingestion_state='accepted'`, bID) != 1 {
		t.Fatal("B not linked")
	}
	// C: no auth_config -> created with an empty plan.
	if got := queryString(t, a, `SELECT plan FROM accounts WHERE account='bare@example.test'`); got != "" {
		t.Fatalf("C plan=%q, want empty", got)
	}
	if got := queryInt(t, a, `SELECT SUM(auto_created) FROM platform_remote_accounts WHERE connection_id='conn'`); got != 2 {
		t.Fatalf("auto_created sum=%d", got)
	}
	if queryInt(t, a, `SELECT COUNT(*) FROM platform_remote_accounts WHERE connection_id='conn' AND account_id<>''`) != 3 {
		t.Fatal("remote rows not all linked to local accounts")
	}
	if queryInt(t, a, `SELECT COUNT(*) FROM audit_events WHERE action='account.platform_import'`) != 2 {
		t.Fatal("expected two import audit events")
	}
	// Usage landed on the remote row and as one snapshot per account with usage.
	if got := queryInt(t, a, `SELECT usage_cost_micro FROM platform_remote_accounts WHERE upstream_id='1001'`); got != 35209959 {
		t.Fatalf("usage cost=%d", got)
	}
	if queryInt(t, a, `SELECT COUNT(*) FROM platform_usage_snapshots`) != 2 {
		t.Fatal("expected one snapshot per account that reported usage")
	}
	// A second identical sync changes nothing.
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	if queryInt(t, a, `SELECT COUNT(*) FROM accounts`) != 3 || queryInt(t, a, `SELECT COUNT(*) FROM platform_usage_snapshots`) != 2 || queryInt(t, a, `SELECT COUNT(*) FROM audit_events WHERE action='account.platform_import'`) != 2 {
		t.Fatal("second sync duplicated accounts, snapshots or audit events")
	}
}

func TestWebAccountsSyncMatchesEmailCaseInsensitive(t *testing.T) {
	a, stub := webFixture(t)
	insertLocalAccount(t, a, "acc-mixed", "Mixed.Case@Example.test ")
	stub.setAccounts(remoteAccount(2001, "mixed.case@example.test", "using", "2026-11-01 10:00", nil, nil))
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	if queryInt(t, a, `SELECT COUNT(*) FROM accounts`) != 1 {
		t.Fatal("a differently-cased email created a duplicate account")
	}
	if queryInt(t, a, `SELECT COUNT(*) FROM platform_links WHERE account_id='acc-mixed' AND upstream_id='2001'`) != 1 {
		t.Fatal("not linked to the existing account")
	}
}

func TestWebAccountsSyncRelinksToNewestDelivery(t *testing.T) {
	a, stub := webFixture(t)
	insertLocalAccount(t, a, "acc-r", "relink@example.test")
	if _, err := a.DB.Exec(`INSERT INTO platform_links(id,account_id,connection_id,upstream_id,upstream_aid,ingestion_state,created_at,updated_at) VALUES('l-r','acc-r','conn','3001','13001','accepted',?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	stub.setAccounts(
		remoteAccount(3001, "relink@example.test", "ban", "2026-11-01 10:00", nil, nil),
		remoteAccount(3002, "relink@example.test", "using", "2026-11-05 10:00", nil, nil),
	)
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	if got := queryString(t, a, `SELECT upstream_id||'/'||COALESCE(upstream_aid,'')||'/'||COALESCE(runtime_status,'') FROM platform_links WHERE account_id='acc-r'`); got != "3002/13002/using" {
		t.Fatalf("link=%s", got)
	}
	if got := queryString(t, a, `SELECT superseded_by FROM platform_remote_accounts WHERE upstream_id='3001'`); got != "3002" {
		t.Fatalf("old delivery superseded_by=%q", got)
	}
	if got := queryString(t, a, `SELECT superseded_by FROM platform_remote_accounts WHERE upstream_id='3002'`); got != "" {
		t.Fatalf("current delivery superseded_by=%q", got)
	}
	if got := queryString(t, a, `SELECT detail FROM audit_events WHERE action='account.platform_relink'`); got != "3001->3002" {
		t.Fatalf("relink audit detail=%q", got)
	}
	if queryInt(t, a, `SELECT COUNT(*) FROM accounts`) != 1 {
		t.Fatal("relink created an account")
	}
	// Both failed: the newest wins.
	stub.setAccounts(
		remoteAccount(3001, "relink@example.test", "ban", "2026-11-01 10:00", nil, nil),
		remoteAccount(3002, "relink@example.test", "ban", "2026-11-05 10:00", nil, nil),
		remoteAccount(3003, "relink@example.test", "ban", "2026-11-07 10:00", nil, nil),
	)
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	if got := queryString(t, a, `SELECT upstream_id||'/'||COALESCE(runtime_status,'') FROM platform_links WHERE account_id='acc-r'`); got != "3003/ban" {
		t.Fatalf("all-banned link=%s", got)
	}
}

func TestWebAccountsSyncMissingAndReasons(t *testing.T) {
	a, stub := webFixture(t)
	x := remoteAccount(4001, "x@example.test", "using", "2026-11-01 10:00", nil, nil)
	y := remoteAccount(4002, "y@example.test", "ban", "2026-11-01 10:00", nil, nil)
	stub.setAccounts(x, y)
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	if got := queryString(t, a, `SELECT reason_text FROM platform_remote_accounts WHERE upstream_id='4002'`); !strings.Contains(got, "4002") {
		t.Fatalf("banned account reason=%q", got)
	}
	if got := queryString(t, a, `SELECT reason_fetched_at FROM platform_remote_accounts WHERE upstream_id='4002'`); got == "" {
		t.Fatal("reason_fetched_at empty")
	}
	if got := stub.reasons.Load(); got != 2 {
		t.Fatalf("reason calls=%d, want the two reason endpoints for the banned account only", got)
	}
	// Second round: Y gone; reasons not refetched within 6 hours.
	stub.setAccounts(x)
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	if queryString(t, a, `SELECT missing_since FROM platform_remote_accounts WHERE upstream_id='4002'`) == "" {
		t.Fatal("Y not marked missing")
	}
	if queryString(t, a, `SELECT missing_since FROM platform_remote_accounts WHERE upstream_id='4001'`) != "" {
		t.Fatal("X wrongly marked missing")
	}
	// Third round: Y returns, still banned, reasons still fresh.
	stub.setAccounts(x, y)
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	if queryString(t, a, `SELECT missing_since FROM platform_remote_accounts WHERE upstream_id='4002'`) != "" {
		t.Fatal("missing_since not cleared when Y returned")
	}
	if got := stub.reasons.Load(); got != 2 {
		t.Fatalf("reason calls=%d after three rounds, want 2 (6-hour refresh)", got)
	}
	// Status change refetches the reasons.
	beforeChange := queryString(t, a, `SELECT status_changed_at FROM platform_remote_accounts WHERE upstream_id='4002'`)
	if beforeChange == "" {
		t.Fatal("status_changed_at empty after first ban sync")
	}
	y["ext_status"], y["a1_status"] = "check_fail", "check_fail"
	stub.setAccounts(x, y)
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	if got := stub.reasons.Load(); got != 4 {
		t.Fatalf("reason calls=%d after a status change, want 4", got)
	}
	afterChange := queryString(t, a, `SELECT status_changed_at FROM platform_remote_accounts WHERE upstream_id='4002'`)
	if afterChange == "" || afterChange < beforeChange {
		t.Fatalf("status_changed_at went backwards on status change: before=%q after=%q", beforeChange, afterChange)
	}
}

func TestWebAccountsSyncStatusChangedAt(t *testing.T) {
	a, stub := webFixture(t)
	acc := remoteAccount(9001, "offline@example.test", "using", "2026-11-01 10:00", nil, nil)
	stub.setAccounts(acc)
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	first := queryString(t, a, `SELECT status_changed_at FROM platform_remote_accounts WHERE upstream_id='9001'`)
	if first == "" {
		t.Fatal("status_changed_at empty on first insert")
	}
	// Same status: stamp stays put.
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	if got := queryString(t, a, `SELECT status_changed_at FROM platform_remote_accounts WHERE upstream_id='9001'`); got != first {
		t.Fatalf("unchanged status moved status_changed_at: %q → %q", first, got)
	}
	time.Sleep(1100 * time.Millisecond) // now() is second-precision RFC3339
	acc["ext_status"], acc["a1_status"] = "offline", "offline"
	stub.setAccounts(acc)
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	offlineAt := queryString(t, a, `SELECT status_changed_at FROM platform_remote_accounts WHERE upstream_id='9001'`)
	if offlineAt == "" || offlineAt <= first {
		t.Fatalf("offline transition status_changed_at=%q first=%q", offlineAt, first)
	}
	time.Sleep(1100 * time.Millisecond)
	acc["ext_status"], acc["a1_status"] = "ban", "ban"
	stub.setAccounts(acc)
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	banAt := queryString(t, a, `SELECT status_changed_at FROM platform_remote_accounts WHERE upstream_id='9001'`)
	if banAt == "" || banAt <= offlineAt {
		t.Fatalf("ban transition status_changed_at=%q offline=%q", banAt, offlineAt)
	}
}

func TestWebAccountsSyncEmptyListIsFailure(t *testing.T) {
	a, stub := webFixture(t)
	insertLocalAccount(t, a, "acc-e", "e@example.test")
	stub.setAccounts(remoteAccount(5001, "e@example.test", "using", "2026-11-01 10:00", nil, nil))
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	stub.setAccounts()
	err := a.syncWebAccounts(context.Background(), "conn")
	if err == nil {
		t.Fatal("an empty list must be reported as a failure")
	}
	state, code, _, _ := lastRun(t, a, "web-accounts")
	if state != "failed" || code != "PLATFORM_RESPONSE_INVALID" {
		t.Fatalf("state=%s code=%s", state, code)
	}
	if queryString(t, a, `SELECT missing_since FROM platform_remote_accounts WHERE upstream_id='5001'`) != "" {
		t.Fatal("an empty list marked the account missing")
	}
	if queryInt(t, a, `SELECT COUNT(*) FROM platform_links WHERE account_id='acc-e'`) != 1 {
		t.Fatal("an empty list touched the link")
	}
}

func TestWebAccountsSyncWithoutUsage(t *testing.T) {
	a, stub := webFixture(t)
	stub.setAccounts(remoteAccount(6001, "nousage@example.test", "using", "2026-11-01 10:00", nil, nil))
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	var cost *int64
	if err := a.DB.QueryRow(`SELECT usage_cost_micro FROM platform_remote_accounts WHERE upstream_id='6001'`).Scan(&cost); err != nil {
		t.Fatal(err)
	}
	if cost != nil {
		t.Fatalf("usage cost should be NULL, got %d", *cost)
	}
	if queryInt(t, a, `SELECT COUNT(*) FROM platform_usage_snapshots`) != 0 {
		t.Fatal("snapshot written without usage")
	}
	if state, _, _, _ := lastRun(t, a, "web-accounts"); state != "succeeded" {
		t.Fatalf("state=%s", state)
	}
}

func TestWebAccountsSyncUsageSnapshotOnChange(t *testing.T) {
	a, stub := webFixture(t)
	row := remoteAccount(7001, "usage@example.test", "using", "2026-11-01 10:00", fixtureUsage(35209959, 0.05), nil)
	stub.setAccounts(row)
	sync := func() {
		t.Helper()
		if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
			t.Fatal(err)
		}
	}
	sync()
	stub.setAccounts(remoteAccount(7001, "usage@example.test", "using", "2026-11-01 10:00", fixtureUsage(35300000, 0.07), nil))
	sync()
	if queryInt(t, a, `SELECT COUNT(*) FROM platform_usage_snapshots`) != 2 {
		t.Fatal("changed usage must add a snapshot")
	}
	sync()
	if queryInt(t, a, `SELECT COUNT(*) FROM platform_usage_snapshots`) != 2 {
		t.Fatal("unchanged usage must not add a snapshot")
	}
	if got := queryInt(t, a, `SELECT usage_cost_micro FROM platform_remote_accounts WHERE upstream_id='7001'`); got != 35300000 {
		t.Fatalf("latest cost=%d", got)
	}
	if got := queryString(t, a, `SELECT five_hour_reset_at FROM platform_remote_accounts WHERE upstream_id='7001'`); got != "2026-11-11T16:50:00Z" {
		t.Fatalf("reset at=%q", got)
	}
}

func TestWebAccountsSyncLoginFailureSuspends(t *testing.T) {
	a, stub := webFixture(t)
	if _, err := a.DB.Exec(`UPDATE platform_connections SET updated_at=? WHERE id='conn'`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	stub.mu.Lock()
	stub.password = "rotated"
	stub.mu.Unlock()
	for i := 1; i <= 3; i++ {
		if err := a.syncWebAccounts(context.Background(), "conn"); err == nil {
			t.Fatalf("round %d: login should fail", i)
		}
		if _, code, _, _ := lastRun(t, a, "web-accounts"); code != "WEB_LOGIN_FAILED" {
			t.Fatalf("round %d code=%s", i, code)
		}
	}
	if queryString(t, a, `SELECT web_suspended_at FROM platform_connections WHERE id='conn'`) == "" {
		t.Fatal("channel not suspended after three login failures")
	}
	logins := stub.logins.Load()
	if err := a.syncAllWebAccounts(context.Background()); err != nil {
		t.Fatalf("a suspended connection must be skipped silently: %v", err)
	}
	if stub.logins.Load() != logins {
		t.Fatal("suspended connection still tried to log in")
	}
}

func TestWebAccountsSyncReasonFailureDoesNotBackOff(t *testing.T) {
	a, stub := webFixture(t)
	if _, err := a.DB.Exec(`UPDATE platform_connections SET updated_at=? WHERE id='conn'`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	stub.setAccounts(remoteAccount(8001, "r@example.test", "ban", "2026-11-01 10:00", nil, nil))
	stub.mu.Lock()
	stub.reasonStatus = 404
	stub.mu.Unlock()
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	if state, _, _, failed := lastRun(t, a, "web-accounts"); state != "partial" || failed != 1 {
		t.Fatalf("state=%s failed=%d", state, failed)
	}
	if n, _, skip, err := a.syncBackoff(context.Background(), "conn", "web-accounts", time.Now()); err != nil || n != 0 || skip {
		t.Fatalf("a partial web run must not back off: n=%d skip=%v err=%v", n, skip, err)
	}
	calls := stub.reasons.Load()
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	if stub.reasons.Load() != calls {
		t.Fatal("failed reason lookups were retried before the 6-hour refresh")
	}
	if got := queryString(t, a, `SELECT reason_fetched_at FROM platform_remote_accounts WHERE upstream_id='8001'`); got == "" {
		t.Fatal("failed lookup did not record an attempt time")
	}
}

func TestWebAccountsSyncRelinksWhenLocalEmailDiffers(t *testing.T) {
	a, stub := webFixture(t)
	// Local account linked (by the OpenAPI submit flow) to delivery X under a
	// differently spelled email; the platform then issues delivery Y.
	insertLocalAccount(t, a, "acc-typo", "typo@example.test")
	if _, err := a.DB.Exec(`INSERT INTO platform_links(id,account_id,connection_id,upstream_id,ingestion_state,created_at,updated_at) VALUES('l-x','acc-typo','conn','9101','accepted',?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	stub.setAccounts(
		remoteAccount(9101, "real@example.test", "ban", "2026-11-01 10:00", nil, nil),
		remoteAccount(9102, "real@example.test", "using", "2026-11-05 10:00", nil, nil),
	)
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	if queryInt(t, a, `SELECT COUNT(*) FROM accounts`) != 1 {
		t.Fatal("a replaced delivery created a duplicate local account")
	}
	if got := queryString(t, a, `SELECT upstream_id FROM platform_links WHERE account_id='acc-typo'`); got != "9102" {
		t.Fatalf("link=%s, want the new delivery", got)
	}
	if got := queryString(t, a, `SELECT superseded_by FROM platform_remote_accounts WHERE upstream_id='9101'`); got != "9102" {
		t.Fatalf("superseded_by=%q", got)
	}
}

func TestWebAccountsSyncShortListingDoesNotMarkMissing(t *testing.T) {
	a, stub := webFixture(t)
	stub.setAccounts(remoteAccount(9201, "s1@example.test", "using", "2026-11-01 10:00", nil, nil), remoteAccount(9202, "s2@example.test", "using", "2026-11-01 10:00", nil, nil))
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	// The console claims 2 accounts but only lists one.
	stub.setAccounts(remoteAccount(9201, "s1@example.test", "using", "2026-11-01 10:00", nil, nil))
	total := 2
	stub.mu.Lock()
	stub.total = &total
	stub.mu.Unlock()
	if err := a.syncWebAccounts(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	if queryString(t, a, `SELECT missing_since FROM platform_remote_accounts WHERE upstream_id='9202'`) != "" {
		t.Fatal("a short listing marked an unseen account missing")
	}
	if state, _, _, _ := lastRun(t, a, "web-accounts"); state != "partial" {
		t.Fatalf("state=%s, want partial", state)
	}
}
