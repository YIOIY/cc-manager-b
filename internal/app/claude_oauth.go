package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"cc-manager/internal/claudeoauth"
	"cc-manager/internal/errs"
)

const oauthSessionTTL = 30 * time.Minute

// Local cooldown policy for Claude OAuth against a known accountID.
// Wait is clamped to max(Retry-After, 60s) then capped at 15 minutes. Cap is a
// local bound, not full compliance with upstream Retry-After. Covers one
// process only: no accountID (new onboard), multi-instance, restart, and
// in-flight requests started before the cooldown is set are out of scope.
const (
	oauthCooldownDefault = time.Minute
	oauthCooldownCap     = 15 * time.Minute
)

type oauthPending struct {
	owner     [32]byte
	expires   time.Time
	proxyID   string
	accountID string
	flow      claudeoauth.Pending
}
type oauthPendingStore struct {
	mu    sync.Mutex
	items map[string]oauthPending
}

func (s *oauthPendingStore) sweep(stamp time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, p := range s.items {
		if !stamp.Before(p.expires) {
			delete(s.items, id)
		}
	}
}

type oauthAccountCooldown struct {
	mu    sync.Mutex
	until map[string]time.Time
}

func (s *oauthAccountCooldown) sweepLocked(now time.Time) {
	for id, at := range s.until {
		if !now.Before(at) {
			delete(s.until, id)
		}
	}
}

func (s *oauthAccountCooldown) remaining(accountID string, now time.Time) time.Duration {
	if accountID == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.until == nil {
		return 0
	}
	s.sweepLocked(now)
	at, ok := s.until[accountID]
	if !ok || !now.Before(at) {
		return 0
	}
	return at.Sub(now)
}

// extend keeps the later of the existing and new deadlines so concurrent
// responses cannot shorten the cooldown.
func (s *oauthAccountCooldown) extend(accountID string, now time.Time, wait time.Duration) time.Duration {
	// max(Retry-After, 60s), then 15m cap.
	if wait < oauthCooldownDefault {
		wait = oauthCooldownDefault
	}
	if wait > oauthCooldownCap {
		wait = oauthCooldownCap
	}
	if accountID == "" {
		return oauthRetryAfterSeconds(wait)
	}
	deadline := now.Add(wait)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.until == nil {
		s.until = make(map[string]time.Time)
	}
	s.sweepLocked(now)
	if existing, ok := s.until[accountID]; ok && existing.After(deadline) {
		deadline = existing
	}
	s.until[accountID] = deadline
	return oauthRetryAfterSeconds(deadline.Sub(now))
}

func oauthRetryAfterSeconds(wait time.Duration) time.Duration {
	if wait <= 0 {
		return time.Second
	}
	secs := int(math.Ceil(wait.Seconds()))
	if secs < 1 {
		secs = 1
	}
	return time.Duration(secs) * time.Second
}

func oauthOwner(r *http.Request) [32]byte {
	c, _ := r.Cookie(sessionCookie)
	if c == nil {
		return [32]byte{}
	}
	return sha256.Sum256([]byte(sessionUser(r).ID + ":" + c.Value))
}

func (a *App) writeOAuthRateLimited(w http.ResponseWriter, r *http.Request, wait time.Duration) {
	secs := int(oauthRetryAfterSeconds(wait) / time.Second)
	if secs < 1 {
		secs = 1
	}
	writeError(w, r, errs.New(errs.CodeOauthRateLimited).
		WithDetail("retry_after_seconds", secs).
		WithMessage(fmt.Sprintf("Claude 授权暂时限流，请约 %d 秒后再试", secs)))
}

func (a *App) claudeOAuthStart(w http.ResponseWriter, r *http.Request) {
	if a.keys == nil {
		fail(w, 503, "ENCRYPTION_KEY_REQUIRED", "请先配置凭证加密密钥")
		return
	}
	var in struct {
		ProxyID   string `json:"proxy_id"`
		AccountID string `json:"account_id"`
	}
	if !decodeJSON(w, r, &in, 8192) {
		return
	}
	if in.AccountID != "" {
		if !a.requireMutable(w, r, "accounts", in.AccountID, "账号不存在") {
			return
		}
		if in.ProxyID == "" {
			if _, err := a.savedCredentialProxy(in.AccountID); err != nil {
				fail(w, 422, "OAUTH_PROXY_INVALID", "已保存代理不可用，请选择代理后重新授权")
				return
			}
		}
	}
	if in.ProxyID != "" {
		if _, err := a.resolveProxyURL(in.ProxyID, ""); err != nil {
			fail(w, 422, "PROXY_INVALID", err.Error())
			return
		}
		if !a.requireProxyBindable(w, r, in.ProxyID) {
			return
		}
	}
	if wait := a.oauthCooldown.remaining(in.AccountID, time.Now()); wait > 0 {
		a.writeOAuthRateLimited(w, r, wait)
		return
	}
	flow, err := claudeoauth.NewPending()
	if err != nil {
		fail(w, 500, "OAUTH_START_FAILED", "无法生成授权链接")
		return
	}
	stamp := time.Now()
	a.oauth.sweep(stamp)
	owner := oauthOwner(r)
	a.oauth.mu.Lock()
	if a.oauth.items == nil {
		a.oauth.items = make(map[string]oauthPending)
	}
	// Generating a replacement invalidates previous links for this session.
	for id, p := range a.oauth.items {
		if p.owner == owner {
			delete(a.oauth.items, id)
		}
	}
	if len(a.oauth.items) >= 512 {
		a.oauth.mu.Unlock()
		fail(w, 429, "OAUTH_BUSY", "授权会话较多，请稍后重试")
		return
	}
	id := randomString()
	expires := stamp.Add(oauthSessionTTL)
	a.oauth.items[id] = oauthPending{owner: owner, expires: expires, proxyID: in.ProxyID, accountID: in.AccountID, flow: flow}
	a.oauth.mu.Unlock()
	write(w, 201, map[string]any{"id": id, "authorization_url": flow.URL, "expires_at": expires.UTC().Format(time.RFC3339)})
}

func (a *App) claudeOAuthCancel(w http.ResponseWriter, r *http.Request) {
	a.oauth.mu.Lock()
	if p, ok := a.oauth.items[r.PathValue("id")]; ok && p.owner == oauthOwner(r) {
		delete(a.oauth.items, r.PathValue("id"))
	}
	a.oauth.mu.Unlock()
	write(w, 200, map[string]bool{"cancelled": true})
}

func (a *App) claudeOAuthExchange(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Result string `json:"result"`
	}
	if !decodeJSON(w, r, &in, 16384) {
		return
	}
	id := r.PathValue("id")
	now := time.Now()
	a.oauth.mu.Lock()
	p, ok := a.oauth.items[id]
	if !ok || p.owner != oauthOwner(r) || !now.Before(p.expires) {
		a.oauth.mu.Unlock()
		fail(w, 404, "OAUTH_SESSION_EXPIRED", "授权链接已失效，请重新获取链接")
		return
	}
	code, err := claudeoauth.ParseResult(in.Result, p.flow.State)
	if err != nil {
		a.oauth.mu.Unlock()
		fail(w, 422, "OAUTH_RESULT_INVALID", err.Error())
		return
	}
	if wait := a.oauthCooldown.remaining(p.accountID, now); wait > 0 {
		// Drop pending so the client must start again after the wait.
		delete(a.oauth.items, id)
		a.oauth.mu.Unlock()
		a.writeOAuthRateLimited(w, r, wait)
		return
	}
	// One-shot consumption happens before network IO, including failed exchanges.
	delete(a.oauth.items, id)
	a.oauth.mu.Unlock()
	proxyURL := ""
	if p.accountID != "" {
		if !a.requireMutable(w, r, "accounts", p.accountID, "账号不存在") {
			return
		}
		if p.proxyID == "" {
			proxyURL, err = a.savedCredentialProxy(p.accountID)
			if err != nil {
				fail(w, 422, "OAUTH_PROXY_INVALID", "已保存代理不可用，请选择代理后重新授权")
				return
			}
		}
	}
	client, closeClient, err := a.claudeOAuthHTTPClient(p.proxyID, proxyURL)
	if err != nil {
		fail(w, 422, "OAUTH_PROXY_INVALID", err.Error())
		return
	}
	defer closeClient()
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	result, err := claudeoauth.Exchange(ctx, client, code, p.flow.State, p.flow.Verifier)
	if err != nil {
		a.writeOAuthExchangeError(w, r, p.accountID, err)
		return
	}
	a.auditFor(r, "account.oauth", "", "", "authorization completed")
	// Fresh credentials only, never previously stored credentials. No database
	// writes or supplier submissions occur until the user submits the form.
	write(w, 200, result)
}

func (a *App) writeOAuthExchangeError(w http.ResponseWriter, r *http.Request, accountID string, err error) {
	if se, ok := claudeoauth.AsStatusError(err); ok {
		switch se.Status {
		case http.StatusTooManyRequests:
			wait := se.RetryAfter
			if wait <= 0 {
				wait = oauthCooldownDefault
			}
			secs := a.oauthCooldown.extend(accountID, time.Now(), wait)
			a.writeOAuthRateLimited(w, r, secs)
			return
		case http.StatusUnauthorized, http.StatusForbidden:
			writeError(w, r, errs.New(errs.CodeOauthExchangeFailed).WithMessage("令牌无效或访问被拒绝，请重新授权"))
			return
		}
	}
	writeError(w, r, errs.New(errs.CodeOauthExchangeFailed).WithMessage(err.Error()))
}

func (a *App) savedCredentialProxy(accountID string) (string, error) {
	auth, err := a.loadCredential(accountID)
	if err != nil {
		return "", err
	}
	proxyURL, _ := auth["proxy_url"].(string)
	return normalizeProxyURL(proxyURL)
}

func (a *App) claudeOAuthHTTPClient(proxyID, fallbackProxyURL string) (*http.Client, func(), error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 15 * time.Second
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = dialer.DialContext
	if proxyID != "" || fallbackProxyURL != "" {
		raw, err := a.resolveProxyURL(proxyID, fallbackProxyURL)
		if err != nil {
			return nil, nil, err
		}
		u, err := proxyEndpoint(raw)
		if err != nil {
			return nil, nil, err
		}
		transport.Proxy = http.ProxyURL(u)
	}
	s, err := a.loadProxySettings()
	if err != nil {
		return nil, nil, errors.New("代理网络设置读取失败")
	}
	if s.Mode == "upstream" {
		u, err := proxyEndpoint(s.UpstreamURL)
		if err != nil {
			return nil, nil, errors.New("前置代理设置无效")
		}
		if s.Username != "" {
			password := ""
			if s.secret != "" {
				plain, err := a.openSecret(s.secret, proxySettingsAAD)
				if err != nil {
					return nil, nil, errors.New("前置代理密码无法解密")
				}
				password = string(plain)
			}
			u.User = url.UserPassword(s.Username, password)
		}
		if proxyID == "" && fallbackProxyURL == "" {
			transport.Proxy = http.ProxyURL(u)
		} else {
			transport.DialContext, err = upstreamDialer(u, transport.TLSClientConfig)
			if err != nil {
				return nil, nil, errors.New("前置代理设置无效")
			}
		}
	}
	return &http.Client{Transport: transport, Timeout: 18 * time.Second}, transport.CloseIdleConnections, nil
}
