package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Hold the global slot until the handler has claimed the pending, making
// changes during its queue wait deterministic without timing the provider.
func queuedOAuthExchange(t *testing.T, a *App) (<-chan *httptest.ResponseRecorder, func(), context.CancelFunc) {
	t.Helper()
	a.oauthGate = newOAuthGate(1)
	release, err := a.oauthGate.acquire(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	r := retryExchangeRequest()
	ctx, cancel := context.WithCancel(r.Context())
	r = r.WithContext(ctx)
	done := make(chan *httptest.ResponseRecorder, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		w := httptest.NewRecorder()
		a.claudeOAuthExchange(w, r)
		done <- w
	}()
	t.Cleanup(func() {
		cancel()
		release()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("queued exchange did not stop")
		}
	})
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		a.oauth.mu.Lock()
		claimed := a.oauth.items["review"].exchanging
		a.oauth.mu.Unlock()
		if claimed {
			return done, release, cancel
		}
		select {
		case <-ticker.C:
		case <-finished:
			t.Fatal("exchange returned before claiming the pending")
		case <-timeout.C:
			t.Fatal("exchange never claimed the pending")
		}
	}
}

func queuedOAuthResponse(t *testing.T, done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case w := <-done:
		return w
	case <-time.After(3 * time.Second):
		t.Fatal("exchange did not finish after the queue was released")
		return nil
	}
}

func TestOAuthQueueRechecksPendingAndCooldown(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
		want int
	}{
		{"session_canceled", "OAUTH_SESSION_EXPIRED", http.StatusNotFound},
		{"session_expired", "OAUTH_SESSION_EXPIRED", http.StatusNotFound},
		{"cooldown_started", "OAUTH_RATE_LIMITED", http.StatusTooManyRequests},
		{"request_canceled", "OAUTH_BUSY", http.StatusTooManyRequests},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, _, _ := deliveryFixture(t)
			var calls atomic.Int32
			retryOAuthProxy(t, a, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "fixture temporary failure", http.StatusServiceUnavailable)
			})
			retrySeedPending(a, "")
			done, release, cancel := queuedOAuthExchange(t, a)
			switch tc.name {
			case "session_canceled":
				w := httptest.NewRecorder()
				a.claudeOAuthCancel(w, retryExchangeRequest())
				if w.Code != http.StatusOK {
					t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
				}
			case "session_expired":
				a.oauth.mu.Lock()
				p := a.oauth.items["review"]
				p.expires = time.Now().Add(-time.Second)
				a.oauth.items["review"] = p
				a.oauth.mu.Unlock()
			case "cooldown_started":
				a.oauthCooldown.extend(cooldownKey("", oauthOwner(retryExchangeRequest())), time.Now(), time.Minute)
			case "request_canceled":
				cancel()
			}
			release()
			response := queuedOAuthResponse(t, done)
			t.Logf("after queue: response=%d; provider requests=%d", response.Code, calls.Load())
			if response.Code != tc.want || !strings.Contains(response.Body.String(), tc.code) || calls.Load() != 0 {
				t.Fatalf("queue bypassed changed state: %d %s; calls=%d", response.Code, response.Body.String(), calls.Load())
			}
			if tc.name == "session_canceled" || tc.name == "session_expired" {
				if retry := retryExchange(a); retry.Code != http.StatusNotFound || calls.Load() != 0 {
					t.Fatalf("retired pending was revived: %d; calls=%d", retry.Code, calls.Load())
				}
				return
			}
			// Both queue cooldown and request cancellation must release the claim.
			a.oauthCooldown.mu.Lock()
			a.oauthCooldown.until = map[string]time.Time{}
			a.oauthCooldown.mu.Unlock()
			if retry := retryExchange(a); retry.Code != http.StatusBadGateway || calls.Load() != 1 {
				t.Fatalf("queue early return retained its claim: %d %s; calls=%d", retry.Code, retry.Body.String(), calls.Load())
			}
		})
	}
}

func TestOAuthQueueCachedTokensRemainBoundToAuthorizationCode(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	var tokenCalls, profileCalls atomic.Int32
	retryOAuthProxy(t, a, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			tokenCalls.Add(1)
			io.WriteString(w, `{"access_token":"fixture-access","refresh_token":"fixture-refresh","expires_in":3600}`)
			return
		}
		if profileCalls.Add(1) == 1 {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "fixture profile rate limit", http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, `{"account":{"uuid":"account-uuid","email":"review@example.test"},"organization":{"uuid":"org-uuid","name":"Fixture"}}`)
	})
	retrySeedPending(a, "")
	if first := retryExchange(a); first.Code != http.StatusTooManyRequests {
		t.Fatalf("profile did not pause: %d %s", first.Code, first.Body.String())
	}
	a.oauthCooldown.mu.Lock()
	a.oauthCooldown.until = map[string]time.Time{}
	a.oauthCooldown.mu.Unlock()
	r := retryExchangeRequest()
	r.Body = io.NopCloser(strings.NewReader(`{"result":"different-code"}`))
	wrongCode := httptest.NewRecorder()
	a.claudeOAuthExchange(wrongCode, r)
	if wrongCode.Code != http.StatusUnprocessableEntity || !strings.Contains(wrongCode.Body.String(), "OAUTH_RESULT_INVALID") || tokenCalls.Load() != 1 || profileCalls.Load() != 1 {
		t.Fatalf("different code used cached tokens: %d %s; POSTs=%d GETs=%d", wrongCode.Code, wrongCode.Body.String(), tokenCalls.Load(), profileCalls.Load())
	}
	if resumed := retryExchange(a); resumed.Code != http.StatusOK || tokenCalls.Load() != 1 || profileCalls.Load() != 2 {
		t.Fatalf("original code could not resume profile: %d %s; POSTs=%d GETs=%d", resumed.Code, resumed.Body.String(), tokenCalls.Load(), profileCalls.Load())
	}
}

func TestOAuthQueueCancelDuringProfileDoesNotRestorePending(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	var tokenCalls, profileCalls atomic.Int32
	profileEntered, profileRelease := make(chan struct{}), make(chan struct{})
	var once sync.Once
	releaseProfile := func() { once.Do(func() { close(profileRelease) }) }
	defer releaseProfile()
	retryOAuthProxy(t, a, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			tokenCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"access_token":"fixture-access","refresh_token":"fixture-refresh","expires_in":3600}`)
			return
		}
		if profileCalls.Add(1) == 1 {
			close(profileEntered)
			<-profileRelease
		}
		w.Header().Set("Retry-After", "60")
		http.Error(w, "fixture profile rate limit", http.StatusTooManyRequests)
	})
	retrySeedPending(a, "")
	done := make(chan *httptest.ResponseRecorder, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- retryExchange(a)
	}()
	t.Cleanup(func() {
		releaseProfile()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("profile exchange did not stop")
		}
	})
	select {
	case <-profileEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("exchange never reached profile")
	}
	a.claudeOAuthCancel(httptest.NewRecorder(), retryExchangeRequest())
	releaseProfile()
	first := queuedOAuthResponse(t, done)
	if first.Code != http.StatusTooManyRequests {
		t.Fatalf("profile response: %d %s", first.Code, first.Body.String())
	}
	if retry := retryExchange(a); retry.Code != http.StatusNotFound || tokenCalls.Load() != 1 || profileCalls.Load() != 1 {
		t.Fatalf("profile cache restored canceled pending: %d %s; POSTs=%d GETs=%d", retry.Code, retry.Body.String(), tokenCalls.Load(), profileCalls.Load())
	}
}
