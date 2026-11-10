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
	"net/http"
	"net/url"
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
func request(ctx context.Context, client *http.Client, method, endpoint, token string, body any, out any) error {
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
	res, err := client.Do(r)
	if err != nil {
		return errors.New("无法连接 Claude 授权服务，请检查代理后重新授权")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Claude 授权服务返回 HTTP %d，请重新授权", res.StatusCode)
	}
	data, err = io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, out) != nil {
		return errors.New("Claude 授权服务响应格式异常，请重新授权")
	}
	return nil
}

func Exchange(ctx context.Context, client *http.Client, code, state, verifier string) (Result, error) {
	// Do not allow redirects to carry codes or Authorization to another host.
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client = &copyClient
	var tokens tokenResponse
	err := request(ctx, client, "POST", tokenURL, "", map[string]string{"grant_type": "authorization_code", "code": code, "state": state, "code_verifier": verifier, "client_id": ClientID, "redirect_uri": RedirectURL}, &tokens)
	if err != nil {
		return Result{}, err
	}
	if tokens.Access == "" || tokens.Refresh == "" || tokens.Expires <= 0 || tokens.Expires > 366*24*3600 {
		return Result{}, errors.New("Claude 未返回完整且有效的令牌，请重新授权")
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
	var profile struct {
		Account      identity     `json:"account"`
		Organization organization `json:"organization"`
	}
	if err := request(ctx, client, "GET", profileURL, tokens.Access, nil, &profile); err != nil {
		result.Warnings = append(result.Warnings, "令牌已获取，但账号资料读取失败，请补充缺失资料。")
	} else if profile.Account.UUID == "" || profile.Organization.UUID == "" || (tokens.Account.UUID != "" && profile.Account.UUID != tokens.Account.UUID) || (tokens.Organization.UUID != "" && profile.Organization.UUID != tokens.Organization.UUID) {
		result.Warnings = append(result.Warnings, "账号资料不完整或与授权身份不一致，未自动填充资料。")
	} else {
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
	}
	if result.Auth["organization_name"] == nil {
		var roles struct {
			Name string `json:"organization_name"`
		}
		if err := request(ctx, client, "GET", rolesURL, tokens.Access, nil, &roles); err == nil {
			set("organization_name", roles.Name)
		}
	}
	return result, nil
}
