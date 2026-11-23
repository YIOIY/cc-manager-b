package claudeoauth

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func cachedResult() Result {
	return Result{Auth: map[string]any{
		"access_token": "private-access", "refresh_token": "private-refresh",
		"expired":      time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"account_uuid": "account", "organization_uuid": "organization",
	}, Warnings: []string{}}
}

func TestProfileFailuresKeepTokenStageForRetry(t *testing.T) {
	for _, status := range []int{0, 401, 403, 429, 503, 200} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			cached := cachedResult()
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if status == 0 {
					return nil, errors.New("private-access in transport failure")
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("private-access invalid JSON"))}, nil
			})}
			result, err := CompleteProfile(context.Background(), client, cached)
			if err == nil || result.Auth["access_token"] != "private-access" {
				t.Fatalf("failed profile must keep tokens and return a stage error, status=%d err=%v", status, err)
			}
			if strings.Contains(err.Error(), "private-") {
				t.Fatal("error leaked secret")
			}
		})
	}
}

func TestRolesFailureResumesRolesOnly(t *testing.T) {
	profileCalls, rolesCalls := 0, 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status, body := 200, `{"account":{"uuid":"account","email":"mail@example.test"},"organization":{"uuid":"organization"}}`
		switch r.URL.Path {
		case "/api/oauth/profile":
			profileCalls++
		case "/api/oauth/claude_cli/roles":
			rolesCalls++
			if rolesCalls == 1 {
				status, body = 429, `{}`
			} else {
				body = `{"organization_name":"Recovered"}`
			}
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	partial, err := CompleteProfile(context.Background(), client, cachedResult())
	if se, ok := AsStatusError(err); !ok || se.Op != "roles" || se.Status != 429 {
		t.Fatalf("roles rate limit not exposed: %v", err)
	}
	result, err := CompleteProfile(context.Background(), client, partial)
	if err != nil || profileCalls != 1 || rolesCalls != 2 || result.Auth["organization_name"] != "Recovered" {
		t.Fatalf("resume repeated completed stage: profile=%d roles=%d err=%v", profileCalls, rolesCalls, err)
	}
}

func TestProbeClassifiesRejectionAndInvalidResponse(t *testing.T) {
	for _, status := range []int{401, 403, 429, 503, 200, 405} {
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": []string{"7200"}}, Body: io.NopCloser(strings.NewReader("not JSON"))}, nil
		})}
		err := Reachable(context.Background(), client)
		switch status {
		case 405:
			if err != nil {
				t.Fatalf("405 should prove reachability: %v", err)
			}
		case 429:
			if busy, ok := AsReachBusy(err); !ok || busy.RetryAfter != 2*time.Hour {
				t.Fatalf("429: %v", err)
			}
		case 200:
			if response, ok := AsResponseError(err); !ok || response.Op != "reach" {
				t.Fatalf("invalid response misclassified: %v", err)
			}
		default:
			// 401/403 without a JSON body: something other than Claude answered.
			if se, ok := AsStatusError(err); !ok || se.Status != status || se.Op != "reach" {
				t.Fatalf("probe status %d hidden: %v", status, err)
			}
		}
	}
}

// The live token endpoint answers a bare GET with HTTP 403 and a JSON error
// body from the API itself (verified 2026-11-23: {"error":{"type":"forbidden",
// "message":"Request not allowed"}}). That is proof the exit reaches Claude,
// not a blocked proxy; treating it as unreachable would refuse every pool proxy.
func TestProbeAcceptsJSONAccessRefusalAsReachable(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"forbidden","message":"Request not allowed"}}`))}, nil
		})}
		if err := Reachable(context.Background(), client); err != nil {
			t.Fatalf("JSON %d from the API must count as reachable: %v", status, err)
		}
	}
}

func TestRetryAfterSaturatesInsteadOfWrapping(t *testing.T) {
	for _, raw := range []string{"9223372036854775807", "999999999999999999999999999999"} {
		if got := parseRetryAfter(http.Header{"Retry-After": []string{raw}}, time.Now()); got != time.Duration(math.MaxInt64) {
			t.Fatalf("large delay wrapped or vanished: %s -> %v", raw, got)
		}
	}
	if got := parseRetryAfter(http.Header{"Retry-After": []string{"86400"}}, time.Now()); got != 24*time.Hour {
		t.Fatalf("normal delay capped: %v", got)
	}
}

func TestOAuthRequestsUseStableApplicationHeaders(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("User-Agent") != defaultUserAgent || r.Header.Get("Accept-Language") != defaultAcceptLanguage || r.Header.Get("X-CC-Manager-OAuth-Version") != "" {
			t.Errorf("missing stable headers for %s: UA=%q lang=%q custom=%q", r.URL.Path, r.Header.Get("User-Agent"), r.Header.Get("Accept-Language"), r.Header.Get("X-CC-Manager-OAuth-Version"))
		}
		body := `{"access_token":"a","refresh_token":"r","expires_in":3600}`
		if r.URL.Path == "/api/oauth/profile" {
			body = `{"account":{"uuid":"a"},"organization":{"uuid":"o","name":"Org"}}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if _, err := Exchange(context.Background(), client, "code", "state", "verifier"); err != nil {
		t.Fatal(err)
	}
	if err := Reachable(context.Background(), client); err != nil {
		t.Fatal(err)
	}
}

func TestTokenStageDoesNotRequestProfile(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/v1/oauth/token" {
			t.Fatal("Token requested another stage")
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "refresh_token") {
			t.Fatal("authorization_code grant carried old refresh token")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"a","refresh_token":"r","expires_in":3600}`))}, nil
	})}
	result, err := Token(context.Background(), client, "one-code", "state", "verifier")
	if err != nil || calls != 1 || result.Auth["access_token"] != "a" || result.ProfileLoaded {
		t.Fatalf("wrong token stage: %v requests=%d", err, calls)
	}
}

func TestTransportRetryRequiresProofOfConnectionFailure(t *testing.T) {
	// A real refused connection emits an establishment failure trace. A custom
	// transport or a connection lost after request transmission cannot prove it.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	for _, scenario := range []string{"connect refused", "unknown transport", "response lost"} {
		t.Run(scenario, func(t *testing.T) {
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = nil
			defer transport.CloseIdleConnections()
			client := &http.Client{Timeout: time.Second}
			switch scenario {
			case "connect refused":
				client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					r.URL.Scheme, r.URL.Host = "http", address
					return transport.RoundTrip(r)
				})
			case "unknown transport":
				client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) { return nil, errors.New("secret-code") })
			case "response lost":
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					io.Copy(io.Discard, r.Body)
					connection, _, e := w.(http.Hijacker).Hijack()
					if e != nil {
						t.Error(e)
						return
					}
					connection.Close()
				}))
				defer server.Close()
				target, _ := url.Parse(server.URL)
				client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
					return transport.RoundTrip(r)
				})
			}
			_, err := Token(context.Background(), client, "secret-code", "state", "verifier")
			failure, ok := AsTransportError(err)
			if !ok || failure.Op != "token" || failure.BeforeWrite != (scenario == "connect refused") || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe retry classification: %v (%+v)", err, failure)
			}
		})
	}
}

func TestObserverReportsEveryStageOnceAndClassifiesFailures(t *testing.T) {
	var observations []Observation
	ctx := WithObserver(context.Background(), func(o Observation) { observations = append(observations, o) })
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"access_token":"a","refresh_token":"r","expires_in":3600}`
		if r.URL.Path == "/api/oauth/profile" {
			body = `{"account":{"uuid":"a"},"organization":{"uuid":"o"}}`
		}
		if strings.HasSuffix(r.URL.Path, "/roles") {
			body = `{"organization_name":"Org"}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if err := Reachable(ctx, client); err != nil {
		t.Fatal(err)
	}
	if _, err := Exchange(ctx, client, "code", "state", "verifier"); err != nil {
		t.Fatal(err)
	}
	if len(observations) != 4 {
		t.Fatalf("observations=%d want 4", len(observations))
	}
	for i, stage := range []string{"reach", "token", "profile", "roles"} {
		o := observations[i]
		if o.Stage != stage || o.Status != 200 || o.Duration <= 0 || o.Kind != "success" {
			t.Fatalf("bad observation: %+v", o)
		}
	}
	for _, scenario := range []string{"transport", "response", "status"} {
		client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if scenario == "transport" {
				return nil, errors.New("private-token")
			}
			status := 200
			if scenario == "status" {
				status = 429
			}
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		})
		if _, err := Token(ctx, client, "code", "state", "verifier"); err == nil {
			t.Fatalf("%s expected failure", scenario)
		}
		if observations[len(observations)-1].Kind != scenario {
			t.Fatalf("wrong failure kind: %+v", observations[len(observations)-1])
		}
	}
	if len(observations) != 7 {
		t.Fatalf("failure observation duplicated: %d", len(observations))
	}
}
