package app

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The provider and CONNECT proxy are both loopback fixtures. No Anthropic calls.
func retryOAuthProxy(t *testing.T, a *App, handler http.HandlerFunc) {
	t.Helper()
	remote := httptest.NewTLSServer(handler)
	t.Cleanup(remote.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target, err := net.Dial("tcp", remote.Listener.Addr().String())
		if err != nil {
			http.Error(w, "fixture dial failed", 502)
			return
		}
		hijacked, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			target.Close()
			return
		}
		defer hijacked.Close()
		defer target.Close()
		fmt.Fprint(buf, "HTTP/1.1 200 Connection Established\r\n\r\n")
		buf.Flush()
		go io.Copy(target, hijacked)
		io.Copy(hijacked, target)
	}))
	t.Cleanup(proxy.Close)
	endpoint, _ := url.Parse(proxy.URL)
	port, _ := strconv.Atoi(endpoint.Port())
	if _, err := a.DB.Exec(`INSERT INTO proxies(id,scheme,host,port,enabled) VALUES('review-proxy','http','127.0.0.1',?,1)`, port); err != nil {
		t.Fatal(err)
	}
	originalTransport := http.DefaultTransport
	transport := originalTransport.(*http.Transport).Clone()
	roots := x509.NewCertPool()
	roots.AddCert(remote.Certificate())
	// Only the loopback fixture certificate is trusted; no insecure TLS setting.
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: "example.com"}
	http.DefaultTransport = transport
	t.Cleanup(func() { transport.CloseIdleConnections(); http.DefaultTransport = originalTransport })
}
func retryExchangeRequest() *http.Request {
	r := withAdminSession(httptest.NewRequest("POST", "/api/v1/claude-oauth/sessions/review/exchange", strings.NewReader(`{"result":"review-code"}`)))
	r.SetPathValue("id", "review")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "review-cookie"})
	return r
}
func retrySeedPending(a *App, accountID string) {
	r := retryExchangeRequest()
	a.oauth.items = map[string]oauthPending{"review": {owner: oauthOwner(r), expires: time.Now().Add(time.Minute), proxyID: "review-proxy", accountID: accountID}}
}
func retryExchange(a *App) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.claudeOAuthExchange(w, retryExchangeRequest())
	return w
}

func TestOAuthRetrySameSessionMustNotExchangeConcurrently(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	retryOAuthProxy(t, a, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		http.Error(w, "fixture temporary failure", 503)
	})
	retrySeedPending(a, "")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- retryExchange(a) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("first request never reached local provider")
	}
	second := retryExchange(a)
	close(release)
	first := <-done
	if first.Code != 502 || second.Code != 429 || !strings.Contains(second.Body.String(), "OAUTH_BUSY") || calls.Load() != 1 {
		t.Fatalf("same code sent concurrently: provider POSTs=%d; responses=%d,%d", calls.Load(), first.Code, second.Code)
	}
	// A retryable failure must release the session claim.
	retry := retryExchange(a)
	if retry.Code != 502 || calls.Load() != 2 {
		t.Fatalf("retry remained locked: %d calls=%d", retry.Code, calls.Load())
	}
}

func TestOAuthRetryNewAccount429MustEnforceServerCooldown(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	var calls atomic.Int32
	retryOAuthProxy(t, a, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "120")
		http.Error(w, "fixture rate limit", 429)
	})
	retrySeedPending(a, "")
	first, second := retryExchange(a), retryExchange(a)
	t.Logf("first=%d; immediate retry=%d; provider POSTs=%d; stored cooldowns=%d", first.Code, second.Code, calls.Load(), len(a.oauthCooldown.until))
	if calls.Load() != 1 {
		t.Fatalf("new-account immediate retry reached provider during Retry-After; POSTs=%d", calls.Load())
	}
}

func TestOAuthRetryProfile429MustNotReplayConsumedCode(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	insertLocalAccount(t, a, "review-account", "review@example.test")
	var tokenCalls, profileCalls atomic.Int32
	retryOAuthProxy(t, a, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			if tokenCalls.Add(1) > 1 {
				http.Error(w, "fixture invalid_grant: code already consumed", 400)
				return
			}
			io.WriteString(w, `{"access_token":"fixture-access","refresh_token":"fixture-refresh","expires_in":3600}`)
			return
		}
		if profileCalls.Add(1) == 1 {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "fixture profile rate limit", 429)
			return
		}
		io.WriteString(w, `{"account":{"uuid":"account-uuid","email":"review@example.test"},"organization":{"uuid":"org-uuid","name":"Fixture"}}`)
	})
	retrySeedPending(a, "review-account")
	first := retryExchange(a)
	if first.Code != 429 || strings.Contains(first.Body.String(), "fixture-access") {
		t.Fatalf("first response=%d %s", first.Code, first.Body.String())
	}
	a.oauthCooldown.mu.Lock()
	a.oauthCooldown.until = map[string]time.Time{}
	a.oauthCooldown.mu.Unlock() // advance beyond cooldown
	second := retryExchange(a)
	if second.Code != 200 || tokenCalls.Load() != 1 || profileCalls.Load() != 2 || !strings.Contains(second.Body.String(), "fixture-access") {
		t.Fatalf("profile retry must reuse tokens: response=%d; token POSTs=%d; profile GETs=%d", second.Code, tokenCalls.Load(), profileCalls.Load())
	}
}

func retryStart(a *App, body string) *httptest.ResponseRecorder {
	r := retryExchangeRequest()
	r.Body = io.NopCloser(strings.NewReader(body))
	w := httptest.NewRecorder()
	a.claudeOAuthStart(w, r)
	return w
}

func TestOAuthRetryNewAccountCannotRefreshOutOfCooldown(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	var calls atomic.Int32
	retryOAuthProxy(t, a, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "120")
		http.Error(w, "busy", 429)
	})
	retrySeedPending(a, "")
	first := retryExchange(a)
	if first.Code != 429 {
		t.Fatalf("initial=%d", first.Code)
	}
	refreshed := retryStart(a, `{"proxy_id":"review-proxy","refresh":true}`)
	if refreshed.Code != 429 {
		t.Fatalf("refresh bypassed new-account cooldown: %d", refreshed.Code)
	}
	reused := retryStart(a, `{"proxy_id":"review-proxy"}`)
	var env struct {
		Data struct {
			ID   string `json:"id"`
			Wait int    `json:"retry_after_seconds"`
		} `json:"data"`
	}
	json.Unmarshal(reused.Body.Bytes(), &env)
	if reused.Code != 200 || env.Data.ID != "review" || env.Data.Wait < 119 {
		t.Fatalf("cooldown must retain original session: %d %s", reused.Code, reused.Body.String())
	}
	a.claudeOAuthCancel(httptest.NewRecorder(), retryExchangeRequest())
	restarted := retryStart(a, `{"proxy_id":"review-proxy"}`)
	if restarted.Code != 429 || calls.Load() != 1 {
		t.Fatalf("cancel bypassed cooldown: %d calls=%d", restarted.Code, calls.Load())
	}
}

func TestOAuthRetryTerminalFailureMarksSessionExpired(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			a, _, _, _ := deliveryFixture(t)
			// The provider answers in JSON; a non-JSON 401/403 is an interception
			// and keeps the session (TestOAuthTokenInterceptedKeepsSession).
			retryOAuthProxy(t, a, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				w.Write([]byte(`{"error":"fixture terminal failure"}`))
			})
			retrySeedPending(a, "")
			res := retryExchange(a)
			var env struct {
				Error struct {
					Details map[string]any `json:"details"`
				} `json:"error"`
			}
			json.Unmarshal(res.Body.Bytes(), &env)
			if env.Error.Details["oauth_session_expired"] != true {
				t.Fatalf("frontend cannot recognize retired pending: %d %s", res.Code, res.Body.String())
			}
			if _, ok := a.oauth.items["review"]; ok {
				t.Fatal("terminal pending survived")
			}
		})
	}
}

// A proxy, WAF or captive portal answering the token POST with its own block
// page never forwarded the code. The session must survive so the operator can
// retry or switch exits without redoing the browser login.
func TestOAuthTokenInterceptedKeepsSession(t *testing.T) {
	for _, status := range []int{401, 403} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			a, _, _, _ := deliveryFixture(t)
			retryOAuthProxy(t, a, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(status)
				w.Write([]byte("<html><body>Access denied by gateway</body></html>"))
			})
			retrySeedPending(a, "")
			res := retryExchange(a)
			var env struct {
				Error struct {
					Code    string         `json:"code"`
					Details map[string]any `json:"details"`
				} `json:"error"`
			}
			json.Unmarshal(res.Body.Bytes(), &env)
			if env.Error.Code != "OAUTH_PROXY_UNREACHABLE" {
				t.Fatalf("interception blamed on identity: %d %s", res.Code, res.Body.String())
			}
			if env.Error.Details["oauth_session_expired"] == true || env.Error.Details["oauth_stage"] != "token" || env.Error.Details["upstream_status"] != float64(status) {
				t.Fatalf("details: %v", env.Error.Details)
			}
			if strings.Contains(res.Body.String(), "Access denied") {
				t.Fatal("block page leaked into response")
			}
			if _, ok := a.oauth.items["review"]; !ok {
				t.Fatal("intercepted exchange retired the pending")
			}
		})
	}
}
