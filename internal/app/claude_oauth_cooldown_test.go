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
	// A wild upstream header is capped so an account is not locked out for a day.
	capped := s.extend("other", now, 5*time.Hour)
	if capped != oauthCooldownCap {
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
	if rem < 60*time.Second || rem > oauthCooldownCap {
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
