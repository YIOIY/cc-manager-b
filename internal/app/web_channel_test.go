package app

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"cc-manager/internal/errs"
)

func TestConnectionWebCredentialsRoundTrip(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "correct-horse-battery")
	t.Setenv("CC_MANAGER_ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	a, err := newApp(t.TempDir()+"/web-conn.db", false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	admin := newTestClient(t, a)
	admin.login("correct-horse-battery")
	var created struct{ Data struct{ ID string } }
	res := admin.do(http.MethodPost, "/api/v1/platform-connections", `{"name":"supplier","base_url":"https://x.example","timezone":"Asia/Shanghai","token":"t","web_username":"console-user","web_password":"console-secret"}`)
	_ = jsonDecode(res, &created)
	if res.StatusCode != http.StatusCreated || created.Data.ID == "" {
		t.Fatalf("create status=%d", res.StatusCode)
	}
	id := created.Data.ID
	var list struct{ Data []map[string]any }
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/platform-connections", ""), &list)
	if len(list.Data) != 1 || list.Data[0]["web_username"] != "console-user" || list.Data[0]["web_configured"] != true || list.Data[0]["web_suspended_at"] != "" {
		t.Fatalf("list=%v", list.Data)
	}
	for k := range list.Data[0] {
		if k == "web_password" || k == "web_password_secret" {
			t.Fatalf("list leaks %s", k)
		}
	}
	var sealedBefore string
	if err := a.DB.QueryRow(`SELECT web_password_secret FROM platform_connections WHERE id=?`, id).Scan(&sealedBefore); err != nil || sealedBefore == "" {
		t.Fatalf("secret not stored: %q %v", sealedBefore, err)
	}
	if plain, err := a.keys.Open(sealedBefore, []byte(id+":web")); err != nil || string(plain) != "console-secret" {
		t.Fatalf("secret not sealed with the web AAD: %v", err)
	}
	if res := admin.do(http.MethodPatch, "/api/v1/platform-connections/"+id, `{"name":"renamed"}`); res.StatusCode != http.StatusOK {
		t.Fatalf("patch status=%d", res.StatusCode)
	}
	var sealedAfter string
	_ = a.DB.QueryRow(`SELECT web_password_secret FROM platform_connections WHERE id=?`, id).Scan(&sealedAfter)
	if sealedAfter != sealedBefore {
		t.Fatal("patch without web_password changed the stored secret")
	}
	if res := admin.do(http.MethodPatch, "/api/v1/platform-connections/"+id, `{"web_clear":true}`); res.StatusCode != http.StatusOK {
		t.Fatalf("clear status=%d", res.StatusCode)
	}
	var username, secret string
	_ = a.DB.QueryRow(`SELECT web_username,web_password_secret FROM platform_connections WHERE id=?`, id).Scan(&username, &secret)
	if username != "" || secret != "" {
		t.Fatalf("clear left %q %q", username, secret)
	}
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/platform-connections", ""), &list)
	if list.Data[0]["web_configured"] != false {
		t.Fatalf("after clear list=%v", list.Data)
	}
	var setCount, clearCount int
	_ = a.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM audit_events WHERE action='connection.web_credentials_set'),(SELECT COUNT(*) FROM audit_events WHERE action='connection.web_credentials_cleared')`).Scan(&setCount, &clearCount)
	if setCount != 1 || clearCount != 1 {
		t.Fatalf("audit set=%d cleared=%d", setCount, clearCount)
	}
	var lookups struct{ Data []map[string]any }
	_ = jsonDecode(admin.do(http.MethodGet, "/api/v1/lookups/platform-connections", ""), &lookups)
	if len(lookups.Data) != 1 || lookups.Data[0]["web_configured"] != false {
		t.Fatalf("lookup=%v", lookups.Data)
	}
}

func TestWebLoginTestEndpoint(t *testing.T) {
	a, stub := webFixture(t)
	admin := newTestClient(t, a)
	admin.login("workflow-test-password")
	if _, err := a.DB.Exec(`UPDATE platform_connections SET web_username='',web_password_secret='' WHERE id='conn'`); err != nil {
		t.Fatal(err)
	}
	assertEnvelope(t, admin.do(http.MethodPost, "/api/v1/platform-connections/conn/web-login-test", ""), http.StatusUnprocessableEntity, "WEB_NOT_CONFIGURED")

	setWebCredentials(t, a, stub)
	stub.mu.Lock()
	stub.password = "changed-on-platform"
	stub.mu.Unlock()
	assertEnvelope(t, admin.do(http.MethodPost, "/api/v1/platform-connections/conn/web-login-test", ""), http.StatusBadGateway, "WEB_LOGIN_FAILED")

	stub.mu.Lock()
	stub.password = "console-secret"
	stub.mu.Unlock()
	if _, err := a.DB.Exec(`UPDATE platform_connections SET web_suspended_at=?,web_suspended_reason='登录连续失败 3 次' WHERE id='conn'`, now()); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Data struct {
			OK           bool   `json:"ok"`
			SupplierName string `json:"supplier_name"`
			ExpiresIn    int    `json:"expires_in"`
		}
	}
	res := admin.do(http.MethodPost, "/api/v1/platform-connections/conn/web-login-test", "")
	_ = jsonDecode(res, &out)
	if res.StatusCode != http.StatusOK || !out.Data.OK || out.Data.SupplierName != "住享科智" || out.Data.ExpiresIn != 18000 {
		t.Fatalf("status=%d out=%+v", res.StatusCode, out.Data)
	}
	var suspended string
	_ = a.DB.QueryRow(`SELECT web_suspended_at FROM platform_connections WHERE id='conn'`).Scan(&suspended)
	if suspended != "" {
		t.Fatal("successful login test did not clear the suspension")
	}
}

func TestNoteWebLoginSuspendsAfterThree(t *testing.T) {
	a, stub := webFixture(t)
	ctx := context.Background()
	loginErr := errs.New(errs.CodeWebLoginFailed)
	// The streak is counted since the credentials were last saved.
	ageConnection := func(app *App) {
		if _, err := app.DB.Exec(`UPDATE platform_connections SET updated_at=? WHERE id='conn'`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	}
	ageConnection(a)
	for i := 2; i >= 1; i-- {
		insertKindRun(t, a, "conn", "web-accounts", "failed", time.Now().Add(-time.Duration(i)*time.Minute))
	}
	if suspended := a.noteWebLogin(ctx, "conn", loginErr); !suspended {
		t.Fatal("third consecutive login failure must suspend the channel")
	}
	var at, reason string
	_ = a.DB.QueryRow(`SELECT web_suspended_at,web_suspended_reason FROM platform_connections WHERE id='conn'`).Scan(&at, &reason)
	if at == "" || reason == "" {
		t.Fatalf("suspension not recorded: %q %q", at, reason)
	}
	if _, _, err := a.webClientFor(ctx, "conn"); err == nil || !errors.Is(err, errWebSuspended) {
		t.Fatalf("suspended connection still hands out a client: %v", err)
	}
	var audits int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='connection.web_suspended'`).Scan(&audits)
	if audits != 1 {
		t.Fatalf("audit rows=%d", audits)
	}
	// Saving credentials again lifts the suspension.
	setWebCredentials(t, a, stub)
	if _, conn, err := a.webClientFor(ctx, "conn"); err != nil || conn.Username != "console-user" || !conn.Configured {
		t.Fatalf("after re-saving: %+v %v", conn, err)
	}
	// Two failures are not enough.
	b, _ := webFixture(t)
	ageConnection(b)
	insertKindRun(t, b, "conn", "web-accounts", "failed", time.Now().Add(-time.Minute))
	if b.noteWebLogin(ctx, "conn", loginErr) {
		t.Fatal("two failures suspended the channel")
	}
	// A non-login failure never counts.
	if b.noteWebLogin(ctx, "conn", errs.New(errs.CodeWebRateLimited)) {
		t.Fatal("rate limit suspended the channel")
	}
}

func TestWebClientForIsCachedPerConnection(t *testing.T) {
	a, _ := webFixture(t)
	ctx := context.Background()
	first, _, err := a.webClientFor(ctx, "conn")
	if err != nil {
		t.Fatal(err)
	}
	second, _, _ := a.webClientFor(ctx, "conn")
	if first != second {
		t.Fatal("client not cached")
	}
	a.dropWebClient("conn")
	third, _, _ := a.webClientFor(ctx, "conn")
	if third == first {
		t.Fatal("dropped client was reused")
	}
	if _, err := a.DB.Exec(`UPDATE platform_connections SET web_username='' WHERE id='conn'`); err != nil {
		t.Fatal(err)
	}
	a.dropWebClient("conn")
	if _, _, err = a.webClientFor(ctx, "conn"); err == nil || !errors.Is(err, errWebNotConfigured) {
		t.Fatalf("unconfigured connection: %v", err)
	}
}
