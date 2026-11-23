package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"cc-manager/internal/claudeoauth"
)

func TestOAuthAccountCooldownExtendKeepsLaterDeadline(t *testing.T) {
	var s oauthAccountCooldown
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	first := s.extend("acc", now, 2*time.Minute)
	if first != 2*time.Minute {
		t.Fatalf("first=%v", first)
	}
	shorter := s.extend("acc", now.Add(10*time.Second), 30*time.Second)
	if shorter < 100*time.Second {
		t.Fatalf("shorter overwrite: %v", shorter)
	}
	// Retry-After is honored even when it exceeds one hour.
	capped := s.extend("other", now, 5*time.Hour)
	if capped != 5*time.Hour {
		t.Fatalf("cap=%v", capped)
	}
	if wait := s.remaining("acc", now.Add(3*time.Minute)); wait != 0 {
		t.Fatalf("expired remaining=%v", wait)
	}
	floored := s.extend("floor", now, 45*time.Second)
	if floored != oauthCooldownDefault {
		t.Fatalf("floor=%v want %v", floored, oauthCooldownDefault)
	}
}

func TestOAuthAccountCooldownRace(t *testing.T) {
	var s oauthAccountCooldown
	now := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			wait := time.Duration(30+i) * time.Second
			s.extend("same", now, wait)
			_ = s.remaining("same", now)
		}(i)
	}
	wg.Wait()
	rem := s.remaining("same", now)
	if rem < 60*time.Second || rem > 61*time.Second {
		t.Fatalf("remaining=%v", rem)
	}
}

func TestClaudeOAuthCooldownSharedAcrossSessionsAndDropsPending(t *testing.T) {
	a, _, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	saved := reauthAuth("one@example.test")
	saved["proxy_url"] = "http://proxy.example:8080"
	sealAccountAuth(t, a, id, saved)

	a.oauthCooldown.extend("id:"+id, time.Now(), 2*time.Minute)

	res := admin.do("POST", "/api/v1/claude-oauth/sessions", `{"account_id":"`+id+`"}`)
	body := readBody(res)
	if res.StatusCode != http.StatusTooManyRequests || !strings.Contains(body, "OAUTH_RATE_LIMITED") {
		t.Fatalf("start cooldown: %d %s", res.StatusCode, body)
	}
	var env struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &env)
	if fmt.Sprint(env.Error.Details["retry_after_seconds"]) == "" || res.Header.Get("Retry-After") == "" {
		t.Fatalf("missing retry-after: details=%v header=%q", env.Error.Details, res.Header.Get("Retry-After"))
	}
	if res.Header.Get("Retry-After") != fmt.Sprint(env.Error.Details["retry_after_seconds"]) {
		t.Fatalf("retry-after mismatch header=%q details=%v", res.Header.Get("Retry-After"), env.Error.Details)
	}

	// A different admin browser session shares the same accountID cooldown.
	other := newTestClient(t, a)
	other.login("workflow-test-password")
	res = other.do("POST", "/api/v1/claude-oauth/sessions", `{"account_id":"`+id+`"}`)
	if res.StatusCode != http.StatusTooManyRequests || !strings.Contains(readBody(res), "OAUTH_RATE_LIMITED") {
		t.Fatalf("cross-session start: %d", res.StatusCode)
	}

	// Clear cooldown briefly to create a pending, then re-apply and exchange.
	a.oauthCooldown.mu.Lock()
	delete(a.oauthCooldown.until, "id:"+id)
	a.oauthCooldown.mu.Unlock()
	res = admin.do("POST", "/api/v1/claude-oauth/sessions", `{"account_id":"`+id+`"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("start after clear: %d %s", res.StatusCode, readBody(res))
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.NewDecoder(res.Body).Decode(&created)
	res.Body.Close()
	a.oauthCooldown.extend("id:"+id, time.Now(), time.Minute)

	res = admin.do("POST", "/api/v1/claude-oauth/sessions/"+created.Data.ID+"/exchange", `{"result":"code"}`)
	exBody := readBody(res)
	if res.StatusCode != http.StatusTooManyRequests || !strings.Contains(exBody, "OAUTH_RATE_LIMITED") {
		t.Fatalf("exchange cooldown: %d %s", res.StatusCode, exBody)
	}
	a.oauth.mu.Lock()
	_, exists := a.oauth.items[created.Data.ID]
	a.oauth.mu.Unlock()
	if !exists {
		t.Fatal("pending should survive a cooldown hit")
	}
}

func TestWriteOAuthExchangeErrorMapsStatuses(t *testing.T) {
	a, _, _ := routesFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/claude-oauth/sessions/x/exchange", nil)
	rr := httptest.NewRecorder()
	a.writeOAuthExchangeError(rr, req, "acc", &claudeoauth.StatusError{Op: "profile", Status: http.StatusUnauthorized})
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "OAUTH_IDENTITY_REJECTED") {
		t.Fatalf("401: %d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	a.writeOAuthExchangeError(rr, req, "acc", &claudeoauth.StatusError{Op: "token", Status: http.StatusTooManyRequests, RetryAfter: 45 * time.Second})
	if rr.Code != http.StatusTooManyRequests || rr.Header().Get("Retry-After") != "60" {
		t.Fatalf("429: %d %s header=%q", rr.Code, rr.Body.String(), rr.Header().Get("Retry-After"))
	}
	if wait := a.oauthCooldown.remaining("acc", time.Now()); wait < 55*time.Second {
		t.Fatalf("cooldown not set: %v", wait)
	}
}

// Success seeds the cooldown under all learned identities, so a *different*
// browser session (different owner) starting with the same account honors it.
func TestOAuthSuccessSeedsCrossSessionCooldown(t *testing.T) {
	a, _, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	email := "one@example.test"
	// Mirror the sealed-credential setup used by the cross-session cooldown
	// test so the saved proxy passes the start-time reachability probe.
	saved := reauthAuth(email)
	saved["proxy_url"] = "http://proxy.example:8080"
	sealAccountAuth(t, a, id, saved)
	// The exchange and profile stages talk to fixed Claude domains. Reuse the
	// existing offline HTTPS fixture so tests never touch the real provider.
	retryOAuthProxy(t, a, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"access_token":"fixture-access","refresh_token":"fixture-refresh","expires_in":3600}`))
		case strings.Contains(r.URL.Path, "profile"):
			_, _ = w.Write([]byte(`{"account":{"uuid":"account-uuid","email":"` + email + `"},"organization":{"uuid":"org-uuid","name":"Fixture"}}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})

	var startErr struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}

	// First session exchanges successfully; owner cooldown is derived from the
	// admin's session cookie, so a second client forms a distinct owner.
	checkOK := func(c *testClient) string {
		res := c.do("POST", "/api/v1/claude-oauth/sessions", `{"account_id":"`+id+`","proxy_id":"review-proxy"}`)
		body := readBody(res)
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("first start: %d %s", res.StatusCode, body)
		}
		var env struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		_ = json.NewDecoder(strings.NewReader(body)).Decode(&env)
		ex := c.do("POST", "/api/v1/claude-oauth/sessions/"+env.Data.ID+"/exchange", `{"result":"code"}`)
		if ex.StatusCode != http.StatusOK {
			t.Fatalf("first exchange: %d %s", ex.StatusCode, readBody(ex))
		}
		ex.Body.Close()
		return env.Data.ID
	}
	firstID := checkOK(admin)

	// Cross-session start with the same account must be rate limited by the
	// freshly seeded accountID/email cooldown keys.
	other := newTestClient(t, a)
	other.login("workflow-test-password")
	res := other.do("POST", "/api/v1/claude-oauth/sessions", `{"account_id":"`+id+`","email":"`+email+`"}`)
	body := readBody(res)
	if res.StatusCode != http.StatusTooManyRequests || !strings.Contains(body, "OAUTH_RATE_LIMITED") {
		t.Fatalf("cross-session start after success: %d %s", res.StatusCode, body)
	}
	_ = json.Unmarshal([]byte(body), &startErr)
	if fmt.Sprint(startErr.Error.Details["retry_after_seconds"]) == "" {
		t.Fatal("missing retry_after_seconds")
	}

	// Mail key is seeded too: even without account_id the cooldown holds.
	res = other.do("POST", "/api/v1/claude-oauth/sessions", `{"email":"`+email+`"}`)
	if res.StatusCode != http.StatusTooManyRequests || !strings.Contains(readBody(res), "OAUTH_RATE_LIMITED") {
		t.Fatalf("mail-key start: %d %s", res.StatusCode, readBody(res))
	}
	_ = firstID
}

func TestSelectPreviousPendingDeterministic(t *testing.T) {
	owner := [32]byte{1}
	stale := oauthPending{owner: owner, expires: time.Now().Add(time.Minute), proxyID: "px-old"}
	newest := oauthPending{owner: owner, expires: time.Now().Add(10 * time.Minute), proxyID: "px-new"}
	items := map[string]oauthPending{"a": stale, "b": newest}
	items["c"] = oauthPending{owner: [32]byte{2}, expires: time.Now().Add(time.Hour)} // foreign owner

	// Exact triple match wins over newer-but-unmatched.
	items["d"] = oauthPending{owner: owner, expires: time.Now().Add(5 * time.Minute), proxyID: "px-old"}
	id, p := selectPreviousPending(items, owner, "px-old", "", "")
	if id != "d" {
		t.Fatalf("match-first: want d got %s (%v)", id, p)
	}
	// No match falls back to the newest expiry, regardless of map order.
	id, _ = selectPreviousPending(items, owner, "zzz", "", "")
	if id != "b" {
		t.Fatalf("fallback-newest: want b got %s", id)
	}
	// Empty result when the owner has no live pending.
	items = map[string]oauthPending{"x": {owner: owner, cancelled: true}}
	if id, p := selectPreviousPending(items, owner, "", "", ""); id != "" || p.owner != owner && p.expires.IsZero() && false {
		t.Fatalf("empty-case: id=%q", id)
	}
}
