package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func reauthAuth(email string) map[string]any {
	return map[string]any{
		"id_token": "", "access_token": "new-access", "refresh_token": "new-refresh",
		"last_refresh": "2026-11-17T00:00:00Z", "email": email,
		"account_uuid": "u", "organization_uuid": "o", "organization_name": "Org",
		"claude_device_ids": []any{"device-id"}, "type": "claude", "expired": "2026-12-17T00:00:00Z",
		"proxy_url": "", "verification_status": "not_required",
	}
}

func postReauth(t *testing.T, c *testClient, accountID string, body map[string]any) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return c.do(http.MethodPost, "/api/v1/accounts/"+accountID+"/reauthorize", string(encoded))
}

func TestReauthorizeFillsProxyURLFromPoolBeforeValidation(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,name,scheme,host,port,enabled,created_at,updated_at) VALUES('pool','exit','http','127.0.0.1',9090,1,?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	res := postReauth(t, admin, id, map[string]any{
		"auth_json": reauthAuth("one@example.test"),
		"proxy_id":  "pool",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.StatusCode, readBody(res))
	}
	if len(stub.updates) != 1 {
		t.Fatalf("updates=%d", len(stub.updates))
	}
	authJSON, _ := stub.updates[0]["cpa_auth_json"].(string)
	var auth map[string]any
	if err := json.Unmarshal([]byte(authJSON), &auth); err != nil {
		t.Fatal(err)
	}
	if auth["proxy_url"] != "http://127.0.0.1:9090" {
		t.Fatalf("proxy_url=%v", auth["proxy_url"])
	}
	if auth["access_token"] != "new-access" {
		t.Fatalf("access_token=%v", auth["access_token"])
	}
	if v := queryString(t, a, `SELECT proxy_id FROM accounts WHERE id=?`, id); v != "pool" {
		t.Fatalf("proxy_id=%q", v)
	}
	if n := queryInt(t, a, `SELECT COUNT(*) FROM audit_events WHERE action='account.reauthorize' AND resource_id=?`, id); n != 1 {
		t.Fatalf("audit=%d", n)
	}
}

func TestReauthorizeRejectsForeignProxy(t *testing.T) {
	a, _, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	if res := admin.do(http.MethodPost, "/api/v1/users", `{"username":"ops","password":"operator-password","role_ids":["`+roleOperatorID+`"]}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("create operator=%d", res.StatusCode)
	}
	opsID := queryString(t, a, `SELECT id FROM users WHERE username='ops'`)
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,name,scheme,host,port,enabled,created_at,updated_at,created_by_user_id) VALUES('foreign','exit','http','127.0.0.1',9090,1,?,?,?)`, now(), now(), opsID); err != nil {
		t.Fatal(err)
	}
	assertEnvelope(t, postReauth(t, admin, id, map[string]any{
		"auth_json": reauthAuth("one@example.test"),
		"proxy_id":  "foreign",
	}), http.StatusUnprocessableEntity, "PROXY_INVALID")
}

func TestReauthorizeClearingProxyUsesCredentialURL(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,name,scheme,host,port,enabled,created_at,updated_at) VALUES('pool','exit','http','127.0.0.1',9090,1,?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`UPDATE accounts SET proxy_id='pool' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	auth := reauthAuth("one@example.test")
	auth["proxy_url"] = "socks5h://imported:secret@1.2.3.4:1080"
	res := postReauth(t, admin, id, map[string]any{
		"auth_json": auth,
		"proxy_id":  "",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.StatusCode, readBody(res))
	}
	if v := queryString(t, a, `SELECT proxy_id FROM accounts WHERE id=?`, id); v != "" {
		t.Fatalf("proxy_id should be cleared, got %q", v)
	}
	authJSON, _ := stub.updates[0]["cpa_auth_json"].(string)
	var pushed map[string]any
	_ = json.Unmarshal([]byte(authJSON), &pushed)
	if pushed["proxy_url"] != "socks5h://imported:secret@1.2.3.4:1080" {
		t.Fatalf("pushed proxy_url=%v", pushed["proxy_url"])
	}
}

func TestReauthorizeRejectsStaleVersionUnderLock(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,name,scheme,host,port,enabled,created_at,updated_at) VALUES('pool','exit','http','127.0.0.1',9090,1,?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	lock := a.webSyncLock("conn")
	lock.Lock()
	done := make(chan *http.Response, 1)
	go func() {
		done <- postReauth(t, admin, id, map[string]any{
			"auth_json":        reauthAuth("one@example.test"),
			"proxy_id":         "pool",
			"expected_version": 1,
		})
	}()
	time.Sleep(300 * time.Millisecond)
	select {
	case res := <-done:
		t.Fatalf("request finished without waiting for the lock: %d", res.StatusCode)
	default:
	}
	// Concurrent reauthorize finished its local save while this one waited.
	if _, err := a.DB.Exec(`UPDATE accounts SET version=2,updated_at=? WHERE id=?`, now(), id); err != nil {
		lock.Unlock()
		t.Fatal(err)
	}
	lock.Unlock()
	res := <-done
	assertEnvelope(t, res, http.StatusConflict, "VERSION_CONFLICT")
	if len(stub.updates) != 0 {
		t.Fatalf("platform should not be updated on stale version, got %d", len(stub.updates))
	}
}

func TestReauthorizeFinishesLocalSaveAfterClientCancel(t *testing.T) {
	a, _, _ := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	authBytes, _ := json.Marshal(reauthAuth("one@example.test"))
	sealed, err := a.keys.Seal(authBytes, []byte(id))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err = a.completeReauthorize(cancelled, id, "admin", "428802", sealed, "kept", 1); err != nil {
		t.Fatal(err)
	}
	if v := queryString(t, a, `SELECT proxy_id FROM accounts WHERE id=?`, id); v != "kept" {
		t.Fatalf("proxy_id=%q", v)
	}
	if n := queryInt(t, a, `SELECT COUNT(*) FROM audit_events WHERE action='account.reauthorize' AND resource_id=? AND actor='admin'`, id); n != 1 {
		t.Fatalf("audit=%d", n)
	}
}

func readBody(res *http.Response) string {
	defer res.Body.Close()
	var buf [4096]byte
	n, _ := res.Body.Read(buf[:])
	return string(buf[:n])
}
