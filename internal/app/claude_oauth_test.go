package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClaudeOAuthSessionOwnershipStateExpiryAndCancellation(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	c := newTestClient(t, a)
	res := c.do("POST", "/api/v1/claude-oauth/sessions", `{}`)
	if res.StatusCode != 401 {
		t.Fatalf("anonymous status %d", res.StatusCode)
	}
	res.Body.Close()
	c.login("workflow-test-password")
	start := func(body string) (string, string) {
		if body == "" {
			body = `{}`
		}
		r := c.do("POST", "/api/v1/claude-oauth/sessions", body)
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		if r.StatusCode != 201 {
			t.Fatalf("start: %d %s", r.StatusCode, b)
		}
		if bytes.Contains(b, []byte("verifier")) || !strings.Contains(r.Header.Get("Cache-Control"), "no-store") {
			t.Fatal("unsafe response")
		}
		var env struct {
			Data struct {
				ID  string `json:"id"`
				URL string `json:"authorization_url"`
			} `json:"data"`
		}
		_ = json.Unmarshal(b, &env)
		u, _ := url.Parse(env.Data.URL)
		return env.Data.ID, u.Query().Get("state")
	}
	id, state := start("")
	other := newTestClient(t, a)
	other.login("workflow-test-password")
	res = other.do("POST", "/api/v1/claude-oauth/sessions/"+id+"/exchange", `{"result":"code#`+state+`"}`)
	if res.StatusCode != 404 {
		t.Fatalf("other session %d", res.StatusCode)
	}
	res.Body.Close()
	res = c.do("POST", "/api/v1/claude-oauth/sessions/"+id+"/exchange", `{"result":"code#wrong"}`)
	if res.StatusCode != 422 {
		t.Fatalf("wrong state %d", res.StatusCode)
	}
	res.Body.Close()
	res = c.do("DELETE", "/api/v1/claude-oauth/sessions/"+id, ``)
	res.Body.Close()
	res = c.do("POST", "/api/v1/claude-oauth/sessions/"+id+"/exchange", `{"result":"code"}`)
	if res.StatusCode != 404 {
		t.Fatalf("cancelled %d", res.StatusCode)
	}
	res.Body.Close()
	id, _ = start("")
	a.oauth.mu.Lock()
	p := a.oauth.items[id]
	p.expires = time.Now().Add(-time.Second)
	a.oauth.items[id] = p
	a.oauth.mu.Unlock()
	res = c.do("POST", "/api/v1/claude-oauth/sessions/"+id+"/exchange", `{"result":"code"}`)
	if res.StatusCode != 404 {
		t.Fatalf("expired %d", res.StatusCode)
	}
	res.Body.Close()
	// Without refresh an unexpired link that still belongs to this browser is
	// reused instead of minting another PKCE round trip.
	id, _ = start("")
	res = c.do("POST", "/api/v1/claude-oauth/sessions", `{}`)
	reused := readBody(res)
	if res.StatusCode != 200 {
		t.Fatalf("reuse: %d %s", res.StatusCode, reused)
	}
	var reusedEnv struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(reused), &reusedEnv); err != nil || reusedEnv.Data.ID != id {
		t.Fatalf("reuse id %q want %q (%v)", reusedEnv.Data.ID, id, err)
	}
	// An explicit refresh mints a new link and retires the previous one.
	fresh, _ := start(`{"refresh":true}`)
	if fresh == id {
		t.Fatal("refresh reused the old session")
	}
	res = c.do("POST", "/api/v1/claude-oauth/sessions/"+id+"/exchange", `{"result":"code"}`)
	if res.StatusCode != 404 {
		t.Fatalf("replaced %d", res.StatusCode)
	}
	res.Body.Close()
}

func TestOnboardPoolProxyCanReplaceMissingCredentialURL(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	_, err := a.DB.Exec(`INSERT INTO proxies(id,scheme,host,port,enabled) VALUES('pool','http','127.0.0.1',9090,1)`)
	if err != nil {
		t.Fatal(err)
	}
	body := deliveryBody()
	body["proxy_id"] = "pool"
	delete(body["auth_json"].(map[string]any), "proxy_url")
	w := onboardRequest(a, body, "pool-without-url")
	if w.Code != http.StatusAccepted {
		t.Fatalf("onboard %d %s", w.Code, w.Body.String())
	}
}

func TestClaudeOAuthConsumedAfterProxyFailureAndRequiresCSRF(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	_, err := a.DB.Exec(`INSERT INTO proxies(id,scheme,host,port,enabled) VALUES('oauth-pool','http','127.0.0.1',9090,1)`)
	if err != nil {
		t.Fatal(err)
	}
	c := newTestClient(t, a)
	c.login("workflow-test-password")
	old := c.csrf
	c.csrf = "wrong"
	res := c.do("POST", "/api/v1/claude-oauth/sessions", `{}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf status %d", res.StatusCode)
	}
	res.Body.Close()
	c.csrf = old
	res = c.do("POST", "/api/v1/claude-oauth/sessions", `{"proxy_id":"oauth-pool"}`)
	var env struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if res.StatusCode != 201 {
		t.Fatalf("start %d", res.StatusCode)
	}
	_ = json.NewDecoder(res.Body).Decode(&env)
	res.Body.Close()
	_, err = a.DB.Exec(`UPDATE proxies SET enabled=0 WHERE id='oauth-pool'`)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/claude-oauth/sessions/" + env.Data.ID + "/exchange"
	res = c.do("POST", path, `{"result":"code"}`)
	if res.StatusCode != 422 || !strings.Contains(readBody(res), "OAUTH_PROXY_INVALID") {
		t.Fatalf("disabled proxy %d", res.StatusCode)
	}
	// The link and the pasted code survive a broken exit; the user only has to
	// supply a usable proxy instead of redoing the browser login.
	a.oauth.mu.Lock()
	_, kept := a.oauth.items[env.Data.ID]
	a.oauth.mu.Unlock()
	if !kept {
		t.Fatal("pending dropped after a proxy failure")
	}
	if _, err = a.DB.Exec(`UPDATE proxies SET enabled=1 WHERE id='oauth-pool'`); err != nil {
		t.Fatal(err)
	}
	// Nothing listens on the test proxy, so the retry now fails one step
	// further along (transport) instead of at proxy resolution.
	res = c.do("POST", path, `{"result":"code"}`)
	if body := readBody(res); res.StatusCode != 422 || !strings.Contains(body, "OAUTH_PROXY_UNREACHABLE") {
		t.Fatalf("retry after re-enable %d %s", res.StatusCode, body)
	}
}

func TestClaudeOAuthRequiresOnboardPermission(t *testing.T) {
	a, admin := rbacApp(t)
	res := admin.do("POST", "/api/v1/users", `{"username":"oauth-viewer","password":"viewer-password-1","role_ids":["role-viewer"]}`)
	if res.StatusCode != 201 {
		t.Fatalf("create viewer %d", res.StatusCode)
	}
	res.Body.Close()
	viewer := newTestClient(t, a)
	viewer.loginAs("oauth-viewer", "viewer-password-1")
	for _, call := range []string{"POST /api/v1/claude-oauth/sessions", "POST /api/v1/claude-oauth/sessions/test/exchange", "DELETE /api/v1/claude-oauth/sessions/test"} {
		method, path, _ := strings.Cut(call, " ")
		res = viewer.do(method, path, `{}`)
		if res.StatusCode != 403 {
			t.Fatalf("permission %s: %d", call, res.StatusCode)
		}
		res.Body.Close()
	}
}

func TestClaudeOAuthCanUseExistingAccountProxy(t *testing.T) {
	a, _, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	saved := reauthAuth("one@example.test")
	saved["proxy_url"] = "http://saved-user:saved-password@proxy.example:8080"
	sealAccountAuth(t, a, id, saved)
	res := admin.do("POST", "/api/v1/claude-oauth/sessions", `{"account_id":"`+id+`"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("start=%d body=%s", res.StatusCode, readBody(res))
	}
	body := readBody(res)
	if strings.Contains(body, "saved-password") || strings.Contains(body, "proxy.example") || strings.Contains(body, "access_token") {
		t.Fatal("saved credential leaked")
	}
	var env struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatal(err)
	}
	a.oauth.mu.Lock()
	accountID := a.oauth.items[env.Data.ID].accountID
	a.oauth.mu.Unlock()
	if accountID != id {
		t.Fatal("OAuth session did not bind the saved proxy account")
	}
	proxyURL, err := a.savedCredentialProxy(id)
	if err != nil {
		t.Fatal("cannot read saved proxy")
	}
	client, closeClient, err := a.claudeOAuthHTTPClient("", proxyURL)
	if err != nil {
		t.Fatal("cannot construct proxy client")
	}
	defer closeClient()
	req, _ := http.NewRequest(http.MethodGet, "https://claude.example", nil)
	selected, err := client.Transport.(*http.Transport).Proxy(req)
	if err != nil || selected == nil || selected.Host != "proxy.example:8080" || selected.User.Username() != "saved-user" {
		t.Fatal("OAuth client did not select the saved proxy")
	}
	// Reassignment after creating the link revokes use of the saved proxy.
	if _, err := a.DB.Exec(`UPDATE accounts SET created_by_user_id='another-owner' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	assertEnvelope(t, admin.do("POST", "/api/v1/claude-oauth/sessions/"+env.Data.ID+"/exchange", `{"result":"code"}`), http.StatusForbidden, "PERMISSION_DENIED")
	assertEnvelope(t, admin.do("POST", "/api/v1/claude-oauth/sessions", `{"account_id":"`+id+`"}`), http.StatusForbidden, "PERMISSION_DENIED")
	assertEnvelope(t, admin.do("POST", "/api/v1/claude-oauth/sessions", `{"account_id":"missing"}`), http.StatusNotFound, "NOT_FOUND")
}

func TestClaudeOAuthStartRefusesProxyThatCannotReachClaude(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,scheme,host,port,enabled) VALUES('dead-pool','http','127.0.0.1',9091,1)`); err != nil {
		t.Fatal(err)
	}
	var probed atomic.Int32
	a.probeOAuthReach = func(_ context.Context, client *http.Client) error {
		probed.Add(1)
		// The probe runs through the same transport the exchange will use.
		req, _ := http.NewRequest(http.MethodGet, "https://claude.example", nil)
		if u, _ := client.Transport.(*http.Transport).Proxy(req); u == nil || u.Host != "127.0.0.1:9091" {
			t.Errorf("probe proxy=%v", u)
		}
		return errors.New("dial tcp: connection refused")
	}
	c := newTestClient(t, a)
	c.login("workflow-test-password")
	res := c.do("POST", "/api/v1/claude-oauth/sessions", `{"proxy_id":"dead-pool"}`)
	body := readBody(res)
	if res.StatusCode != 422 || !strings.Contains(body, "OAUTH_PROXY_UNREACHABLE") || !strings.Contains(body, "连不上 Claude") {
		t.Fatalf("start=%d body=%s", res.StatusCode, body)
	}
	if probed.Load() != 1 {
		t.Fatalf("probed %d times", probed.Load())
	}
	a.oauth.mu.Lock()
	pending := len(a.oauth.items)
	a.oauth.mu.Unlock()
	if pending != 0 {
		t.Fatal("a link was issued despite the failed probe")
	}
	// Without a pool proxy nothing is probed and the link is issued.
	res = c.do("POST", "/api/v1/claude-oauth/sessions", `{}`)
	res.Body.Close()
	if res.StatusCode != 201 || probed.Load() != 1 {
		t.Fatalf("no-proxy start=%d probed=%d", res.StatusCode, probed.Load())
	}
}
