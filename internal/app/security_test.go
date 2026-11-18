package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type testClient struct {
	t      *testing.T
	server *httptest.Server
	client *http.Client
	csrf   string
}

func newTestClient(t *testing.T, a *App) *testClient {
	t.Helper()
	server := httptest.NewServer(a.Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	c := &testClient{t: t, server: server, client: &http.Client{Jar: jar}}
	c.refreshSession()
	return c
}

func (c *testClient) refreshSession() {
	res, err := c.client.Get(c.server.URL + "/api/v1/auth/session")
	if err != nil {
		c.t.Fatal(err)
	}
	var env struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		} `json:"data"`
	}
	_ = json.NewDecoder(res.Body).Decode(&env)
	res.Body.Close()
	c.csrf = env.Data.CSRF
}

func (c *testClient) do(method, path, body string) *http.Response {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.server.URL+path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", c.csrf)
	res, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	return res
}

func (c *testClient) login(password string) *http.Response {
	res := c.do(http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"`+password+`"}`)
	if res.StatusCode == http.StatusOK {
		var env struct {
			Data struct {
				CSRF string `json:"csrf_token"`
			} `json:"data"`
		}
		_ = json.NewDecoder(res.Body).Decode(&env)
		c.csrf = env.Data.CSRF
	}
	res.Body.Close()
	return res
}

func TestLoginThrottlesRepeatedFailuresPerClient(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "correct-horse-battery")
	a, err := newApp(t.TempDir()+"/login.db", false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c := newTestClient(t, a)
	for i := 0; i < loginFreeAttempts; i++ {
		if res := c.login("wrong-password-value"); res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d status=%d", i, res.StatusCode)
		}
	}
	res := c.login("correct-horse-battery")
	if res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") == "" {
		t.Fatalf("expected throttling after repeated failures, status=%d", res.StatusCode)
	}
	a.logins.reset(a.clientIP(httptest.NewRequest("GET", "/", nil)) + "|admin")
	var count int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='auth.login_failed'`).Scan(&count)
	if count != loginFreeAttempts {
		t.Fatalf("failed logins audited=%d", count)
	}
}

func TestProtectedRoutesRequireSessionAndCSRF(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "correct-horse-battery")
	a, err := newApp(t.TempDir()+"/csrf.db", false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c := newTestClient(t, a)
	if res := c.do(http.MethodGet, "/api/v1/accounts", ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous list status=%d", res.StatusCode)
	}
	if res := c.login("correct-horse-battery"); res.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d", res.StatusCode)
	}
	good := c.csrf
	c.csrf = "forged"
	if res := c.do(http.MethodPost, "/api/v1/proxies", `{"scheme":"http","host":"127.0.0.1","port":1}`); res.StatusCode != http.StatusForbidden {
		t.Fatalf("forged csrf status=%d", res.StatusCode)
	}
	c.csrf = good
	req, _ := http.NewRequest(http.MethodGet, c.server.URL+"/api/v1/accounts", nil)
	req.Header.Set("Origin", "https://evil.example")
	res, _ := c.client.Do(req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin status=%d", res.StatusCode)
	}
	res.Body.Close()
	if res := c.do(http.MethodGet, "/api/v1/audit-events", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("audit list status=%d", res.StatusCode)
	}
	if res := c.do(http.MethodGet, "/api/v1/not-a-route", ""); res.StatusCode != http.StatusNotFound || res.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("unknown route status=%d type=%s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if res := c.do(http.MethodPost, "/api/v1/auth/logout", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("logout status=%d", res.StatusCode)
	}
	if res := c.do(http.MethodGet, "/api/v1/accounts", ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout status=%d", res.StatusCode)
	}
}

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Fatalf("unexpected format %s", hash)
	}
	if !verifyPassword("correct-horse-battery", "", hash) || verifyPassword("correct-horse-batterx", "", hash) {
		t.Fatal("argon2 verification is wrong")
	}
	if verifyPassword("short", "short", "") {
		t.Fatal("plaintext secrets shorter than the minimum must never verify")
	}
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "")
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD_HASH", hash)
	cfg, err := LoadConfig()
	if err != nil || cfg.AdminPasswordHash != hash {
		t.Fatalf("hash configuration rejected: %v", err)
	}
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD_HASH", "$argon2id$v=19$m=1,t=1,p=1$AAAA$AAAA")
	if _, err = LoadConfig(); err == nil {
		t.Fatal("weak hash parameters were accepted")
	}
}

func TestSessionStoreBoundsAnonymousSessions(t *testing.T) {
	store := newSessionStore()
	for i := 0; i < maxSessions+5; i++ {
		store.put(randomString(), sessionState{expires: time.Now().Add(time.Minute)})
	}
	if store.len() > maxSessions {
		t.Fatalf("sessions=%d exceeds cap", store.len())
	}
	store.put("expired", sessionState{expires: time.Now().Add(-time.Second)})
	store.sweep(time.Now())
	if _, ok := store.get("expired"); ok {
		t.Fatal("expired session survived sweep")
	}
}

func TestWriteRateLimitReturns429PerUser(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "correct-horse-battery")
	t.Setenv("CC_MANAGER_WRITE_RATE_PER_MINUTE", "4") // burst floor of 5
	a, err := newApp(t.TempDir()+"/ratelimit.db", false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c := newTestClient(t, a)
	c.login("correct-horse-battery")
	limited := false
	for i := 0; i < 8; i++ {
		res := c.do(http.MethodPost, "/api/v1/proxies", `{"name":"p`+string(rune('a'+i))+`","scheme":"http","host":"127.0.0.1","port":`+strconv.Itoa(8080+i)+`}`)
		res.Body.Close()
		if res.StatusCode == http.StatusTooManyRequests {
			if res.Header.Get("Retry-After") == "" {
				t.Fatal("missing Retry-After")
			}
			if i < 5 {
				t.Fatalf("limited too early at request %d", i+1)
			}
			limited = true
			break
		}
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("request %d status=%d", i+1, res.StatusCode)
		}
	}
	if !limited {
		t.Fatal("expected a 429 after the burst")
	}
	// Reads are never limited.
	if res := c.do(http.MethodGet, "/api/v1/proxies", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("read status=%d", res.StatusCode)
	}
}

func TestWriteLimiterRefillsAndIgnoresZeroRate(t *testing.T) {
	l := newWriteLimiter(60) // 1 token/s, burst 15
	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < 15; i++ {
		if ok, _ := l.allow("u", base); !ok {
			t.Fatalf("burst exhausted at %d", i)
		}
	}
	if ok, wait := l.allow("u", base); ok || wait <= 0 || wait > time.Second {
		t.Fatalf("expected throttle, ok=%v wait=%s", ok, wait)
	}
	if ok, _ := l.allow("u", base.Add(2*time.Second)); !ok {
		t.Fatal("bucket did not refill")
	}
	if ok, _ := l.allow("other", base); !ok {
		t.Fatal("users must not share buckets")
	}
	off := newWriteLimiter(0)
	for i := 0; i < 1000; i++ {
		if ok, _ := off.allow("u", base); !ok {
			t.Fatal("rate 0 must disable the limiter")
		}
	}
	l.sweep(base.Add(time.Hour))
	if len(l.items) != 0 {
		t.Fatalf("sweep left %d buckets", len(l.items))
	}
}
