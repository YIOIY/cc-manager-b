package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"cc-manager/internal/claudeoauth"
)

// fixtureProvider answers token/profile for one brand-new account.
func fixtureProvider(email string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/oauth/token":
			if r.Method == http.MethodGet {
				// What the live endpoint answers a probe with: a JSON refusal.
				w.WriteHeader(http.StatusForbidden)
				io.WriteString(w, `{"error":{"type":"forbidden","message":"Request not allowed"}}`)
				return
			}
			io.WriteString(w, `{"access_token":"a","refresh_token":"r","expires_in":3600,"account":{"uuid":"u1","email_address":"`+email+`"},"organization":{"uuid":"o1","name":"Org"}}`)
		case "/api/oauth/profile":
			io.WriteString(w, `{"account":{"uuid":"u1","email":"`+email+`","created_at":"2025-01-02T03:00:00Z"},"organization":{"uuid":"o1","name":"Org","rate_limit_tier":"default_claude_max_5x"}}`)
		default:
			w.WriteHeader(404)
		}
	}
}

type startResult struct {
	status int
	data   map[string]any
}

func startSession(t *testing.T, c *testClient, body string) startResult {
	t.Helper()
	res := c.do("POST", "/api/v1/claude-oauth/sessions", body)
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var env struct {
		Data  map[string]any `json:"data"`
		Error map[string]any `json:"error"`
	}
	_ = json.Unmarshal(b, &env)
	if env.Data != nil {
		return startResult{res.StatusCode, env.Data}
	}
	return startResult{res.StatusCode, env.Error}
}

// The live token endpoint answers a bare GET with a JSON 403; the probe must
// treat that as "the exit reaches Claude", otherwise every pool proxy is refused.
func TestOAuthStartAcceptsLiveStyleProbeRefusal(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	a.probeOAuthReach = nil // use the real probe against the fixture
	retryOAuthProxy(t, a, fixtureProvider("probe@example.test"))
	c := newTestClient(t, a)
	c.login("workflow-test-password")
	got := startSession(t, c, `{"proxy_id":"review-proxy"}`)
	if got.status != http.StatusCreated {
		t.Fatalf("start through a reachable proxy refused: %d %v", got.status, got.data)
	}
}

// A probe that is rate limited is a cooldown, not a broken exit: the start
// must answer OAUTH_RATE_LIMITED with the upstream wait, never PROXY_UNREACHABLE.
func TestOAuthStartKeepsProbeRateLimitTyped(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	a.probeOAuthReach = func(context.Context, *http.Client) error {
		return &claudeoauth.ReachBusy{RetryAfter: 90 * time.Second}
	}
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,scheme,host,port,enabled) VALUES('busy-proxy','http','127.0.0.1',9090,1)`); err != nil {
		t.Fatal(err)
	}
	c := newTestClient(t, a)
	c.login("workflow-test-password")
	got := startSession(t, c, `{"proxy_id":"busy-proxy"}`)
	if got.status != http.StatusTooManyRequests || got.data["code"] != "OAUTH_RATE_LIMITED" {
		t.Fatalf("probe 429 misclassified: %d %v", got.status, got.data)
	}
	if secs, _ := got.data["details"].(map[string]any)["retry_after_seconds"].(float64); secs < 89 {
		t.Fatalf("upstream Retry-After not honoured: %v", got.data)
	}
}

// Finishing one brand-new account must not throttle the operator's next one:
// the post-success cooldown belongs to the authorized identity, not to the
// browser session that happens to have performed it.
func TestOAuthSuccessDoesNotThrottleNextNewAccountOfSameOperator(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	retryOAuthProxy(t, a, fixtureProvider("first@example.test"))
	c := newTestClient(t, a)
	c.login("workflow-test-password")
	first := startSession(t, c, `{"proxy_id":"review-proxy"}`)
	if first.status != http.StatusCreated {
		t.Fatalf("first start: %d %v", first.status, first.data)
	}
	sessionID := first.data["id"].(string)
	u, _ := url.Parse(first.data["authorization_url"].(string))
	res := c.do("POST", "/api/v1/claude-oauth/sessions/"+sessionID+"/exchange", `{"result":"code#`+u.Query().Get("state")+`"}`)
	exBody := readBody(res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("exchange: %d %s", res.StatusCode, exBody)
	}
	var ex struct {
		Data struct {
			Auth map[string]any `json:"auth_json"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(exBody), &ex)
	body := deliveryBody()
	body["account"] = "first@example.test"
	body["proxy_id"] = "review-proxy"
	body["oauth_session_id"] = sessionID
	ex.Data.Auth["proxy_url"] = ""
	body["auth_json"] = ex.Data.Auth
	payload, _ := json.Marshal(body)
	if res = c.do("POST", "/api/v1/accounts/onboard", string(payload)); res.StatusCode != http.StatusAccepted {
		t.Fatalf("onboard with proof: %d %s", res.StatusCode, readBody(res))
	}
	// 继续添加下一个: same operator, same proxy, a different new account.
	second := startSession(t, c, `{"proxy_id":"review-proxy"}`)
	if second.status != http.StatusCreated {
		t.Fatalf("next new account throttled after a successful onboard: %d %v", second.status, second.data)
	}
	// The identity that was authorized is the one in cooldown.
	if a.oauthCooldown.remaining("mail:first@example.test", time.Now()) <= 0 {
		t.Fatal("authorized identity not cooled down")
	}
}

// A ready session (tokens cached, response lost) is recovered by the browser
// with an empty result, exactly as ClaudeOAuthPanel.start() sends it.
func TestOAuthExchangeReplaysReadySessionWithoutCode(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	r := retryExchangeRequest()
	cached := claudeoauth.Result{Auth: map[string]any{"access_token": "cached", "refresh_token": "r", "email": "x@example.test"}, Warnings: []string{}}
	a.oauth.items = map[string]oauthPending{"review": {owner: oauthOwner(r), expires: time.Now().Add(time.Minute), proxyID: "review-proxy", tokens: &cached, ready: true, codeHash: sha256.Sum256([]byte("review-code"))}}
	exchange := func(result string) *httptest.ResponseRecorder {
		req := withAdminSession(httptest.NewRequest("POST", "/api/v1/claude-oauth/sessions/review/exchange", strings.NewReader(`{"result":"`+result+`"}`)))
		req.SetPathValue("id", "review")
		req.AddCookie(r.Cookies()[0])
		w := httptest.NewRecorder()
		a.claudeOAuthExchange(w, req)
		return w
	}
	if w := exchange(""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"access_token":"cached"`) || !strings.Contains(w.Body.String(), `"oauth_session_id":"review"`) {
		t.Fatalf("empty result did not replay the ready session: %d %s", w.Code, w.Body.String())
	}
	// A different code is still refused: the cache stays bound to its code.
	if w := exchange("other-code"); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("foreign code accepted on a ready session: %d %s", w.Code, w.Body.String())
	}
	if w := exchange("review-code"); w.Code != http.StatusOK {
		t.Fatalf("original code refused on a ready session: %d %s", w.Code, w.Body.String())
	}
}

// Cached tokens that expired while the session waited cannot complete it;
// the session is retired instead of failing the same stage forever.
func TestOAuthExchangeRetiresSessionWhoseCachedTokenExpired(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	retryOAuthProxy(t, a, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream must not be called for an expired cache: %s %s", r.Method, r.URL.Path)
	})
	r := retryExchangeRequest()
	cached := claudeoauth.Result{Auth: map[string]any{"access_token": "cached", "refresh_token": "r", "expired": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}, Warnings: []string{}}
	a.oauth.items = map[string]oauthPending{"review": {owner: oauthOwner(r), expires: time.Now().Add(time.Minute), proxyID: "review-proxy", tokens: &cached, codeHash: sha256.Sum256([]byte("review-code"))}}
	w := retryExchange(a)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "OAUTH_SESSION_EXPIRED") || !strings.Contains(w.Body.String(), `"oauth_session_expired":true`) {
		t.Fatalf("expired cache not retired: %d %s", w.Code, w.Body.String())
	}
	if _, kept := a.oauth.items["review"]; kept {
		t.Fatal("pending with an expired cache survived")
	}
}

func TestOAuthProbeCacheIsSafeForConcurrentStarts(t *testing.T) {
	var c oauthProbeCache
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i byte) {
			defer wg.Done()
			fp := [32]byte{i}
			for n := 0; n < 200; n++ {
				c.store(fp, time.Now(), time.Minute)
				_ = c.remaining(fp, time.Now())
			}
		}(byte(i))
	}
	wg.Wait()
}

// One live link per browser session: asking for another exit or account must
// not silently hand back the old link (which the browser would then compare
// client-side), nor mint a second one. The 409 names the live session so the
// browser can refresh or cancel it; an explicit refresh replaces it.
func TestOAuthStartConflictsOnDifferentBinding(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	retryOAuthProxy(t, a, fixtureProvider("conflict@example.test"))
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,scheme,host,port,enabled) VALUES('other-proxy','http','127.0.0.1',9090,1)`); err != nil {
		t.Fatal(err)
	}
	c := newTestClient(t, a)
	c.login("workflow-test-password")
	first := startSession(t, c, `{"proxy_id":"review-proxy"}`)
	if first.status != http.StatusCreated {
		t.Fatalf("first start: %d %v", first.status, first.data)
	}
	firstID := first.data["id"].(string)

	same := startSession(t, c, `{"proxy_id":"review-proxy"}`)
	if same.status != http.StatusOK || same.data["id"] != firstID {
		t.Fatalf("same binding not reused: %d %v", same.status, same.data)
	}
	withEmail := startSession(t, c, `{"proxy_id":"review-proxy","email":"Conflict@Example.test"}`)
	if withEmail.status != http.StatusOK || withEmail.data["id"] != firstID {
		t.Fatalf("email learned later must not break reuse: %d %v", withEmail.status, withEmail.data)
	}

	other := startSession(t, c, `{"proxy_id":"other-proxy"}`)
	if other.status != http.StatusConflict || other.data["code"] != "OAUTH_SESSION_CONFLICT" {
		t.Fatalf("different exit: %d %v", other.status, other.data)
	}
	details, _ := other.data["details"].(map[string]any)
	if details["oauth_session_id"] != firstID || details["proxy_id"] != "review-proxy" || details["account_id"] != "" || details["stage"] != "pending" {
		t.Fatalf("conflict details: %v", details)
	}
	forAccount := startSession(t, c, `{"proxy_id":"review-proxy","account_id":"some-account"}`)
	if forAccount.status != http.StatusConflict || forAccount.data["code"] != "OAUTH_SESSION_CONFLICT" {
		t.Fatalf("different account: %d %v", forAccount.status, forAccount.data)
	}
	a.oauth.mu.Lock()
	live := len(a.oauth.items)
	a.oauth.mu.Unlock()
	if live != 1 {
		t.Fatalf("conflict minted a session: %d live", live)
	}

	replaced := startSession(t, c, `{"proxy_id":"other-proxy","refresh":true}`)
	if replaced.status != http.StatusCreated || replaced.data["id"] == firstID || replaced.data["proxy_id"] != "other-proxy" {
		t.Fatalf("refresh did not replace: %d %v", replaced.status, replaced.data)
	}
	a.oauth.mu.Lock()
	_, oldAlive := a.oauth.items[firstID]
	a.oauth.mu.Unlock()
	if oldAlive {
		t.Fatal("refresh left the old session alive")
	}
}

// Every non-429 probe failure is the exit's problem: nothing identifying the
// user has been sent yet, so "identity rejected", "supplier rejected" and
// "response invalid" would all point the operator at the wrong fix. Only the
// 429 keeps its own meaning (cooldown).
func TestOAuthStartBlamesExitForEveryProbeFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"html 403", &claudeoauth.StatusError{Op: "reach", Status: http.StatusForbidden}},
		{"html 401", &claudeoauth.StatusError{Op: "reach", Status: http.StatusUnauthorized}},
		{"bad gateway", &claudeoauth.StatusError{Op: "reach", Status: http.StatusBadGateway}},
		{"teapot", &claudeoauth.StatusError{Op: "reach", Status: http.StatusTeapot}},
		{"portal 200", &claudeoauth.ResponseError{Op: "reach"}},
		{"dial failure", &claudeoauth.TransportError{Op: "reach"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, _, _ := deliveryFixture(t)
			a.probeOAuthReach = func(context.Context, *http.Client) error { return tc.err }
			if _, err := a.DB.Exec(`INSERT INTO proxies(id,scheme,host,port,enabled) VALUES('blocked-proxy','http','127.0.0.1',9090,1)`); err != nil {
				t.Fatal(err)
			}
			c := newTestClient(t, a)
			c.login("workflow-test-password")
			got := startSession(t, c, `{"proxy_id":"blocked-proxy"}`)
			if got.data["code"] != "OAUTH_PROXY_UNREACHABLE" {
				t.Fatalf("probe failure misclassified: %d %v", got.status, got.data)
			}
			details, _ := got.data["details"].(map[string]any)
			if details["oauth_stage"] != "reach" || details["oauth_session_expired"] == true {
				t.Fatalf("details: %v", details)
			}
			if se, ok := tc.err.(*claudeoauth.StatusError); ok && details["upstream_status"] != float64(se.Status) {
				t.Fatalf("upstream_status lost: %v", details)
			}
			a.oauth.mu.Lock()
			live := len(a.oauth.items)
			a.oauth.mu.Unlock()
			if live != 0 {
				t.Fatalf("failed probe left %d pending sessions", live)
			}
		})
	}
}
