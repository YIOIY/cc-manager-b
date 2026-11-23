package claudeoauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAuthorizationPKCEAndResultParsing(t *testing.T) {
	p, err := NewPending()
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(p.URL)
	q := u.Query()
	h := sha256.Sum256([]byte(p.Verifier))
	if q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(h[:]) || q.Get("code_challenge_method") != "S256" || q.Get("state") != p.State {
		t.Fatal("invalid PKCE")
	}
	if q.Get("redirect_uri") != RedirectURL || q.Get("client_id") != ClientID || len(p.Verifier) < 43 {
		t.Fatal("invalid authorization parameters")
	}
	for _, raw := range []string{"the-code", "the-code#" + p.State, RedirectURL + "?code=the-code&state=" + p.State} {
		code, err := ParseResult(raw, p.State)
		if err != nil || code != "the-code" {
			t.Fatalf("parse: %s, %v", code, err)
		}
	}
	for _, raw := range []string{"", "code#wrong", "https://evil.test/?code=x&state=" + p.State, RedirectURL + "?code=x", RedirectURL + "?error=access_denied", "a b", "x#" + p.State + "#extra"} {
		if _, err := ParseResult(raw, p.State); err == nil {
			t.Fatalf("accepted invalid result: %q", raw)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	if d := parseRetryAfter(http.Header{"Retry-After": []string{"45"}}, now); d != 45*time.Second {
		t.Fatalf("seconds: %v", d)
	}
	if d := parseRetryAfter(http.Header{"Retry-After": []string{"0"}}, now); d != 0 {
		t.Fatalf("zero: %v", d)
	}
	if d := parseRetryAfter(http.Header{"Retry-After": []string{"not-a-value"}}, now); d != 0 {
		t.Fatalf("invalid: %v", d)
	}
	at := now.Add(90 * time.Second).Format(http.TimeFormat)
	if d := parseRetryAfter(http.Header{"Retry-After": []string{at}}, now); d < 89*time.Second || d > 91*time.Second {
		t.Fatalf("http-date: %v", d)
	}
	past := now.Add(-time.Minute).Format(http.TimeFormat)
	if d := parseRetryAfter(http.Header{"Retry-After": []string{past}}, now); d != 0 {
		t.Fatalf("past http-date: %v", d)
	}
}

func TestExchangeMapsActualFieldsAndKeepsUnknownsMissing(t *testing.T) {
	var tokenBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/oauth/token":
			_ = json.NewDecoder(r.Body).Decode(&tokenBody)
			io.WriteString(w, `{"access_token":"access","refresh_token":"refresh","expires_in":3600,"account":{"uuid":"account-id","email_address":"test@example.com"},"organization":{"uuid":"org-id"}}`)
		case "/api/oauth/profile":
			if r.Header.Get("Authorization") != "Bearer access" {
				t.Error("missing bearer")
			}
			io.WriteString(w, `{"account":{"uuid":"account-id","email":"test@example.com","created_at":"2025-01-02T03:00:00Z"},"organization":{"uuid":"org-id","rate_limit_tier":"default_claude_max_5x","subscription_created_at":"2025-02-01T00:00:00Z"}}`)
		case "/api/oauth/claude_cli/roles":
			io.WriteString(w, `{"organization_name":"My org"}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	c := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})}
	before := time.Now()
	result, err := Exchange(context.Background(), c, "code", "state", "verifier")
	if err != nil {
		t.Fatal(err)
	}
	if tokenBody["code_verifier"] != "verifier" || tokenBody["state"] != "state" || tokenBody["redirect_uri"] != RedirectURL {
		t.Fatal(tokenBody)
	}
	if result.Auth["email"] != "test@example.com" || result.Auth["organization_name"] != "My org" || result.Plan != "claude/cc-max-5x" || result.RegisterTime != "2025-01-02" {
		t.Fatalf("mapping: %+v", result)
	}
	for _, key := range []string{"claude_device_ids", "proxy_url", "password", "plan_start_time", "plan_end_time"} {
		if _, ok := result.Auth[key]; ok {
			t.Fatalf("fabricated %s", key)
		}
	}
	expires, _ := time.Parse(time.RFC3339, result.Auth["expired"].(string))
	if expires.Before(before.Add(3599 * time.Second)) {
		t.Fatal("wrong expiry")
	}
}

func TestExchangeDoesNotLeakUpstreamSecretsOrInventIdentity(t *testing.T) {
	for _, body := range []string{`{"error":"secret-code"}`, `{"access_token":"secret-token","expires_in":0}`} {
		c := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}
		_, err := Exchange(context.Background(), c, "secret-code", "state", "verifier")
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
	rolesCalls := 0
	c := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, status := `{"access_token":"a","refresh_token":"r","expires_in":3600}`, 200
		if r.Method == "GET" {
			if strings.Contains(r.URL.Path, "roles") {
				rolesCalls++
			}
			body, status = "unavailable secret-token", 503
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	result, err := Exchange(context.Background(), c, "code", "state", "verifier")
	if err == nil || result.Auth["access_token"] != "a" || result.Auth["email"] != nil || result.Auth["organization_name"] != nil {
		t.Fatalf("partial result: %+v %v", result, err)
	}
	if rolesCalls != 0 {
		t.Fatalf("roles called after profile failure: %d", rolesCalls)
	}
	if se, ok := AsStatusError(err); !ok || se.Op != "profile" || se.Status != 503 {
		t.Fatalf("profile stage not exposed: %v", err)
	}
}

func TestExchangeRejectsRestrictiveProfileStatuses(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		rolesCalls := 0
		c := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body := `{"access_token":"a","refresh_token":"r","expires_in":3600}`
			hdr := make(http.Header)
			code := 200
			if r.Method == "GET" {
				if strings.Contains(r.URL.Path, "roles") {
					rolesCalls++
				}
				body = `{"error":"denied"}`
				code = status
				if status == http.StatusTooManyRequests {
					hdr.Set("Retry-After", "12")
				}
			}
			return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: hdr}, nil
		})}
		result, err := Exchange(context.Background(), c, "code", "state", "verifier")
		se, ok := AsStatusError(err)
		if !ok || se.Status != status || se.Op != "profile" || rolesCalls != 0 {
			t.Fatalf("status %d: err=%v roles=%d", status, err, rolesCalls)
		}
		if status == http.StatusTooManyRequests && se.RetryAfter != 12*time.Second {
			t.Fatalf("retry-after: %v", se.RetryAfter)
		}
		if result.Auth["access_token"] != "a" {
			t.Fatalf("profile status %d discarded completed token stage", status)
		}
		if strings.Contains(err.Error(), "denied") {
			t.Fatalf("leaked body: %v", err)
		}
	}
}

func TestExchangeRejectsRedirectsAndIgnoresMismatchedProfile(t *testing.T) {
	redirectCalls := 0
	c := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		redirectCalls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://other.example/token"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	if _, err := Exchange(context.Background(), c, "code", "state", "verifier"); err == nil || redirectCalls != 1 {
		t.Fatalf("redirect followed: %d, %v", redirectCalls, err)
	}
	rolesCalls := 0
	c.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"access_token":"a","refresh_token":"r","expires_in":3600,"account":{"uuid":"correct","email_address":"correct@example.com"},"organization":{"uuid":"correct-org"}}`
		if r.URL.Path == "/api/oauth/profile" {
			body = `{"account":{"uuid":"other","email":"other@example.com","created_at":"2020-01-01T00:00:00Z"},"organization":{"uuid":"other-org","name":"Wrong org","rate_limit_tier":"default_claude_max_20x"}}`
		}
		if r.URL.Path == "/api/oauth/claude_cli/roles" {
			rolesCalls++
			body = `{}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	result, err := Exchange(context.Background(), c, "code", "state", "verifier")
	if err != nil || result.Auth["email"] != "correct@example.com" || result.Plan != "" || result.RegisterTime != "" || result.Auth["organization_name"] != nil || len(result.Warnings) == 0 {
		t.Fatalf("mixed identities: %+v, %v", result, err)
	}
	if rolesCalls != 0 {
		t.Fatalf("roles called after identity mismatch: %d", rolesCalls)
	}
}

func TestStatusErrorAs(t *testing.T) {
	err := error(&StatusError{Op: "token", Status: 429, RetryAfter: time.Minute})
	se, ok := AsStatusError(err)
	if !ok || se.Status != 429 {
		t.Fatal(se, ok)
	}
	if !errors.As(err, &se) {
		t.Fatal("errors.As failed")
	}
}

func TestExchangeResumesRateLimitedProfileWithoutRedeemingCodeAgain(t *testing.T) {
	var tokenCalls, profileCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/oauth/token":
			if tokenCalls.Add(1) != 1 {
				http.Error(w, "authorization code already used", http.StatusBadRequest)
				return
			}
			io.WriteString(w, `{"access_token":"resume-access","refresh_token":"resume-refresh","expires_in":3600,"account":{"uuid":"account-id","email_address":"token@example.com"},"organization":{"uuid":"org-id"}}`)
		case "/api/oauth/profile":
			if r.Header.Get("Authorization") != "Bearer resume-access" {
				t.Error("profile request did not use the exchanged token")
			}
			if profileCalls.Add(1) == 1 {
				w.Header().Set("Retry-After", "12")
				http.Error(w, "secret-upstream-body", http.StatusTooManyRequests)
				return
			}
			io.WriteString(w, `{"account":{"uuid":"account-id","email":"profile@example.com","created_at":"2025-01-02T03:00:00Z"},"organization":{"uuid":"org-id","name":"Resumed org","rate_limit_tier":"default_claude_max_5x"}}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})}

	pending, err := Exchange(context.Background(), client, "single-use-code", "state", "verifier")
	se, ok := AsStatusError(err)
	if !ok || se.Op != "profile" || se.Status != http.StatusTooManyRequests || se.RetryAfter != 12*time.Second {
		t.Fatalf("expected profile cooldown: %v", err)
	}
	if pending.Auth["access_token"] != "resume-access" || pending.Auth["refresh_token"] != "resume-refresh" {
		t.Fatal("profile cooldown discarded the successfully exchanged tokens")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("upstream body leaked: %v", err)
	}

	result, err := CompleteProfile(context.Background(), client, pending)
	if err != nil {
		t.Fatal(err)
	}
	if result.Auth["email"] != "profile@example.com" || result.Auth["organization_name"] != "Resumed org" || result.Plan != "claude/cc-max-5x" || result.RegisterTime != "2025-01-02" {
		t.Fatalf("profile was not completed: plan=%q registered=%q", result.Plan, result.RegisterTime)
	}
	if tokenCalls.Load() != 1 || profileCalls.Load() != 2 {
		t.Fatalf("token requests=%d profile requests=%d; want 1 and 2", tokenCalls.Load(), profileCalls.Load())
	}
	if pending.Auth["email"] != "token@example.com" || pending.Auth["organization_name"] != nil || pending.Plan != "" || pending.RegisterTime != "" {
		t.Fatal("completing the profile mutated the cached token result")
	}
}

func TestExchangeMarksInvalidTokenResponsesTerminal(t *testing.T) {
	for name, body := range map[string]string{
		"invalid JSON":    `{"access_token":"secret-token"`,
		"missing tokens":  `{}`,
		"missing refresh": `{"access_token":"secret-token","expires_in":3600}`,
		"invalid expiry":  `{"access_token":"secret-token","refresh_token":"secret-refresh","expires_in":0}`,
		"oversized body":  strings.Repeat("x", (1<<20)+1),
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			result, err := Exchange(context.Background(), client, "secret-code", "state", "verifier")
			re, ok := AsResponseError(err)
			if !ok || re.Op != "token" || result.Auth != nil || calls != 1 {
				t.Fatalf("expected terminal token response failure after one request; err=%v requests=%d", err, calls)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("response error leaked credentials: %v", err)
			}
		})
	}
}

func TestCompleteProfileRejectsUnusableCachedTokensBeforeRequest(t *testing.T) {
	for name, field := range map[string]struct{ key, value string }{
		"expired":         {"expired", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)},
		"invalid expiry":  {"expired", "not-a-time"},
		"missing expiry":  {"expired", ""},
		"missing access":  {"access_token", ""},
		"missing refresh": {"refresh_token", ""},
	} {
		t.Run(name, func(t *testing.T) {
			cached := Result{Auth: map[string]any{
				"access_token": "cached-access", "refresh_token": "cached-refresh",
				"expired": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			}}
			cached.Auth[field.key] = field.value
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"account":{"uuid":"account-id"},"organization":{"uuid":"org-id","name":"Org"}}`))}, nil
			})}
			result, err := CompleteProfile(context.Background(), client, cached)
			// Unusable cached tokens are a terminal condition for the session,
			// distinct from a malformed profile response (which is retryable).
			if !errors.Is(err, ErrTokenExpired) || result.Auth != nil || calls != 0 {
				t.Fatalf("expected terminal profile failure without a request; err=%v requests=%d", err, calls)
			}
			if _, isResponse := AsResponseError(err); isResponse {
				t.Fatalf("expired cache must not look like a retryable response error: %v", err)
			}
			if strings.Contains(err.Error(), "cached-") {
				t.Fatalf("cached credentials leaked: %v", err)
			}
		})
	}
}

func TestCompleteProfileChecksCachedAccountAndOrganization(t *testing.T) {
	for name, body := range map[string]string{
		"different account":      `{"account":{"uuid":"other","email":"other@example.com"},"organization":{"uuid":"cached-org","name":"Other org"}}`,
		"different organization": `{"account":{"uuid":"cached-account","email":"other@example.com"},"organization":{"uuid":"other","name":"Other org"}}`,
		"missing identity":       `{"account":{"email":"other@example.com"},"organization":{"uuid":"cached-org","name":"Other org"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			cached := Result{Auth: map[string]any{
				"access_token": "cached-access", "refresh_token": "cached-refresh",
				"expired":      time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
				"account_uuid": "cached-account", "organization_uuid": "cached-org", "email": "cached@example.com",
			}}
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/api/oauth/profile" || r.Header.Get("Authorization") != "Bearer cached-access" {
					t.Error("unexpected request while checking cached identity")
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			result, err := CompleteProfile(context.Background(), client, cached)
			if err != nil || result.Auth["account_uuid"] != "cached-account" || result.Auth["organization_uuid"] != "cached-org" || result.Auth["email"] != "cached@example.com" || result.Auth["organization_name"] != nil || result.Plan != "" || result.RegisterTime != "" || len(result.Warnings) != 1 || calls != 1 {
				t.Fatalf("mismatched profile changed the cached identity or requested roles: err=%v requests=%d", err, calls)
			}
		})
	}
}

func TestCompleteProfileTransientFailuresKeepIsolatedPartialResults(t *testing.T) {
	for _, failure := range []string{"network", "server", "malformed response"} {
		t.Run(failure, func(t *testing.T) {
			backingWarnings := []string{"existing warning", "unused cached slot"}
			cached := Result{Auth: map[string]any{
				"access_token": "cached-access", "refresh_token": "cached-refresh",
				"expired": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			}, Warnings: backingWarnings[:1]}
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if failure == "network" {
					return nil, errors.New("secret-token in proxy error")
				}
				status := http.StatusOK
				if failure == "server" {
					status = http.StatusServiceUnavailable
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("secret-token in upstream body"))}, nil
			})}
			result, err := CompleteProfile(context.Background(), client, cached)
			if err == nil || result.Auth["access_token"] != "cached-access" || len(result.Warnings) != 1 || calls != 1 {
				t.Fatalf("transient profile failure lost the partial result: err=%v requests=%d", err, calls)
			}
			if strings.Contains(strings.Join(result.Warnings, " "), "secret-token") {
				t.Fatal("warning leaked credentials")
			}
			if backingWarnings[1] != "unused cached slot" {
				t.Fatal("warning append overwrote cached backing storage")
			}
			result.Auth["access_token"] = "changed"
			result.Warnings[0] = "changed"
			if cached.Auth["access_token"] != "cached-access" || cached.Warnings[0] != "existing warning" {
				t.Fatal("partial result shares mutable storage with cached result")
			}
		})
	}
}

func TestCompleteProfileDoesNotFollowRedirects(t *testing.T) {
	cached := Result{Auth: map[string]any{
		"access_token": "cached-access", "refresh_token": "cached-refresh",
		"expired": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}}
	requests, callerRedirects := 0, 0
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { callerRedirects++; return nil },
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://other.example/profile"}}, Body: io.NopCloser(strings.NewReader("secret-upstream-body")), Request: r}, nil
		}),
	}
	result, err := CompleteProfile(context.Background(), client, cached)
	if err == nil || requests != 1 || callerRedirects != 0 || result.Auth["access_token"] != "cached-access" {
		t.Fatalf("profile redirect was followed or lost the partial result: err=%v requests=%d redirects=%d", err, requests, callerRedirects)
	}
	if err := client.CheckRedirect(nil, nil); err != nil || callerRedirects != 1 {
		t.Fatal("profile completion changed the caller's redirect policy")
	}
}

func TestExchangeTokenTransportFailureDoesNotClaimAResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("secret-token and secret-code in transport error")
	})}
	result, err := Exchange(context.Background(), client, "secret-code", "state", "verifier")
	if err == nil || result.Auth != nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe token transport failure: %v", err)
	}
	if _, ok := AsResponseError(err); ok {
		t.Fatalf("transport failure was classified as an invalid response: %v", err)
	}
}
