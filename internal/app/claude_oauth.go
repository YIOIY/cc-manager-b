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
	"strings"
	"sync"
	"time"

	"cc-manager/internal/claudeoauth"
	"cc-manager/internal/errs"
)

const oauthSessionTTL = 30 * time.Minute

// oauthReachTimeout bounds the connectivity check made before a link is issued.
const oauthReachTimeout = 10 * time.Second

// Local cooldown policy for Claude OAuth. The cooldown is keyed by account,
// or by normalised email when a brand-new account still has no ID, so a retry
// made straight after a 429 no longer walks into the same rate limit.
//
// Upstream Retry-After is obeyed in full up to oauthCooldownCap; the lower
// bound only fills in responses that carry no header. The cap is deliberate:
// a bogus multi-hour header must not lock an account out for the rest of the
// day. The policy is per process — multi-instance, restart, and requests
// already in flight when the cooldown is set stay out of scope.
const (
	oauthCooldownDefault = time.Minute
	oauthCooldownMin     = time.Minute
	oauthCooldownCap     = time.Hour
)

// oauthGateWait is how long an exchange queues for a slot before OAUTH_BUSY.
const oauthGateWait = 30 * time.Second

type oauthPending struct {
	owner     [32]byte
	expires   time.Time
	proxyID   string
	accountID string
	flow      claudeoauth.Pending
	// refreshed records that this pending already survived one transport or
	// proxy failure. It is informational for now: reuse no longer keys off it,
	// because a link that outlived a broken exit is still valid.
	refreshed bool
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

// remaining reports how long key is still cooling down. An empty key (no
// account ID and no email yet) simply never matches.
func (s *oauthAccountCooldown) remaining(key string, now time.Time) time.Duration {
	if key == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.until == nil {
		return 0
	}
	s.sweepLocked(now)
	at, ok := s.until[key]
	if !ok || !now.Before(at) {
		return 0
	}
	return at.Sub(now)
}

// extend records a cooldown and returns the remaining time for Retry-After.
// It keeps the later of the existing and new deadlines so concurrent
// responses cannot shorten the wait.
func (s *oauthAccountCooldown) extend(key string, now time.Time, wait time.Duration) time.Duration {
	// Honour upstream Retry-After; only fill in a missing/short one.
	if wait <= 0 {
		wait = oauthCooldownDefault
	}
	if wait < oauthCooldownMin {
		wait = oauthCooldownMin
	}
	if wait > oauthCooldownCap {
		wait = oauthCooldownCap
	}
	if key == "" {
		return oauthRetryAfterSeconds(wait)
	}
	deadline := now.Add(wait)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.until == nil {
		s.until = make(map[string]time.Time)
	}
	s.sweepLocked(now)
	if existing, ok := s.until[key]; ok && existing.After(deadline) {
		deadline = existing
	}
	s.until[key] = deadline
	return oauthRetryAfterSeconds(deadline.Sub(now))
}

// cooldownKey prefers the stable account ID. A new onboard has no ID yet, so
// the normalised email stands in; without either, "" (no cooldown).
func cooldownKey(accountID, email string) string {
	if accountID != "" {
		return "id:" + accountID
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return ""
	}
	return "mail:" + email
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
		// Refresh asks for a link that is usable even when one is already
		// waiting, e.g. after the user closed the tab or wants a new code.
		Refresh bool `json:"refresh"`
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
	owner := oauthOwner(r)
	stamp := time.Now()
	// Reuse an existing link when nothing would change. Re-issuing costs a new
	// PKCE pair and, more importantly on some exits, a fresh round trip that
	// looks like another login attempt from the same IP.
	var (
		id          string
		p           oauthPending
		reusable    oauthPending
		hasReusable bool
	)
	if !in.Refresh {
		id, reusable, hasReusable = a.reusableOAuthPending(owner, in.ProxyID, in.AccountID, stamp)
	}
	key := cooldownKey(in.AccountID, a.oauthEmail(in.AccountID))
	if wait := a.oauthCooldown.remaining(key, stamp); wait > 0 {
		// A link that is still usable is handed back with the wait attached: the
		// browser login the user already did stays valid, only the exchange is
		// throttled. Minting a new PKCE pair during a cooldown would add another
		// login round trip from an IP Anthropic is already throttling.
		if hasReusable {
			secs := int(oauthRetryAfterSeconds(wait) / time.Second)
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", fmt.Sprint(secs))
			body := oauthPendingResponse(id, reusable)
			body["retry_after_seconds"] = secs
			write(w, 200, body)
			return
		}
		a.writeOAuthRateLimited(w, r, wait)
		return
	}
	// Hand back the live link rather than minting a second PKCE pair. The user
	// keeps the tab they already opened and no new login round trip starts.
	if hasReusable {
		write(w, 200, oauthPendingResponse(id, reusable))
		return
	}
	// A proxy that passed the pool check may still not reach Claude. Finding
	// out now costs one request; finding out at exchange costs the user the
	// browser login they just completed.
	if in.ProxyID != "" {
		ctx, cancel := context.WithTimeout(r.Context(), oauthReachTimeout)
		defer cancel()
		if err := a.checkOAuthProxyReach(ctx, in.ProxyID); err != nil {
			if busy, ok := claudeoauth.AsReachBusy(err); ok {
				secs := a.oauthCooldown.extend(key, time.Now(), busy.RetryAfter)
				a.writeOAuthRateLimited(w, r, secs)
				return
			}
			writeError(w, r, errs.New(errs.CodeOauthProxyUnreachable).WithMessage(oauthReachMessage(err)))
			return
		}
	}
	flow, err := claudeoauth.NewPending()
	if err != nil {
		fail(w, 500, "OAUTH_START_FAILED", "无法生成授权链接")
		return
	}
	a.oauth.sweep(stamp)
	a.oauth.mu.Lock()
	if a.oauth.items == nil {
		a.oauth.items = make(map[string]oauthPending)
	}
	// A replacement invalidates the previous link of this session.
	for oldID, oldPending := range a.oauth.items {
		if oldPending.owner == owner {
			delete(a.oauth.items, oldID)
		}
	}
	if len(a.oauth.items) >= 512 {
		a.oauth.mu.Unlock()
		fail(w, 429, "OAUTH_BUSY", "授权会话较多，请稍后重试")
		return
	}
	id = randomString()
	p = oauthPending{owner: owner, expires: stamp.Add(oauthSessionTTL), proxyID: in.ProxyID, accountID: in.AccountID, flow: flow}
	a.oauth.items[id] = p
	a.oauth.mu.Unlock()
	write(w, 201, oauthPendingResponse(id, p))
}

func oauthPendingResponse(id string, p oauthPending) map[string]any {
	return map[string]any{"id": id, "authorization_url": p.flow.URL, "expires_at": p.expires.UTC().Format(time.RFC3339)}
}

// reusableOAuthPending returns an unexpired pending bound to the same owner,
// proxy and account. A transport failure or a 5xx does not make the link
// worthless, so the pending stays reusable; only an explicit refresh retires
// it.
func (a *App) reusableOAuthPending(owner [32]byte, proxyID, accountID string, now time.Time) (string, oauthPending, bool) {
	a.oauth.mu.Lock()
	defer a.oauth.mu.Unlock()
	for id, p := range a.oauth.items {
		if p.owner == owner && p.proxyID == proxyID && p.accountID == accountID && now.Before(p.expires) {
			return id, p, true
		}
	}
	return "", oauthPending{}, false
}

// oauthEmail reads auth_json.email for a saved account so a 429 on a new
// onboard can still be attributed without an account ID.
func (a *App) oauthEmail(accountID string) string {
	if accountID == "" {
		return ""
	}
	auth, err := a.loadCredential(accountID)
	if err != nil {
		return ""
	}
	email, _ := auth["email"].(string)
	return email
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
	key := cooldownKey(p.accountID, a.oauthEmail(p.accountID))
	if wait := a.oauthCooldown.remaining(key, now); wait > 0 {
		// Keep the pending: the link and the pasted code are still good, the
		// user only needs to press the button again once the wait is over.
		a.oauth.mu.Unlock()
		a.writeOAuthRateLimited(w, r, wait)
		return
	}
	a.oauth.mu.Unlock()
	// Bound the number of concurrent POSTs one machine sends at a time.
	ctx, cancel := context.WithTimeout(r.Context(), oauthGateWait+40*time.Second)
	defer cancel()
	release, err := a.oauthGate.acquire(ctx, oauthGateWait)
	if err == errOAuthGateBusy {
		secs := a.oauthCooldown.extend(key, time.Now(), oauthGateWait)
		a.writeOAuthBusy(w, r, secs)
		return
	}
	if err != nil {
		fail(w, 499, "OAUTH_EXCHANGE_FAILED", "等待授权额度超时，请重试")
		return
	}
	defer release()
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
	exchangeCtx, cancelExchange := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancelExchange()
	result, err := claudeoauth.Exchange(exchangeCtx, client, code, p.flow.State, p.flow.Verifier)
	if err == nil {
		a.dropOAuthPending(id)
		a.auditFor(r, "account.oauth", "", "", "authorization completed")
		// Fresh credentials only, never previously stored credentials. No database
		// writes or supplier submissions occur until the user submits the form.
		write(w, 200, result)
		return
	}
	// Keep the pending on retryable failures so one more attempt can reuse the
	// same link and code instead of forcing the whole browser round trip again.
	se, isStatus := claudeoauth.AsStatusError(err)
	transport := !isStatus
	if agentBusy, ok := claudeoauth.AsReachBusy(err); ok {
		transport = false
		_ = agentBusy
	}
	if transport || p.refreshed || (isStatus && se.Status >= 500) {
		a.markOAuthPendingRetried(id, true)
	} else {
		a.markOAuthPendingRetried(id, false)
	}
	if isStatus && oauthTerminalStatus(se.Status) {
		a.dropOAuthPending(id)
	}
	a.writeOAuthExchangeError(w, r, key, err)
}

// oauthTerminalStatus reports statuses where replaying the same code cannot
// succeed: the code is spent, bound to another state, or the identity is
// already rejected.
func oauthTerminalStatus(status int) bool {
	switch status {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict:
		return true
	}
	return false
}

func (a *App) markOAuthPendingRetried(id string, retried bool) {
	a.oauth.mu.Lock()
	defer a.oauth.mu.Unlock()
	p, ok := a.oauth.items[id]
	if !ok {
		return
	}
	p.refreshed = retried
	a.oauth.items[id] = p
}

func (a *App) dropOAuthPending(id string) {
	a.oauth.mu.Lock()
	delete(a.oauth.items, id)
	a.oauth.mu.Unlock()
}

func (a *App) writeOAuthBusy(w http.ResponseWriter, r *http.Request, wait time.Duration) {
	secs := int(oauthRetryAfterSeconds(wait) / time.Second)
	if secs < 1 {
		secs = 1
	}
	writeError(w, r, errs.New(errs.CodeOauthBusy).
		WithDetail("retry_after_seconds", secs).
		WithMessage(fmt.Sprintf("同时进行的授权较多，请约 %d 秒后再试", secs)))
}

// writeOAuthExchangeError classifies one exchange failure. The split matters:
// a transport error means "swap the exit", a 429 means "wait", a 401/403 means
// "Anthropic is refusing this identity" and replaying the same code is
// pointless, while any other 4xx is usually the user's pasted code.
func (a *App) writeOAuthExchangeError(w http.ResponseWriter, r *http.Request, key string, err error) {
	if busy, ok := claudeoauth.AsReachBusy(err); ok {
		secs := a.oauthCooldown.extend(key, time.Now(), busy.RetryAfter)
		a.writeOAuthRateLimited(w, r, secs)
		return
	}
	se, ok := claudeoauth.AsStatusError(err)
	if !ok {
		writeError(w, r, errs.New(errs.CodeOauthProxyUnreachable).
			WithMessage("通过所选代理连接 Claude 失败，授权码未使用；请换代理或重试"))
		return
	}
	switch {
	case se.Status == http.StatusTooManyRequests:
		secs := a.oauthCooldown.extend(key, time.Now(), se.RetryAfter)
		a.writeOAuthRateLimited(w, r, secs)
	case se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden:
		writeError(w, r, errs.New(errs.CodeOauthIdentityRejected).
			WithDetail("upstream_status", se.Status))
	case se.Status >= 500:
		writeError(w, r, errs.New(errs.CodeOauthSupplierRejected).
			WithDetail("upstream_status", se.Status))
	case se.Status == http.StatusBadRequest || se.Status == http.StatusNotFound:
		writeError(w, r, errs.New(errs.CodeOauthExchangeFailed).
			WithDetail("upstream_status", se.Status).
			WithMessage("授权码无效或已使用，请重新生成链接"))
	default:
		writeError(w, r, errs.New(errs.CodeOauthExchangeFailed).
			WithDetail("upstream_status", se.Status))
	}
}

func oauthReachMessage(err error) string {
	if se, ok := claudeoauth.AsStatusError(err); ok {
		return se.Error()
	}
	return ""
}

// checkOAuthProxyReach opens one connection to Claude through the pool proxy
// using the same client the token exchange will use.
func (a *App) checkOAuthProxyReach(ctx context.Context, proxyID string) error {
	client, closeClient, err := a.claudeOAuthHTTPClient(proxyID, "")
	if err != nil {
		return err
	}
	defer closeClient()
	if a.probeOAuthReach != nil {
		return a.probeOAuthReach(ctx, client)
	}
	return claudeoauth.Reachable(ctx, client)
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
