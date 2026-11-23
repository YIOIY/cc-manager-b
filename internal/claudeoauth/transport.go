package claudeoauth

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
	"time"
)

// HeaderProfile is the stable per-account presentation used by every OAuth
// HTTP request. It deliberately contains only bounded, low-cardinality client
// presentation fields; it never carries tokens or endpoint URLs.
type HeaderProfile struct {
	UserAgent      string `json:"user_agent"`
	AcceptLanguage string `json:"accept_language"`
}

const defaultUserAgent = "claude-cli/1.0.120 (external, cli)"
const defaultAcceptLanguage = "en-US,en;q=0.9"

// WithHeaderProfile binds one stable client presentation to the whole OAuth
// flow so token, profile, role and probe requests cannot drift.
func WithHeaderProfile(ctx context.Context, profile HeaderProfile) context.Context {
	return context.WithValue(ctx, headerProfileKey{}, profile)
}

func HeaderProfileFrom(ctx context.Context) HeaderProfile {
	profile, _ := ctx.Value(headerProfileKey{}).(HeaderProfile)
	if profile.UserAgent == "" {
		profile.UserAgent = defaultUserAgent
	}
	if profile.AcceptLanguage == "" {
		profile.AcceptLanguage = defaultAcceptLanguage
	}
	return profile
}

// TransportError never retains transport error strings, which may contain
// proxy credentials. BeforeWrite permits one safe automatic retry only when
// the connection trace positively proves connection establishment failed.
// An untraced or already-connected request has an uncertain delivery outcome.
type TransportError struct {
	Op          string
	BeforeWrite bool
}

func (e *TransportError) Error() string {
	return "连接 Claude 授权服务中断，请检查网络或代理"
}

func AsTransportError(err error) (*TransportError, bool) {
	var te *TransportError
	if errors.As(err, &te) {
		return te, true
	}
	return nil, false
}

// Observation contains only bounded labels, an HTTP status, and elapsed time.
// No URL, credential, provider response body or transport text is retained.
type Observation struct {
	Stage    string
	Status   int
	Duration time.Duration
	Kind     string
}

type observerKey struct{}
type headerProfileKey struct{}

func WithObserver(ctx context.Context, observer func(Observation)) context.Context {
	return context.WithValue(ctx, observerKey{}, observer)
}

func applicationHeaders(ctx context.Context, r *http.Request) {
	profile := HeaderProfileFrom(ctx)
	r.Header.Set("User-Agent", profile.UserAgent)
	r.Header.Set("Accept-Language", profile.AcceptLanguage)
	r.Header.Set("Accept", "application/json")
}

// observeResponse includes response decoding in the elapsed time and reports
// exactly once per request. Trace callbacks can run concurrently.
func observeResponse(ctx context.Context, client *http.Client, r *http.Request, op string, consume func(*http.Response) error) (err error) {
	started := time.Now()
	status := 0
	defer func() {
		observer, _ := ctx.Value(observerKey{}).(func(Observation))
		if observer == nil {
			return
		}
		kind := "success"
		if err != nil {
			kind = "status"
			if _, ok := AsTransportError(err); ok {
				kind = "transport"
			} else if _, ok := AsResponseError(err); ok {
				kind = "response"
			}
		}
		observer(Observation{Stage: op, Status: status, Duration: time.Since(started), Kind: kind})
	}()
	var connected, wrote, establishmentFailed atomic.Bool
	trace := &httptrace.ClientTrace{
		GotConn:      func(httptrace.GotConnInfo) { connected.Store(true) },
		WroteHeaders: func() { wrote.Store(true) },
		WroteRequest: func(httptrace.WroteRequestInfo) { wrote.Store(true) },
		DNSDone: func(info httptrace.DNSDoneInfo) {
			if info.Err != nil {
				establishmentFailed.Store(true)
			}
		},
		ConnectDone: func(_, _ string, e error) {
			if e != nil {
				establishmentFailed.Store(true)
			}
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, e error) {
			if e != nil {
				establishmentFailed.Store(true)
			}
		},
	}
	r = r.WithContext(httptrace.WithClientTrace(r.Context(), trace))
	applicationHeaders(ctx, r)
	res, e := client.Do(r)
	if e != nil {
		return &TransportError{Op: op, BeforeWrite: establishmentFailed.Load() && !connected.Load() && !wrote.Load()}
	}
	defer res.Body.Close()
	status = res.StatusCode
	return consume(res)
}

func withoutRedirects(client *http.Client) *http.Client {
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copyClient
}
