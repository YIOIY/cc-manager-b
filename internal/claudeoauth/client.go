// Package claudeoauth implements the manual-code PKCE flow observed in the
// Claude Code client. These provider endpoints are not a stable public API.
package claudeoauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	ClientID    = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	RedirectURL = "https://platform.claude.com/oauth/code/callback"
	tokenURL    = "https://platform.claude.com/v1/oauth/token"
	profileURL  = "https://api.anthropic.com/api/oauth/profile"
	rolesURL    = "https://api.anthropic.com/api/oauth/claude_cli/roles"
)

type Pending struct{ State, Verifier, URL string }

func NewPending() (Pending, error) {
	random := func() (string, error) {
		b := make([]byte, 32)
		_, err := rand.Read(b)
		return base64.RawURLEncoding.EncodeToString(b), err
	}
	state, err := random()
	if err != nil {
		return Pending{}, err
	}
	verifier, err := random()
	if err != nil {
		return Pending{}, err
	}
	hash := sha256.Sum256([]byte(verifier))
	q := url.Values{"code": {"true"}, "client_id": {ClientID}, "response_type": {"code"}, "redirect_uri": {RedirectURL},
		"scope": {"user:profile user:inference user:sessions:claude_code"}, "state": {state},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "code_challenge_method": {"S256"}}
	return Pending{state, verifier, "https://claude.com/cai/oauth/authorize?" + q.Encode()}, nil
}

// Pasted URLs are parsed locally, never fetched. Bare codes are bound by PKCE
// and the application's authenticated session; supplied state must match.
func ParseResult(raw, expectedState string) (string, error) {
	raw = strings.TrimSpace(raw)
	code, state := raw, ""
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host != "platform.claude.com" || u.Path != "/oauth/code/callback" || u.User != nil || u.Fragment != "" {
			return "", errors.New("请粘贴 Claude 授权页给出的授权码或完整回调地址")
		}
		q := u.Query()
		if q.Get("error") != "" {
			return "", errors.New("授权未完成，请重新打开链接并允许授权")
		}
		if len(q["code"]) != 1 || len(q["state"]) != 1 {
			return "", errors.New("回调地址缺少授权码或 state")
		}
		code, state = q.Get("code"), q.Get("state")
		if state == "" {
			return "", errors.New("回调地址缺少 state")
		}
	} else if strings.Contains(raw, "#") {
		parts := strings.Split(raw, "#")
		if len(parts) != 2 || parts[1] == "" {
			return "", errors.New("授权结果格式不正确")
		}
		code, state = parts[0], parts[1]
	}
	if state != "" && subtle.ConstantTimeCompare([]byte(state), []byte(expectedState)) != 1 {
		return "", errors.New("授权结果与当前链接不匹配，请使用本次生成的链接")
	}
	if code == "" || len(code) > 4096 || strings.ContainsAny(code, " \t\r\n?#&") {
		return "", errors.New("授权码为空或格式不正确")
	}
	return code, nil
}

type Result struct {
	Auth         map[string]any `json:"auth_json"`
	Plan         string         `json:"plan,omitempty"`
	RegisterTime string         `json:"register_time,omitempty"`
	Warnings     []string       `json:"warnings"`
	// ProfileLoaded is internal recovery state; never accepted from HTTP input.
	ProfileLoaded bool `json:"-"`
}

// StatusError is a non-200 Claude OAuth HTTP response. The message never
// includes the upstream body (tokens and codes can appear there).
type StatusError struct {
	Op         string
	Status     int
	RetryAfter time.Duration
}

func (e *StatusError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Sprintf("Claude 授权服务拒绝访问（HTTP %d）", e.Status)
	case http.StatusTooManyRequests:
		return fmt.Sprintf("Claude 授权服务暂时限流（HTTP %d）", e.Status)
	default:
		return fmt.Sprintf("Claude 授权服务返回 HTTP %d", e.Status)
	}
}

func AsStatusError(err error) (*StatusError, bool) {
	var se *StatusError
	if errors.As(err, &se) {
		return se, true
	}
	return nil, false
}

// ResponseError identifies unusable response data without retaining any
// upstream body. At the token stage the authorization code may be consumed.
// At the profile stage it also identifies expired or invalid cached tokens.
type ResponseError struct{ Op string }

func (e *ResponseError) Error() string {
	return "Claude 授权数据无效或已过期，请重新授权"
}

func AsResponseError(err error) (*ResponseError, bool) {
	var re *ResponseError
	if errors.As(err, &re) {
		return re, true
	}
	return nil, false
}

func parseRetryAfter(h http.Header, now time.Time) time.Duration {
	raw := strings.TrimSpace(h.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	numeric := true
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			numeric = false
			break
		}
	}
	if numeric {
		secs, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || secs > uint64(math.MaxInt64/int64(time.Second)) {
			// Saturate the representation, never wrap a long wait into zero or
			// a negative duration. This is not a business-policy cooldown cap.
			return time.Duration(math.MaxInt64)
		}
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(raw); err == nil {
		if d := at.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

type identity struct {
	UUID         string `json:"uuid"`
	Email        string `json:"email"`
	EmailAddress string `json:"email_address"`
	CreatedAt    string `json:"created_at"`
}
type organization struct {
	UUID          string `json:"uuid"`
	Name          string `json:"name"`
	RateLimitTier string `json:"rate_limit_tier"`
}
type tokenResponse struct {
	Access       string       `json:"access_token"`
	Refresh      string       `json:"refresh_token"`
	ID           string       `json:"id_token"`
	Expires      int64        `json:"expires_in"`
	Account      identity     `json:"account"`
	Organization organization `json:"organization"`
}

// request deliberately never includes a response body, URL or transport error
// in its error: upstreams and proxies can echo tokens, codes and passwords.
func request(ctx context.Context, client *http.Client, method, endpoint, token, op string, body any, out any) error {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	r, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(data))
	if err != nil {
		return errors.New("授权请求无法创建")
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return observeResponse(ctx, client, r, op, func(res *http.Response) error {
		if res.StatusCode != http.StatusOK {
			return &StatusError{Op: op, Status: res.StatusCode, RetryAfter: parseRetryAfter(res.Header, time.Now().UTC())}
		}
		data, err = io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
		if err != nil || len(data) > 1<<20 || json.Unmarshal(data, out) != nil {
			return &ResponseError{Op: op}
		}
		if tokens, ok := out.(*tokenResponse); ok && (tokens.Access == "" || tokens.Refresh == "" || tokens.Expires <= 0 || tokens.Expires > 366*24*3600) {
			return &ResponseError{Op: op}
		}
		return nil
	})
}

// Exchange is the convenience entry point. Stateful callers should save Token's
// successful result before calling CompleteProfile, so a later error cannot
// force the one-time authorization code to be exchanged again.
func Exchange(ctx context.Context, client *http.Client, code, state, verifier string) (Result, error) {
	result, err := Token(ctx, client, code, state, verifier)
	if err != nil {
		return Result{}, err
	}
	return CompleteProfile(ctx, client, result)
}

// Token exchanges only the one-time code. The returned tokens are a resumable
// stage and must remain server-side until profile completion and final submit.
func Token(ctx context.Context, client *http.Client, code, state, verifier string) (Result, error) {
	client = withoutRedirects(client)
	var tokens tokenResponse
	err := request(ctx, client, "POST", tokenURL, "", "token", map[string]string{"grant_type": "authorization_code", "code": code, "state": state, "code_verifier": verifier, "client_id": ClientID, "redirect_uri": RedirectURL}, &tokens)
	if err != nil {
		return Result{}, err
	}
	now := time.Now().UTC()
	result := Result{Auth: map[string]any{"id_token": tokens.ID, "access_token": tokens.Access, "refresh_token": tokens.Refresh, "last_refresh": now.Format(time.RFC3339), "expired": now.Add(time.Duration(tokens.Expires) * time.Second).Format(time.RFC3339), "type": "claude", "verification_status": "not_required"}, Warnings: []string{}}
	set := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			result.Auth[key] = value
		}
	}
	set("email", tokens.Account.EmailAddress)
	set("account_uuid", tokens.Account.UUID)
	set("organization_uuid", tokens.Organization.UUID)
	set("organization_name", tokens.Organization.Name)
	return result, nil
}

// CompleteProfile resumes after a successful token exchange. All remote errors
// preserve the successful token stage, even an access refusal. The caller can
// decide whether to wait, retry a later stage or explicitly reauthorize.
// Result maps and slices are copied so callers can retain an untouched cache.
//
// Optional opts configure inter-stage pacing; production callers rely on the
// default DefaultProductionPause so profile and roles calls do not arrive
// back-to-back. Passing WithStagePause(nil) restores the unpaced behaviour
// used by focused unit tests.
func CompleteProfile(ctx context.Context, client *http.Client, result Result, opts ...ProfileOption) (Result, error) {
	pause := DefaultProductionPause
	for _, opt := range opts {
		if opt != nil {
			opt(&pause)
		}
	}
	access, _ := result.Auth["access_token"].(string)
	refresh, _ := result.Auth["refresh_token"].(string)
	expired, _ := result.Auth["expired"].(string)
	expiresAt, err := time.Parse(time.RFC3339, expired)
	if access == "" || refresh == "" || err != nil || !expiresAt.After(time.Now()) {
		return Result{}, &ResponseError{Op: "profile"}
	}
	client = withoutRedirects(client)
	auth := make(map[string]any, len(result.Auth))
	for key, value := range result.Auth {
		auth[key] = value
	}
	result.Auth = auth
	warnings := make([]string, len(result.Warnings))
	copy(warnings, result.Warnings)
	result.Warnings = warnings
	set := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			result.Auth[key] = value
		}
	}
	if pauseErr := sleepStage(ctx, pause, "profile"); pauseErr != nil {
		return result, pauseErr
	}
	if !result.ProfileLoaded {
		accountUUID, _ := result.Auth["account_uuid"].(string)
		organizationUUID, _ := result.Auth["organization_uuid"].(string)
		var profile struct {
			Account      identity     `json:"account"`
			Organization organization `json:"organization"`
		}
		profileErr := request(ctx, client, "GET", profileURL, access, "profile", nil, &profile)
		if profileErr != nil {
			return result, profileErr
		}
		profileOK := profile.Account.UUID != "" && profile.Organization.UUID != "" &&
			(accountUUID == "" || profile.Account.UUID == accountUUID) &&
			(organizationUUID == "" || profile.Organization.UUID == organizationUUID)
		if !profileOK {
			result.Warnings = append(result.Warnings, "账号资料不完整或与授权身份不一致，未自动填充资料。")
			// Do not merge metadata from an untrusted identity or query its roles.
			return result, nil
		}
		set("email", profile.Account.Email)
		set("account_uuid", profile.Account.UUID)
		set("organization_uuid", profile.Organization.UUID)
		set("organization_name", profile.Organization.Name)
		if created, e := time.Parse(time.RFC3339, profile.Account.CreatedAt); e == nil {
			result.RegisterTime = created.UTC().Format("2006-01-02")
		}
		switch profile.Organization.RateLimitTier {
		case "default_claude_max_5x":
			result.Plan = "claude/cc-max-5x"
		case "default_claude_max_20x":
			result.Plan = "claude/cc-max-20x"
		case "default_claude_pro":
			result.Plan = "claude/cc-pro"
		}
		result.ProfileLoaded = true
	}
	// Persist ProfileLoaded before a fallible roles call. A resumed request
	// can now fetch roles without repeating the profile or token stages.
	if result.Auth["organization_name"] == nil {
		if pauseErr := sleepStage(ctx, pause, "roles"); pauseErr != nil {
			return result, pauseErr
		}
		var roles struct {
			Name string `json:"organization_name"`
		}
		if err := request(ctx, client, "GET", rolesURL, access, "roles", nil, &roles); err != nil {
			return result, err
		}
		set("organization_name", roles.Name)
	}
	return result, nil
}

// ReachBusy is a reachability probe that got a 429. The exit works; Claude is
// just throttling, so the caller should wait rather than swap proxies.
type ReachBusy struct{ RetryAfter time.Duration }

func (e *ReachBusy) Error() string { return "Claude 授权服务暂时限流" }

func AsReachBusy(err error) (*ReachBusy, bool) {
	var re *ReachBusy
	if errors.As(err, &re) {
		return re, true
	}
	return nil, false
}

// Reachable probes the real token endpoint without consuming a code. A 405
// proves GET reached it; an access refusal, rate limit, upstream failure and
// invalid response remain distinct from a connection failure.
func Reachable(ctx context.Context, client *http.Client) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return &ResponseError{Op: "reach"}
	}
	return observeResponse(ctx, withoutRedirects(client), req, "reach", func(res *http.Response) error {
		switch res.StatusCode {
		case http.StatusTooManyRequests:
			return &ReachBusy{RetryAfter: parseRetryAfter(res.Header, time.Now().UTC())}
		case http.StatusMethodNotAllowed:
			return nil
		case http.StatusOK:
			data, err := io.ReadAll(io.LimitReader(res.Body, (64<<10)+1))
			if err != nil || len(data) > 64<<10 || !json.Valid(data) {
				return &ResponseError{Op: "reach"}
			}
			return nil
		default:
			return &StatusError{Op: "reach", Status: res.StatusCode, RetryAfter: parseRetryAfter(res.Header, time.Now().UTC())}
		}
	})
}
