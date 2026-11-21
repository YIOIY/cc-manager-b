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
	if err != nil || len(result.Warnings) == 0 || result.Auth["email"] != nil || result.Auth["organization_name"] != nil {
		t.Fatalf("partial result: %+v %v", result, err)
	}
	if rolesCalls != 0 {
		t.Fatalf("roles called after profile failure: %d", rolesCalls)
	}
	if se, ok := AsStatusError(err); ok {
		t.Fatalf("unexpected status error: %v", se)
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
		_, err := Exchange(context.Background(), c, "code", "state", "verifier")
		se, ok := AsStatusError(err)
		if !ok || se.Status != status || se.Op != "profile" || rolesCalls != 0 {
			t.Fatalf("status %d: err=%v roles=%d", status, err, rolesCalls)
		}
		if status == http.StatusTooManyRequests && se.RetryAfter != 12*time.Second {
			t.Fatalf("retry-after: %v", se.RetryAfter)
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
