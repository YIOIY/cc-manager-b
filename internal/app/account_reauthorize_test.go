package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func reauthAuth(email string) map[string]any {
	now := time.Now().UTC()
	return map[string]any{
		"id_token": "", "access_token": "new-access", "refresh_token": "new-refresh",
		"last_refresh": now.Add(-time.Hour).Format(time.RFC3339), "email": email,
		"account_uuid": "u", "organization_uuid": "o", "organization_name": "Org",
		"claude_device_ids": []any{"device-id"}, "type": "claude",
		"expired":             now.Add(30 * 24 * time.Hour).Format(time.RFC3339),
		"proxy_url":           "",
		"verification_status": "not_required",
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

func sealAccountAuth(t *testing.T, a *App, accountID string, auth map[string]any) {
	t.Helper()
	authBytes, err := json.Marshal(auth)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := a.keys.Seal(authBytes, []byte(accountID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec(`UPDATE accounts SET credentials_json=? WHERE id=?`, sealed, accountID); err != nil {
		t.Fatal(err)
	}
}

func TestReauthorizeOmitsAuthJSONUsesSavedCredential(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,name,scheme,host,port,enabled,created_at,updated_at) VALUES('pool','exit','http','127.0.0.1',9090,1,?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	saved := reauthAuth("one@example.test")
	saved["access_token"] = "saved-access"
	saved["proxy_url"] = "http://old.example:1"
	sealAccountAuth(t, a, id, saved)

	res := postReauth(t, admin, id, map[string]any{"proxy_id": "pool"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("omit status=%d body=%s", res.StatusCode, readBody(res))
	}
	if len(stub.updates) != 1 {
		t.Fatalf("updates=%d", len(stub.updates))
	}
	authJSON, _ := stub.updates[0]["cpa_auth_json"].(string)
	var pushed map[string]any
	if err := json.Unmarshal([]byte(authJSON), &pushed); err != nil {
		t.Fatal(err)
	}
	if pushed["access_token"] != "saved-access" {
		t.Fatalf("access_token=%v", pushed["access_token"])
	}
	if pushed["proxy_url"] != "http://127.0.0.1:9090" {
		t.Fatalf("proxy_url=%v", pushed["proxy_url"])
	}
	// Local copy rewritten with the merged auth that was pushed.
	loaded, err := a.loadCredential(id)
	if err != nil {
		t.Fatal(err)
	}
	if loaded["access_token"] != "saved-access" || loaded["proxy_url"] != "http://127.0.0.1:9090" {
		t.Fatalf("stored=%v", loaded)
	}
}

func TestReauthorizeNullAuthJSONUsesSavedCredential(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,name,scheme,host,port,enabled,created_at,updated_at) VALUES('pool','exit','http','127.0.0.1',9090,1,?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	saved := reauthAuth("one@example.test")
	saved["access_token"] = "null-path-access"
	sealAccountAuth(t, a, id, saved)
	res := postReauth(t, admin, id, map[string]any{"auth_json": nil, "proxy_id": "pool"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("null status=%d body=%s", res.StatusCode, readBody(res))
	}
	authJSON, _ := stub.updates[0]["cpa_auth_json"].(string)
	if !strings.Contains(authJSON, "null-path-access") {
		t.Fatalf("pushed=%s", authJSON)
	}
}

func TestReauthorizeRejectsExpiredSavedCredential(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,name,scheme,host,port,enabled,created_at,updated_at) VALUES('pool','exit','http','127.0.0.1',9090,1,?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	saved := reauthAuth("one@example.test")
	saved["expired"] = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	sealAccountAuth(t, a, id, saved)
	res := postReauth(t, admin, id, map[string]any{"proxy_id": "pool"})
	body := readBody(res)
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "auth_json.expired") || !strings.Contains(body, "访问令牌已过期") {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	if len(stub.updates) != 0 {
		t.Fatalf("supplier updates=%d", len(stub.updates))
	}
}

func TestReauthorizeOmitAuthOverlaysDeviceIDs(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,name,scheme,host,port,enabled,created_at,updated_at) VALUES('pool','exit','http','127.0.0.1',9090,1,?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	saved := reauthAuth("one@example.test")
	saved["access_token"] = "saved-access"
	delete(saved, "claude_device_ids")
	sealAccountAuth(t, a, id, saved)

	res := postReauth(t, admin, id, map[string]any{"proxy_id": "pool"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("auto device status=%d body=%s", res.StatusCode, readBody(res))
	}
	authJSON, _ := stub.updates[0]["cpa_auth_json"].(string)
	var pushed map[string]any
	if err := json.Unmarshal([]byte(authJSON), &pushed); err != nil {
		t.Fatal(err)
	}
	if pushed["access_token"] != "saved-access" {
		t.Fatalf("access_token=%v", pushed["access_token"])
	}
	ids, _ := pushed["claude_device_ids"].([]any)
	if len(ids) != 1 || strings.TrimSpace(fmt.Sprint(ids[0])) == "" {
		t.Fatalf("expected auto-generated device id, got %v", pushed["claude_device_ids"])
	}
	generated := ids[0]

	res = postReauth(t, admin, id, map[string]any{
		"proxy_id":          "pool",
		"claude_device_ids": []any{"overlay-device"},
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("overlay status=%d body=%s", res.StatusCode, readBody(res))
	}
	authJSON, _ = stub.updates[1]["cpa_auth_json"].(string)
	if err := json.Unmarshal([]byte(authJSON), &pushed); err != nil {
		t.Fatal(err)
	}
	ids, _ = pushed["claude_device_ids"].([]any)
	if len(ids) != 1 || ids[0] != "overlay-device" {
		t.Fatalf("claude_device_ids=%v", pushed["claude_device_ids"])
	}
	if ids[0] == generated {
		t.Fatal("overlay should replace the generated device id")
	}
	loaded, err := a.loadCredential(id)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := loaded["claude_device_ids"].([]any)
	if len(stored) != 1 || stored[0] != "overlay-device" {
		t.Fatalf("stored devices=%v", loaded["claude_device_ids"])
	}
}

func TestReauthorizeInvalidAuthJSONDistinctFromOmit(t *testing.T) {
	a, _, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,name,scheme,host,port,enabled,created_at,updated_at) VALUES('pool','exit','http','127.0.0.1',9090,1,?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	sealAccountAuth(t, a, id, reauthAuth("one@example.test"))
	// Invalid payload is format error even when a saved credential exists.
	assertEnvelope(t, postReauth(t, admin, id, map[string]any{"auth_json": "not-json", "proxy_id": "pool"}), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	// Omit with no readable sealed credential.
	if _, err := a.DB.Exec(`UPDATE accounts SET credentials_json='{}' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	assertEnvelope(t, postReauth(t, admin, id, map[string]any{"proxy_id": "pool"}), http.StatusUnprocessableEntity, "CREDENTIALS_REQUIRED")
}

func TestReauthorizeSavedCredentialReloadsUnderLock(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,name,scheme,host,port,enabled,created_at,updated_at) VALUES('pool','exit','http','127.0.0.1',9090,1,?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	old := reauthAuth("one@example.test")
	old["access_token"] = "stale-access"
	sealAccountAuth(t, a, id, old)

	lock := a.webSyncLock("conn")
	lock.Lock()
	done := make(chan *http.Response, 1)
	go func() {
		// No expected_version: must still pick up the token written while waiting.
		done <- postReauth(t, admin, id, map[string]any{"proxy_id": "pool"})
	}()
	time.Sleep(300 * time.Millisecond)
	fresh := reauthAuth("one@example.test")
	fresh["access_token"] = "fresh-access"
	sealAccountAuth(t, a, id, fresh)
	if _, err := a.DB.Exec(`UPDATE accounts SET version=version+1,updated_at=? WHERE id=?`, now(), id); err != nil {
		lock.Unlock()
		t.Fatal(err)
	}
	lock.Unlock()
	res := <-done
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.StatusCode, readBody(res))
	}
	authJSON, _ := stub.updates[0]["cpa_auth_json"].(string)
	if !strings.Contains(authJSON, "fresh-access") {
		t.Fatalf("pushed stale credential: %s", authJSON)
	}
	if strings.Contains(authJSON, "stale-access") {
		t.Fatalf("stale token leaked: %s", authJSON)
	}
}

func TestReauthorizeSavedCredentialChangeAndClearProxy(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,name,scheme,host,port,enabled,created_at,updated_at) VALUES('pool','exit','http','127.0.0.1',9090,1,?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	saved := reauthAuth("one@example.test")
	saved["access_token"] = "keep-me"
	saved["proxy_url"] = "socks5h://imported:secret@1.2.3.4:1080"
	sealAccountAuth(t, a, id, saved)
	if _, err := a.DB.Exec(`UPDATE accounts SET proxy_id='pool',version=3 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}

	res := postReauth(t, admin, id, map[string]any{"proxy_id": "", "expected_version": 3})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("clear proxy status=%d body=%s", res.StatusCode, readBody(res))
	}
	if v := queryString(t, a, `SELECT proxy_id FROM accounts WHERE id=?`, id); v != "" {
		t.Fatalf("proxy_id=%q", v)
	}
	authJSON, _ := stub.updates[0]["cpa_auth_json"].(string)
	var pushed map[string]any
	_ = json.Unmarshal([]byte(authJSON), &pushed)
	if pushed["proxy_url"] != "socks5h://imported:secret@1.2.3.4:1080" {
		t.Fatalf("proxy_url=%v", pushed["proxy_url"])
	}
	assertEnvelope(t, postReauth(t, admin, id, map[string]any{"proxy_id": "", "expected_version": 3}), http.StatusConflict, "VERSION_CONFLICT")
}

func readBody(res *http.Response) string {
	defer res.Body.Close()
	var buf [4096]byte
	n, _ := res.Body.Read(buf[:])
	return string(buf[:n])
}

func TestReauthorizeFreshTokensInheritSavedFields(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	saved := reauthAuth("one@example.test")
	saved["access_token"] = "old-access"
	saved["refresh_token"] = "old-refresh"
	saved["proxy_url"] = "http://saved.example:8080"
	sealAccountAuth(t, a, id, saved)
	fresh := reauthAuth("one@example.test")
	for _, key := range []string{"account_uuid", "organization_name", "claude_device_ids", "proxy_url"} {
		delete(fresh, key)
	}
	res := postReauth(t, admin, id, map[string]any{"auth_json": fresh})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.StatusCode, readBody(res))
	}
	authJSON, _ := stub.updates[0]["cpa_auth_json"].(string)
	var pushed map[string]any
	if err := json.Unmarshal([]byte(authJSON), &pushed); err != nil {
		t.Fatal(err)
	}
	if pushed["access_token"] != "new-access" || pushed["refresh_token"] != "new-refresh" || pushed["account_uuid"] != "u" || pushed["organization_name"] != "Org" || pushed["proxy_url"] != "http://saved.example:8080" {
		t.Fatal("fresh tokens or inherited fields do not match")
	}
	ids, _ := pushed["claude_device_ids"].([]any)
	if len(ids) != 1 || ids[0] != "device-id" {
		t.Fatal("saved devices were not inherited")
	}
	loaded, err := a.loadCredential(id)
	if err != nil || loaded["access_token"] != "new-access" || loaded["proxy_url"] != "http://saved.example:8080" {
		t.Fatal("merged credential was not persisted")
	}
}

func TestReauthorizeNeverInheritsTokensOrUnconfirmedIdentity(t *testing.T) {
	for _, scenario := range []string{"access_token", "refresh_token", "last_refresh", "expired", "organization_uuid", "different-account", "different-organization", "foreign-saved-email"} {
		t.Run(scenario, func(t *testing.T) {
			a, stub, admin := routesFixture(t)
			id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
			saved := reauthAuth("one@example.test")
			saved["proxy_url"] = "http://saved.example:8080"
			fresh := reauthAuth("one@example.test")
			fresh["proxy_url"] = "http://fresh.example:8080"
			switch scenario {
			case "different-account":
				fresh["account_uuid"] = "different"
				delete(fresh, "claude_device_ids")
			case "different-organization":
				fresh["organization_uuid"] = "different"
				delete(fresh, "organization_name")
			case "foreign-saved-email":
				saved["email"] = "other@example.test"
				delete(fresh, "claude_device_ids")
			default:
				delete(fresh, scenario)
			}
			sealAccountAuth(t, a, id, saved)
			res := postReauth(t, admin, id, map[string]any{"auth_json": fresh})
			// Missing devices alone are auto-filled; identity mismatch only blocks inheritance.
			if scenario == "different-account" || scenario == "foreign-saved-email" {
				if res.StatusCode != http.StatusOK {
					t.Fatalf("status=%d body=%s", res.StatusCode, readBody(res))
				}
				var pushed map[string]any
				if err := json.Unmarshal([]byte(stub.updates[0]["cpa_auth_json"].(string)), &pushed); err != nil {
					t.Fatal(err)
				}
				if pushed["proxy_url"] != "http://fresh.example:8080" {
					t.Fatalf("saved proxy was inherited under unconfirmed identity: %v", pushed["proxy_url"])
				}
				ids, _ := pushed["claude_device_ids"].([]any)
				if len(ids) != 1 || ids[0] == "device-id" {
					t.Fatalf("expected generated device id without inheriting saved devices, got %v", ids)
				}
				return
			}
			assertEnvelope(t, res, http.StatusUnprocessableEntity, "VALIDATION_FAILED")
			if len(stub.updates) != 0 {
				t.Fatal("incomplete or mismatched credentials reached platform")
			}
		})
	}
}

func TestReauthorizeDeviceOverlayRejectsNonStrings(t *testing.T) {
	for _, ids := range []any{[]any{123}, []any{true}, []any{map[string]any{"id": "invalid"}}, []any{nil}, 123} {
		a, stub, admin := routesFixture(t)
		id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
		saved := reauthAuth("one@example.test")
		saved["proxy_url"] = "http://saved.example:8080"
		sealAccountAuth(t, a, id, saved)
		assertEnvelope(t, postReauth(t, admin, id, map[string]any{"claude_device_ids": ids}), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
		if len(stub.updates) != 0 {
			t.Fatal("invalid device IDs reached platform")
		}
	}
}

func TestReauthorizeExplicitFreshFieldsTakePrecedence(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	saved := reauthAuth("one@example.test")
	saved["proxy_url"] = "http://old.example:8080"
	sealAccountAuth(t, a, id, saved)
	fresh := reauthAuth("one@example.test")
	fresh["organization_name"] = "New name"
	fresh["proxy_url"] = "http://new.example:8080"
	fresh["claude_device_ids"] = []any{"new-device"}
	res := postReauth(t, admin, id, map[string]any{"auth_json": fresh})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.StatusCode, readBody(res))
	}
	var pushed map[string]any
	if err := json.Unmarshal([]byte(stub.updates[0]["cpa_auth_json"].(string)), &pushed); err != nil {
		t.Fatal(err)
	}
	if pushed["organization_name"] != "New name" || pushed["proxy_url"] != "http://new.example:8080" || pushed["claude_device_ids"].([]any)[0] != "new-device" {
		t.Fatal("explicit fresh fields were overwritten")
	}
}

func TestReauthorizeReloadsOmittedProxyUnderLock(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,name,scheme,host,port,enabled,created_at,updated_at) VALUES('new-pool','exit','http','127.0.0.1',9090,1,?,?)`, now(), now()); err != nil {
		t.Fatal(err)
	}
	saved := reauthAuth("one@example.test")
	saved["proxy_url"] = "http://old.example:8080"
	sealAccountAuth(t, a, id, saved)
	lock := a.webSyncLock("conn")
	lock.Lock()
	done := make(chan *http.Response, 1)
	go func() { done <- postReauth(t, admin, id, map[string]any{}) }()
	time.Sleep(300 * time.Millisecond)
	if _, err := a.DB.Exec(`UPDATE accounts SET proxy_id='new-pool',version=version+1 WHERE id=?`, id); err != nil {
		lock.Unlock()
		t.Fatal(err)
	}
	lock.Unlock()
	res := <-done
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.StatusCode, readBody(res))
	}
	if queryString(t, a, `SELECT proxy_id FROM accounts WHERE id=?`, id) != "new-pool" {
		t.Fatal("concurrently changed pool binding was lost")
	}
	var pushed map[string]any
	if err := json.Unmarshal([]byte(stub.updates[0]["cpa_auth_json"].(string)), &pushed); err != nil {
		t.Fatal(err)
	}
	if pushed["proxy_url"] != "http://127.0.0.1:9090" {
		t.Fatal("latest pool proxy was not pushed")
	}
}
