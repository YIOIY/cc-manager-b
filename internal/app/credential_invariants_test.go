package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCredentialValidationDoesNotGenerateDeviceIDs(t *testing.T) {
	auth := reauthAuth("one@example.test")
	auth["proxy_url"] = "http://127.0.0.1:9090"
	delete(auth, "claude_device_ids")
	if p := validateAuthFields(auth); len(p) != 0 {
		t.Fatalf("legacy credential validation: %+v", p)
	}
	if _, exists := auth["claude_device_ids"]; exists {
		t.Fatal("validation generated a transient device ID")
	}
}

func TestCredentialReplacementKeepsPersistedDeviceID(t *testing.T) {
	a, _, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	saved := reauthAuth("one@example.test")
	saved["proxy_url"] = "http://127.0.0.1:9090"
	saved["claude_device_ids"] = []any{"persisted-device"}
	sealAccountAuth(t, a, id, saved)
	incoming := reauthAuth("one@example.test")
	incoming["last_refresh"] = time.Now().UTC().Format(time.RFC3339)
	incoming["proxy_url"] = saved["proxy_url"]
	delete(incoming, "claude_device_ids")
	b, _ := json.Marshal(map[string]any{"auth_json": incoming})
	res := admin.do(http.MethodPut, "/api/v1/accounts/"+id+"/credentials", string(b))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("replacement: %d %s", res.StatusCode, readBody(res))
	}
	res.Body.Close()
	stored, err := a.loadCredential(id)
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := stored["claude_device_ids"].([]any)
	if len(ids) != 1 || ids[0] != "persisted-device" {
		t.Fatalf("device changed: %v", ids)
	}
}

func TestCredentialReplacementRejectsStaleSnapshot(t *testing.T) {
	a, _, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	saved := reauthAuth("one@example.test")
	saved["proxy_url"] = "http://127.0.0.1:9090"
	saved["last_refresh"] = time.Now().UTC().Format(time.RFC3339)
	sealAccountAuth(t, a, id, saved)
	incoming := reauthAuth("one@example.test")
	incoming["access_token"] = "stale-access"
	incoming["proxy_url"] = saved["proxy_url"]
	b, _ := json.Marshal(map[string]any{"auth_json": incoming})
	res := admin.do(http.MethodPut, "/api/v1/accounts/"+id+"/credentials", string(b))
	assertEnvelope(t, res, http.StatusConflict, "CREDENTIAL_STALE")
	stored, err := a.loadCredential(id)
	if err != nil {
		t.Fatal(err)
	}
	if stored["access_token"] != "new-access" {
		t.Fatal("stale credential overwrote saved token")
	}
}

func TestReauthorizeRejectsOlderCredentialBeforePush(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	saved := reauthAuth("one@example.test")
	saved["proxy_url"] = "http://127.0.0.1:9090"
	saved["last_refresh"] = time.Now().UTC().Format(time.RFC3339)
	sealAccountAuth(t, a, id, saved)
	incoming := reauthAuth("one@example.test")
	incoming["proxy_url"] = saved["proxy_url"]
	incoming["access_token"] = "stale-access"
	assertEnvelope(t, postReauth(t, admin, id, map[string]any{"auth_json": incoming}), http.StatusConflict, "CREDENTIAL_STALE")
	if len(stub.updates) != 0 {
		t.Fatal("stale credential was sent to supplier")
	}
}

func TestWorkerPersistsLegacyDeviceBeforeRetry(t *testing.T) {
	var pushed []string
	a, _, _, mode := deliveryFixture(t, func(body map[string]any) {
		auth := body["auth_json"].(map[string]any)
		ids, _ := auth["claude_device_ids"].([]any)
		if len(ids) != 1 {
			t.Errorf("missing device IDs: %v", ids)
			return
		}
		pushed = append(pushed, ids[0].(string))
	})
	body := deliveryBody()
	w := onboardRequest(a, body, "legacy-device")
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	id := responseData(t, w)["account_id"].(string)
	auth := body["auth_json"].(map[string]any)
	delete(auth, "claude_device_ids")
	sealAccountAuth(t, a, id, auth)
	mode.Store(1)
	a.runOne(context.Background())
	stored, err := a.loadCredential(id)
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := stored["claude_device_ids"].([]any)
	if len(ids) != 1 {
		t.Fatalf("generated device was not persisted: %v", ids)
	}
	retry := submitRequest(a, id, "legacy-device-retry")
	if retry.Code != 202 {
		t.Fatal(retry.Body.String())
	}
	mode.Store(0)
	a.runOne(context.Background())
	if len(pushed) != 2 || pushed[0] != pushed[1] || pushed[0] != ids[0] {
		t.Fatalf("device identity changed across retry: %v saved=%v", pushed, ids)
	}
}

func TestWorkerRejectsChangedProxyBinding(t *testing.T) {
	a, calls, _, _ := deliveryFixture(t)
	w := onboardRequest(a, deliveryBody(), "proxy-binding")
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	id := responseData(t, w)["account_id"].(string)
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,scheme,host,port,enabled) VALUES('changed','http','127.0.0.1',9191,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`UPDATE accounts SET proxy_id='changed' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	a.runOne(context.Background())
	code := queryString(t, a, `SELECT json_extract(result_json,'$.code') FROM jobs WHERE account_id=?`, id)
	if calls.Load() != 0 || code != "PROXY_BINDING_CHANGED" {
		t.Fatalf("silent proxy overwrite: posts=%d code=%s", calls.Load(), code)
	}
	stored, err := a.loadCredential(id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(stored["proxy_url"].(string), ":9090") {
		t.Fatal("saved proxy was overwritten")
	}
}

func TestReauthorizeFailedPushKeepsGeneratedDeviceForRetry(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	auth := reauthAuth("one@example.test")
	auth["proxy_url"] = "http://127.0.0.1:9090"
	delete(auth, "claude_device_ids")
	stub.updateResponse = func(w http.ResponseWriter) { w.WriteHeader(503) }
	res := postReauth(t, admin, id, map[string]any{"auth_json": auth, "expected_version": 1})
	if res.StatusCode < 400 {
		t.Fatal("expected supplier failure")
	}
	res.Body.Close()
	stub.updateResponse = nil
	res = postReauth(t, admin, id, map[string]any{"auth_json": auth, "expected_version": 1})
	if res.StatusCode != 200 {
		t.Fatalf("retry: %d %s", res.StatusCode, readBody(res))
	}
	res.Body.Close()
	if len(stub.updates) != 2 {
		t.Fatalf("updates=%d", len(stub.updates))
	}
	var first, second map[string]any
	_ = json.Unmarshal([]byte(stub.updates[0]["cpa_auth_json"].(string)), &first)
	_ = json.Unmarshal([]byte(stub.updates[1]["cpa_auth_json"].(string)), &second)
	if first["claude_device_ids"].([]any)[0] != second["claude_device_ids"].([]any)[0] {
		t.Fatal("failed push caused a second device identity")
	}
}

func TestReauthorizeSavedCredentialCannotChangeExit(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	saved := reauthAuth("one@example.test")
	saved["proxy_url"] = "http://old.example:8080"
	sealAccountAuth(t, a, id, saved)
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,scheme,host,port,enabled) VALUES('different','http','127.0.0.1',9090,1)`); err != nil {
		t.Fatal(err)
	}
	assertEnvelope(t, postReauth(t, admin, id, map[string]any{"proxy_id": "different"}), 409, "PROXY_BINDING_CHANGED")
	if len(stub.updates) != 0 {
		t.Fatal("saved token silently moved to a different exit")
	}
}

func TestCredentialFreshnessOrdering(t *testing.T) {
	stamp := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	saved := map[string]any{"access_token": "a", "refresh_token": "r", "last_refresh": stamp.Format(time.RFC3339), "expired": stamp.Add(time.Hour).Format(time.RFC3339)}
	for _, tc := range []struct {
		name             string
		refresh, expires time.Duration
		token            string
		reject           bool
	}{
		{"same snapshot", 0, time.Hour, "a", false},
		{"same timestamps different token", 0, time.Hour, "b", true},
		{"older refresh", -time.Minute, 2 * time.Hour, "b", true},
		{"older expiry", time.Minute, 30 * time.Minute, "b", true},
		{"new refresh", time.Minute, time.Hour, "b", false},
		{"new expiry", 0, 2 * time.Hour, "b", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := map[string]any{"access_token": tc.token, "refresh_token": "r", "last_refresh": stamp.Add(tc.refresh).Format(time.RFC3339), "expired": stamp.Add(tc.expires).Format(time.RFC3339)}
			if got := credentialFreshness(auth, saved); (got != nil) != tc.reject {
				t.Fatalf("rejected=%v want %v", got, tc.reject)
			}
		})
	}
}
