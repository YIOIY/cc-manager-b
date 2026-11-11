package supplier

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	ErrSubmitRejected = errors.New("supplier submit rejected")
	ErrSubmitUnknown  = errors.New("supplier submit outcome unknown")
)

// SubmitError is a classified submission failure. It implements
// errs.Upstream so the application maps it to a registry code without
// parsing text, and unwraps to the legacy sentinels for errors.Is callers.
type SubmitError struct {
	Kind    string // "rejected" or "unknown"
	Status  int    // upstream HTTP status, 0 when the request never completed
	Summary string // whitelisted upstream text (detail/msg), unsanitized
	cause   error  // local transport error, logs only
}

// NewSubmitError builds a SubmitError; exported for tests.
func NewSubmitError(kind string, status int, summary string) *SubmitError {
	return &SubmitError{Kind: kind, Status: status, Summary: summary}
}

func (e *SubmitError) Error() string {
	sentinel := ErrSubmitUnknown
	if e.Kind == "rejected" {
		sentinel = ErrSubmitRejected
	}
	s := sentinel.Error()
	if e.Status != 0 {
		s += fmt.Sprintf(": HTTP %d", e.Status)
	}
	if e.Summary != "" {
		s += ": " + e.Summary
	}
	if e.cause != nil {
		s += ": " + e.cause.Error()
	}
	return s
}

func (e *SubmitError) Unwrap() error {
	if e.Kind == "rejected" {
		return ErrSubmitRejected
	}
	return ErrSubmitUnknown
}

func (e *SubmitError) UpstreamCode() string {
	switch {
	case e.Kind == "rejected" && (e.Status == 401 || e.Status == 403):
		return "PLATFORM_AUTH_FAILED"
	case e.Kind == "rejected":
		return "PLATFORM_REJECTED"
	case e.Status >= 500:
		return "PLATFORM_UNAVAILABLE"
	default:
		return "SUBMIT_OUTCOME_UNKNOWN"
	}
}
func (e *SubmitError) UpstreamStatus() int     { return e.Status }
func (e *SubmitError) UpstreamSummary() string { return e.Summary }

type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}
type SubmitResult struct {
	ID    int64  `json:"id"`
	Alias string `json:"alias"`
	Msg   string `json:"msg"`
}
type Metric struct {
	ID                                       int64
	AID                                      int64
	Alias, Status                            string
	Requests, PromptTokens, CompletionTokens int64
	SuccessRate                              float64
	SuccessRatePresent                       bool
}
type Bill struct {
	ID                                                                                        int64
	RequestCount, PromptTokens, CompletionTokens, CacheReadInputTokens, CacheWriteInputTokens int64
	CostFen                                                                                   int64
}

func New(baseURL, token string) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("supplier token is required")
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("supplier base URL must use HTTPS")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Supplier traffic never inherits HTTP(S)_PROXY from the process
	// environment: a stale desktop proxy once silently broke every submission.
	// An operator who needs a proxy names it explicitly.
	transport.Proxy = nil
	proxy, err := ProxyFromEnv()
	if err != nil {
		return nil, err
	}
	if proxy != nil {
		transport.Proxy = http.ProxyURL(proxy)
	}
	pinned := false
	// Pins are scoped to an exact host:port. An operator can trust an existing
	// private endpoint certificate without weakening other supplier connections.
	if encoded := os.Getenv("CC_MANAGER_SUPPLIER_TLS_PINS"); encoded != "" {
		var pins map[string]string
		if json.Unmarshal([]byte(encoded), &pins) != nil {
			return nil, errors.New("supplier TLS pins must be a JSON object")
		}
		if value, ok := pins[u.Host]; ok {
			pinned = true
			pin, err := hex.DecodeString(value)
			if err != nil || len(pin) != sha256.Size {
				return nil, errors.New("supplier TLS pin must be a SHA256 hex digest")
			}
			transport.TLSClientConfig = &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: true, // exact certificate verification below replaces PKI validation for this endpoint
				VerifyConnection: func(state tls.ConnectionState) error {
					if len(state.PeerCertificates) == 0 {
						return errors.New("supplier TLS certificate missing")
					}
					digest := sha256.Sum256(state.PeerCertificates[0].Raw)
					if subtle.ConstantTimeCompare(digest[:], pin) != 1 {
						return errors.New("supplier TLS certificate pin mismatch")
					}
					return nil
				},
			}
		}
	}
	if os.Getenv("CC_MANAGER_SUPPLIER_INSECURE_TLS") == "1" && !pinned {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // explicit local acceptance switch; secure default remains enabled
	}
	return &Client{base: u, token: token, http: &http.Client{Timeout: 30 * time.Second, Transport: transport}}, nil
}
func (c *Client) request(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	data, status, _, err := c.requestDetails(ctx, method, path, body)
	return data, status, err
}
func (c *Client) requestDetails(ctx context.Context, method, path string, body any) ([]byte, int, http.Header, error) {
	var rd io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return nil, 0, nil, e
		}
		rd = bytes.NewReader(b)
	}
	u := *c.base
	u.Path = strings.TrimRight(c.base.Path, "/") + path
	req, e := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if e != nil {
		return nil, 0, nil, e
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.http.Do(req)
	if e != nil {
		return nil, 0, nil, e
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if e != nil {
		return nil, resp.StatusCode, resp.Header, e
	}
	return data, resp.StatusCode, resp.Header, nil
}
func parseErr(data []byte, status int) error {
	var v struct {
		Detail string `json:"detail"`
		Msg    string `json:"msg"`
	}
	_ = json.Unmarshal(data, &v)
	s := v.Detail
	if s == "" {
		s = v.Msg
	}
	if s == "" {
		s = http.StatusText(status)
	}
	return &SubmitError{Kind: "rejected", Status: status, Summary: s}
}
func (c *Client) SubmitClaudeAccount(ctx context.Context, payload map[string]any) (SubmitResult, error) {
	data, status, e := c.request(ctx, http.MethodPost, "/supplier-account/api/openapi/claude/accounts", payload)
	if e != nil {
		// Transport failures are local; their English text never belongs in
		// user-facing copy, so the summary stays empty and the cause is in Error().
		return SubmitResult{}, &SubmitError{Kind: "unknown", cause: e}
	}
	if status < 200 || status >= 300 {
		if status >= 400 && status < 500 {
			return SubmitResult{}, parseErr(data, status)
		}
		return SubmitResult{}, &SubmitError{Kind: "unknown", Status: status}
	}
	var out SubmitResult
	if e = json.Unmarshal(data, &out); e != nil {
		return SubmitResult{}, &SubmitError{Kind: "unknown", Status: status, Summary: "invalid response"}
	}
	if out.ID <= 0 {
		return SubmitResult{}, &SubmitError{Kind: "unknown", Status: status, Summary: "missing upstream id"}
	}
	return out, nil
}

// ProxyFromEnv reads CC_MANAGER_SUPPLIER_PROXY: nil means connect directly.
// The server validates it at startup so a typo fails loudly instead of
// surfacing later as "platform connection invalid".
func ProxyFromEnv() (*url.URL, error) {
	raw := strings.TrimSpace(os.Getenv("CC_MANAGER_SUPPLIER_PROXY"))
	if raw == "" {
		return nil, nil
	}
	p, err := url.Parse(raw)
	if err != nil || p.Host == "" || (p.Scheme != "http" && p.Scheme != "https" && p.Scheme != "socks5" && p.Scheme != "socks5h") {
		return nil, errors.New("supplier proxy is invalid: use http://, https://, socks5:// or socks5h:// with a host")
	}
	return p, nil
}

// MetricsHTTPError is a non-2xx metrics response, classified for the registry.
type MetricsHTTPError struct {
	Status  int
	Summary string
}

func (e *MetricsHTTPError) Error() string {
	return fmt.Sprintf("supplier metrics HTTP %d: %s", e.Status, e.Summary)
}

func (e *MetricsHTTPError) UpstreamCode() string {
	switch {
	case e.Status == 401 || e.Status == 403:
		return "PLATFORM_AUTH_FAILED"
	case e.Status == 404:
		return "PLATFORM_QUERY_DENIED"
	case e.Status == 429 || e.Status >= 500:
		return "PLATFORM_UNAVAILABLE"
	default:
		return "PLATFORM_RESPONSE_INVALID"
	}
}
func (e *MetricsHTTPError) UpstreamStatus() int     { return e.Status }
func (e *MetricsHTTPError) UpstreamSummary() string { return e.Summary }

func validateIDs(ids []string) ([]int64, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return nil, errors.New("account IDs must contain 1-100 items")
	}
	out := make([]int64, len(ids))
	for i, s := range ids {
		n, e := strconv.ParseInt(s, 10, 64)
		if e != nil || n <= 0 {
			return nil, fmt.Errorf("invalid account id")
		}
		out[i] = n
	}
	return out, nil
}
func (c *Client) QueryMetrics(ctx context.Context, ids []string, begin, end time.Time) ([]Metric, error) {
	a, e := validateIDs(ids)
	if e != nil {
		return nil, e
	}
	now := time.Now()
	if begin.After(end) || end.After(now) || end.Sub(begin) > 7*24*time.Hour {
		return nil, errors.New("metrics window invalid")
	}
	body := map[string]any{"account_ids": a, "begin_time": begin.Format("2006-01-02 15:04:05"), "end_time": end.Format("2006-01-02 15:04:05"), "aggregate": false}
	data, status, e := c.request(ctx, http.MethodPost, "/supplier-account/api/openapi/metrics/query", body)
	if e != nil {
		return nil, e
	}
	if status < 200 || status >= 300 {
		return nil, &MetricsHTTPError{Status: status, Summary: responseSummary(data)}
	}
	var raw struct {
		Accounts []struct {
			ID               json.Number `json:"id"`
			AID              json.Number `json:"aid"`
			Alias            string      `json:"account_alias"`
			Status           string      `json:"status"`
			Requests         int64       `json:"requests"`
			SuccessRate      *float64    `json:"success_rate"`
			PromptTokens     int64       `json:"prompt_tokens"`
			CompletionTokens int64       `json:"completion_tokens"`
		} `json:"accounts"`
	}
	if e = json.Unmarshal(data, &raw); e != nil {
		return nil, e
	}
	out := make([]Metric, len(raw.Accounts))
	for i, v := range raw.Accounts {
		out[i].ID, _ = strconv.ParseInt(v.ID.String(), 10, 64)
		out[i].AID, _ = strconv.ParseInt(v.AID.String(), 10, 64)
		out[i].Alias = v.Alias
		out[i].Status = v.Status
		out[i].Requests = v.Requests
		out[i].PromptTokens = v.PromptTokens
		out[i].CompletionTokens = v.CompletionTokens
		if v.SuccessRate != nil {
			out[i].SuccessRate = *v.SuccessRate
			out[i].SuccessRatePresent = true
		}
	}
	return out, nil
}

func responseSummary(data []byte) string {
	var v struct {
		Detail  string `json:"detail"`
		Msg     string `json:"msg"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &v) == nil {
		for _, s := range []string{v.Detail, v.Msg, v.Message} {
			if strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 240 {
		s = s[:240]
	}
	return s
}
func decimalFen(s string) (int64, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, errors.New("invalid cost")
	}
	r.Mul(r, big.NewRat(100, 1))
	if r.Denom().Cmp(big.NewInt(1)) != 0 {
		return 0, errors.New("cost has more than 2 decimals")
	}
	if !r.Num().IsInt64() {
		return 0, errors.New("cost overflow")
	}
	return r.Num().Int64(), nil
}
