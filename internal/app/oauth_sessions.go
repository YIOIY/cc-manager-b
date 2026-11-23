package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"log/slog"
	"math/rand/v2"

	"cc-manager/internal/claudeoauth"
	"cc-manager/internal/errs"
)

type oauthLease struct {
	once   sync.Once
	unlock func()
}

func (l *oauthLease) release() {
	if l != nil {
		l.once.Do(l.unlock)
	}
}

type oauthNetwork struct {
	client      *http.Client
	raw         string
	fingerprint [32]byte
	close       func()
}

// Captures resolved target and upstream settings. Clients are never rebuilt from
// mutable database settings during an exchange; a retry clones this transport.
func (a *App) captureOAuthNetwork(proxyID, fallback string) (*oauthNetwork, error) {
	raw := fallback
	var err error
	if proxyID != "" {
		raw, err = a.resolveProxyURL(proxyID, "")
		if err != nil {
			return nil, err
		}
	}
	fp, err := a.oauthNetworkFingerprint(proxyID, raw)
	if err != nil {
		return nil, err
	}
	client, closeClient, err := a.claudeOAuthHTTPClient("", raw)
	if err != nil {
		return nil, err
	}
	return &oauthNetwork{client: client, raw: raw, fingerprint: fp, close: closeClient}, nil
}
func (a *App) oauthNetworkFingerprint(proxyID, raw string) ([32]byte, error) {
	if proxyID != "" {
		var err error
		raw, err = a.resolveProxyURL(proxyID, "")
		if err != nil {
			return [32]byte{}, err
		}
	}
	settings, err := a.loadProxySettings()
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%d", raw, settings.Mode, settings.UpstreamURL, settings.Username, settings.secret, settings.Version))), nil
}
func (a *App) oauthNetworkMatches(p oauthPending) bool {
	if p.network == nil {
		return true
	} // legacy in-memory fixtures; real sessions always capture.
	fp, err := a.oauthNetworkFingerprint(p.proxyID, p.network.raw)
	return err == nil && fp == p.network.fingerprint
}
func (a *App) oauthKeys(p oauthPending) []string {
	keys := []string{cooldownKey(p.accountID, p.owner)}
	if p.email != "" {
		keys = append(keys, "mail:"+strings.ToLower(strings.TrimSpace(p.email)))
	}
	return keys
}

// oauthIdentityKeys are the keys that name the authorized account itself:
// the local account ID and the e-mail the exchange returned. The browser
// owner key is deliberately absent — it stands in for an identity only while
// a brand-new account has none, so a 429 can be attributed to someone. A
// completed authorization must not throttle the same operator's next,
// unrelated account.
func oauthIdentityKeys(p oauthPending) []string {
	var keys []string
	if p.accountID != "" {
		keys = append(keys, cooldownKey(p.accountID, p.owner))
	}
	if p.email != "" {
		keys = append(keys, "mail:"+strings.ToLower(strings.TrimSpace(p.email)))
	}
	return keys
}

// oauthTypedProbeError reports whether a probe error already carries one of
// the claudeoauth classifications oauthFailure understands.
func oauthTypedProbeError(err error) bool {
	if _, ok := claudeoauth.AsReachBusy(err); ok {
		return true
	}
	if _, ok := claudeoauth.AsStatusError(err); ok {
		return true
	}
	if _, ok := claudeoauth.AsResponseError(err); ok {
		return true
	}
	_, ok := claudeoauth.AsTransportError(err)
	return ok
}

// oauthExitJitterFraction spreads simultaneous cooldown expiries so several
// accounts unlocked by the same retry window do not reopen the floodgate in
// the same instant. Applied only at the read side: stored deadlines keep
// their keep-later semantics, and upstream Retry-After responses bypass this
// jitter so the exact instructed wait is honoured.
const oauthExitJitterFraction = 0.15

// oauthExitJitterCap bounds the additive delay on very long cooldowns.
const oauthExitJitterCap = 30 * time.Second

func (a *App) oauthRemaining(p oauthPending) time.Duration {
	var wait time.Duration
	for _, key := range a.oauthKeys(p) {
		if d := a.oauthCooldown.remaining(key, time.Now()); d > wait {
			wait = d
		}
	}
	if wait <= 0 {
		return 0
	}
	// Package-level rand is safe for the concurrent handlers that call this.
	jitter := time.Duration(float64(wait) * oauthExitJitterFraction * rand.Float64())
	if jitter > oauthExitJitterCap {
		jitter = oauthExitJitterCap
	}
	return wait + jitter
}
func (a *App) extendOAuthCooldown(p oauthPending, wait time.Duration) time.Duration {
	for _, key := range a.oauthKeys(p) {
		wait = a.oauthCooldown.extend(key, time.Now(), wait)
	}
	return wait
}

// selectPreviousPending deterministically picks the owner's live pending OAuth
// session: an exact match on the incoming proxy/account/email triple wins;
// ties fall back to the session expiring last (equivalently, the newest one),
// avoiding Go map's random iteration order when stale duplicates linger.
func selectPreviousPending(items map[string]oauthPending, owner [32]byte, proxyID, accountID, email string) (string, oauthPending) {
	matchProxy, matchAccount, matchEmail := strings.TrimSpace(proxyID), strings.TrimSpace(accountID), strings.ToLower(strings.TrimSpace(email))
	var chosenID string
	var chosen oauthPending
	found := false
	for id, p := range items {
		if p.owner != owner || p.cancelled || p.completed != nil {
			continue
		}
		exact := p.proxyID == matchProxy && p.accountID == matchAccount && p.email == matchEmail
		if !found {
			chosenID, chosen, found = id, p, true
			continue
		}
		prevExact := chosen.proxyID == matchProxy && chosen.accountID == matchAccount && chosen.email == matchEmail
		if exact != prevExact {
			if exact {
				chosenID, chosen = id, p
			}
			continue
		}
		if p.expires.After(chosen.expires) {
			chosenID, chosen = id, p
		}
	}
	return chosenID, chosen
}

func (a *App) claudeOAuthStart(w http.ResponseWriter, r *http.Request) {
	if a.keys == nil {
		fail(w, 503, "ENCRYPTION_KEY_REQUIRED", "请先配置凭证加密密钥")
		return
	}
	var in struct {
		ProxyID   string `json:"proxy_id"`
		AccountID string `json:"account_id"`
		Email     string `json:"email"`
		Refresh   bool   `json:"refresh"`
	}
	if !decodeJSON(w, r, &in, 8192) {
		return
	}
	owner := oauthOwner(r)
	ctx, cancel := context.WithTimeout(r.Context(), oauthGateWait+oauthReachTimeout)
	defer cancel()
	unlockOwner, err := a.oauth.starts.lock(ctx, fmt.Sprintf("%x", owner))
	if err != nil {
		a.writeOAuthBusy(w, r, time.Second)
		return
	}
	defer unlockOwner()
	a.oauth.sweep(time.Now())
	a.oauth.mu.Lock()
	previousID, previous := selectPreviousPending(a.oauth.items, owner, in.ProxyID, in.AccountID, in.Email)
	a.oauth.mu.Unlock()
	if previousID != "" && !in.Refresh {
		body := oauthPendingResponse(previousID, previous)
		if wait := a.oauthRemaining(previous); wait > 0 {
			secs := int(oauthRetryAfterSeconds(wait) / time.Second)
			body["retry_after_seconds"] = secs
			w.Header().Set("Retry-After", fmt.Sprint(secs))
		}
		write(w, 200, body)
		return
	}
	if previousID != "" && (previous.exchanging || previous.submitting) {
		a.writeOAuthBusy(w, r, time.Second)
		return
	}
	p := oauthPending{owner: owner, expires: time.Now().Add(oauthSessionTTL), proxyID: strings.TrimSpace(in.ProxyID), accountID: strings.TrimSpace(in.AccountID), email: strings.ToLower(strings.TrimSpace(in.Email))}
	if p.accountID != "" {
		if !a.requireMutable(w, r, "accounts", p.accountID, "账号不存在") {
			return
		}
		if err = a.DB.QueryRowContext(ctx, `SELECT account FROM accounts WHERE id=?`, p.accountID).Scan(&p.email); err != nil {
			writeError(w, r, storageError(err, "账号读取失败"))
			return
		}
	}
	if wait := a.oauthRemaining(p); wait > 0 {
		a.writeOAuthRateLimited(w, r, wait)
		return
	}
	if previousID != "" {
		if wait := a.oauthRemaining(previous); wait > 0 {
			a.writeOAuthRateLimited(w, r, wait)
			return
		}
	}
	if !a.requireProxyBindable(w, r, p.proxyID) {
		return
	}
	if !a.enforceOAuthProxyBinding(w, r, p) {
		return
	}
	// Serialize lease establishment with ordinary account/proxy writers. Do not
	// hold this mutex over network waits: different owners still get 3 slots.
	a.oauth.configMu.Lock()
	var unlock func()
	sameLease := previousID != "" && previous.accountID == p.accountID && previous.lease != nil
	if sameLease {
		p.lease = previous.lease
	} else if p.accountID != "" {
		var ok bool
		unlock, ok = a.accountLocks.tryLock(p.accountID)
		if !ok {
			a.oauth.configMu.Unlock()
			fail(w, 409, "ACCOUNT_IN_PROGRESS", "账号正在授权或写入，请等待完成")
			return
		}
		p.lease = &oauthLease{unlock: unlock}
	}
	// Establish one stable client presentation before any provider traffic.
	// Existing persisted identities win; a new account derives deterministically
	// and is persisted under the account lease.
	cleanup := func() {
		if p.network != nil {
			p.network.close()
		}
		if !sameLease && p.lease != nil {
			p.lease.release()
		}
	}
	_, profileErr := a.ensureOAuthProfile(ctx, &p)
	if profileErr != nil {
		a.oauth.configMu.Unlock()
		cleanup()
		writeError(w, r, storageError(profileErr, "授权指纹读取失败"))
		return
	}
	if p.accountID != "" {
		var n int
		err = a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE account_id=? AND kind='account.submit' AND state IN ('queued','running')`, p.accountID).Scan(&n)
		if err != nil || n > 0 {
			a.oauth.configMu.Unlock()
			cleanup()
			if err != nil {
				writeError(w, r, storageError(err, "任务状态读取失败"))
			} else {
				fail(w, 409, "ACCOUNT_IN_PROGRESS", "账号有在途提交任务，请等待完成后重新授权")
			}
			return
		}
	}
	fallback := ""
	if p.proxyID == "" && p.accountID != "" {
		fallback, err = a.savedCredentialProxy(p.accountID)
		if err != nil {
			a.oauth.configMu.Unlock()
			cleanup()
			fail(w, 422, "OAUTH_PROXY_INVALID", "已保存代理不可用，请选择代理后重新授权")
			return
		}
	}
	p.network, err = a.captureOAuthNetwork(p.proxyID, fallback)
	if err != nil {
		a.oauth.configMu.Unlock()
		cleanup()
		fail(w, 422, "OAUTH_PROXY_INVALID", "代理配置无效，请检查后重试")
		return
	}
	p.flow, err = claudeoauth.NewPending()
	if err != nil {
		a.oauth.configMu.Unlock()
		cleanup()
		fail(w, 500, "OAUTH_START_FAILED", "无法生成授权链接")
		return
	}
	id := randomString()
	p.exchanging = true // reserves network configuration during probe
	a.oauth.mu.Lock()
	if a.oauth.items == nil {
		a.oauth.items = map[string]oauthPending{}
	}
	if len(a.oauth.items) >= 512 {
		a.oauth.mu.Unlock()
		a.oauth.configMu.Unlock()
		cleanup()
		a.writeOAuthBusy(w, r, time.Second)
		return
	}
	// Explicit replacement retires the previous flow without releasing a reused lease.
	if previousID != "" {
		old := a.oauth.items[previousID]
		if sameLease {
			old.lease = nil
			a.oauth.items[previousID] = old
		}
		a.oauth.dropLocked(previousID)
	}
	a.oauth.items[id] = p
	a.oauth.mu.Unlock()
	a.oauth.configMu.Unlock()
	defer a.releaseOAuthPending(id)
	queueStart := time.Now()
	release, err := a.oauthGate.Load().acquire(ctx, oauthGateWait)
	a.oauth.stats.observe("queue", 0, time.Since(queueStart), err == nil)
	if err != nil {
		a.dropOAuthPending(id)
		a.writeOAuthBusy(w, r, time.Second)
		return
	}
	defer release()
	if wait := a.oauthRemaining(p); wait > 0 {
		a.dropOAuthPending(id)
		a.writeOAuthRateLimited(w, r, wait)
		return
	}
	if p.proxyID != "" {
		// Direct connections are verified implicitly by the first provider
		// call; only proxied exits need an upfront reachability gate.
		if s, e := a.loadOAuthDispatchSettings(); e == nil && s.ProbeCacheSeconds > 0 && a.oauthProbeCache.remaining(p.network.fingerprint, time.Now()) > 0 {
			// cached probe: do not call provider again.
		} else {
			probeCtx, stop := context.WithTimeout(ctx, oauthReachTimeout)
			defer stop()
			probeCtx = a.observeOAuthContext(probeCtx, r, p, id)
			if a.probeOAuthReach != nil {
				at := time.Now()
				err = a.probeOAuthReach(probeCtx, p.network.client)
				a.oauth.stats.observe("reach", 0, time.Since(at), err == nil)
			} else {
				err = claudeoauth.Reachable(probeCtx, p.network.client)
			}
			if err == nil {
				if s, e2 := a.loadOAuthDispatchSettings(); e2 == nil && s.ProbeCacheSeconds > 0 {
					a.oauthProbeCache.store(p.network.fingerprint, time.Now(), time.Duration(s.ProbeCacheSeconds)*time.Second)
				}
			}
		}
	}
	if err != nil {
		// Reachable already classifies its outcomes (ReachBusy, StatusError,
		// ResponseError, TransportError) and oauthFailure maps each one: a 429
		// cools the identity down instead of blaming the exit. Only an untyped
		// error — a test stub or a bare dial failure — is a transport failure.
		if !oauthTypedProbeError(err) {
			err = &claudeoauth.TransportError{Op: "reach"}
		}
		a.oauthFailure(w, r, p, id, err)
		a.dropOAuthPending(id)
		return
	}
	a.oauth.mu.Lock()
	current, ok := a.oauth.items[id]
	if ok {
		current.exchanging = false
		a.oauth.items[id] = current
	}
	a.oauth.mu.Unlock()
	if !ok || current.cancelled {
		fail(w, 404, "OAUTH_SESSION_EXPIRED", "授权已取消")
		return
	}
	if p.accountID != "" {
		a.auditFor(r, "account.oauth.start", "account", p.accountID, "session="+id)
	}
	write(w, 201, oauthPendingResponse(id, p))
}
func oauthPendingResponse(id string, p oauthPending) map[string]any {
	stage := "pending"
	if p.ready {
		stage = "ready"
	} else if p.tokens != nil {
		stage = "profile"
	}
	return map[string]any{"id": id, "authorization_url": p.flow.URL, "expires_at": p.expires.UTC().Format(time.RFC3339), "proxy_id": p.proxyID, "account_id": p.accountID, "stage": stage}
}
func (a *App) claudeOAuthCancel(w http.ResponseWriter, r *http.Request) {
	a.oauth.mu.Lock()
	id := r.PathValue("id")
	if p, ok := a.oauth.items[id]; ok && p.owner == oauthOwner(r) {
		a.oauth.dropLocked(id)
	}
	a.oauth.mu.Unlock()
	write(w, 200, map[string]bool{"cancelled": true})
}
func (a *App) dropOAuthPending(id string) {
	a.oauth.mu.Lock()
	a.oauth.dropLocked(id)
	a.oauth.mu.Unlock()
}
func (a *App) releaseOAuthPending(id string) {
	a.oauth.mu.Lock()
	defer a.oauth.mu.Unlock()
	if p, ok := a.oauth.items[id]; ok {
		p.exchanging = false
		a.oauth.items[id] = p
		if p.cancelled || !time.Now().Before(p.expires) {
			a.oauth.dropLocked(id)
		}
	}
}
func (a *App) cacheOAuthResult(id string, result claudeoauth.Result, codeHash [32]byte, ready bool) {
	a.oauth.mu.Lock()
	defer a.oauth.mu.Unlock()
	if p, ok := a.oauth.items[id]; ok && !p.cancelled {
		p.tokens = &result
		p.codeHash = codeHash
		p.ready = ready
		if email, _ := result.Auth["email"].(string); email != "" {
			p.email = email
		}
		a.oauth.items[id] = p
	}
}
func (a *App) claudeOAuthExchange(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Result string `json:"result"`
	}
	if !decodeJSON(w, r, &in, 16384) {
		return
	}
	id := r.PathValue("id")
	a.oauth.sweep(time.Now())
	a.oauth.mu.Lock()
	p, ok := a.oauth.items[id]
	if !ok || p.cancelled || p.owner != oauthOwner(r) || !time.Now().Before(p.expires) {
		a.oauth.mu.Unlock()
		fail(w, 404, "OAUTH_SESSION_EXPIRED", "授权会话已失效")
		return
	}
	if p.exchanging || p.submitting {
		a.oauth.mu.Unlock()
		a.writeOAuthBusy(w, r, time.Second)
		return
	}
	// Once tokens are cached the code has been spent and is no longer needed:
	// an empty result resumes the session (profile stage or a ready replay),
	// which is what the browser sends when it recovers a session it cannot
	// paste a code for. A non-empty result must still be this session's code.
	var code string
	var err error
	hash := p.codeHash
	if p.tokens == nil || strings.TrimSpace(in.Result) != "" {
		code, err = claudeoauth.ParseResult(in.Result, p.flow.State)
		if err != nil {
			a.oauth.mu.Unlock()
			fail(w, 422, "OAUTH_RESULT_INVALID", err.Error())
			return
		}
		hash = sha256.Sum256([]byte(code))
		if p.tokens != nil && p.codeHash != hash {
			a.oauth.mu.Unlock()
			fail(w, 422, "OAUTH_RESULT_INVALID", "请使用本次授权结果")
			return
		}
	}
	if p.ready {
		result := *p.tokens
		a.oauth.mu.Unlock()
		a.writeOAuthResult(w, id, result)
		return
	}
	p.exchanging = true
	a.oauth.items[id] = p
	a.oauth.mu.Unlock()
	defer a.releaseOAuthPending(id)
	if wait := a.oauthRemaining(p); wait > 0 {
		a.writeOAuthRateLimited(w, r, wait)
		return
	}
	// One queue budget covers the global gate, the per-exit gate and the exit
	// pacing together, so queueing can never add up to more than oauthGateWait.
	// It follows the request: a browser that gives up while queued has not
	// spent anything yet.
	ctx, cancel := context.WithTimeout(r.Context(), oauthGateWait)
	defer cancel()
	at := time.Now()
	release, err := a.oauthGate.Load().acquire(ctx, oauthGateWait)
	a.oauth.stats.observe("queue", 0, time.Since(at), err == nil)
	if err != nil {
		a.writeOAuthBusy(w, r, time.Second)
		return
	}
	defer release()
	exitKey := "direct"
	if p.network != nil && p.network.raw != "" {
		exitKey = p.network.raw
	}
	if gate := a.oauthExitGate.Load(); gate != nil {
		releaseExit, err := gate.acquire(ctx, exitKey)
		if err != nil {
			a.writeOAuthBusy(w, r, time.Second)
			return
		}
		defer releaseExit()
	}
	if s, e := a.loadOAuthDispatchSettings(); e == nil {
		releasePacing, err := oauthExitPacing.wait(ctx, exitKey, time.Duration(s.ExchangeIntervalSeconds)*time.Second)
		if err != nil {
			a.writeOAuthBusy(w, r, time.Second)
			return
		}
		defer releasePacing()
	}
	a.oauth.mu.Lock()
	cur, exists := a.oauth.items[id]
	valid := exists && !cur.cancelled && time.Now().Before(cur.expires)
	a.oauth.mu.Unlock()
	if !valid {
		fail(w, 404, "OAUTH_SESSION_EXPIRED", "授权会话已失效")
		return
	}
	if wait := a.oauthRemaining(p); wait > 0 {
		a.writeOAuthRateLimited(w, r, wait)
		return
	}
	if ctx.Err() != nil {
		a.writeOAuthBusy(w, r, time.Second)
		return
	}
	if p.accountID != "" && !a.requireMutable(w, r, "accounts", p.accountID, "账号不存在") {
		return
	}
	if !a.oauthNetworkMatches(p) {
		fail(w, 409, "OAUTH_PROXY_CHANGED", "授权出口配置已变化，请显式作废并重新授权")
		return
	}
	// Tests and sessions created by older code may lack a captured client.
	if p.network == nil {
		fallback := ""
		if p.accountID != "" && p.proxyID == "" {
			fallback, err = a.savedCredentialProxy(p.accountID)
		}
		if err == nil {
			p.network, err = a.captureOAuthNetwork(p.proxyID, fallback)
		}
		if err != nil {
			fail(w, 422, "OAUTH_PROXY_INVALID", "代理不可用")
			return
		}
		a.oauth.mu.Lock()
		cur = a.oauth.items[id]
		cur.network = p.network
		a.oauth.items[id] = cur
		a.oauth.mu.Unlock()
	}
	// From here the one-time code is about to be consumed. The upstream calls
	// are detached from the browser's connection: if the client times out or
	// closes the tab mid-exchange, the exchange still completes and its result
	// is cached on the pending, so the next request replays it instead of
	// discovering that the code was spent for nothing.
	exchangeCtx, stop := context.WithTimeout(context.WithoutCancel(r.Context()), oauthExchangeTimeout)
	defer stop()
	exchangeCtx = a.observeOAuthContext(exchangeCtx, r, p, id)
	var result claudeoauth.Result
	if p.tokens != nil {
		result = *p.tokens
	} else {
		result, err = claudeoauth.Token(exchangeCtx, p.network.client, code, p.flow.State, p.flow.Verifier)
		if te, ok := claudeoauth.AsTransportError(err); ok && te.BeforeWrite {
			// Same endpoint/settings, fresh connection pool. Never silently change exits.
			if transport, ok := p.network.client.Transport.(*http.Transport); ok {
				clone := transport.Clone()
				retryClient := *p.network.client
				retryClient.Transport = clone
				p.network.close()
				p.network.client = &retryClient
				p.network.close = clone.CloseIdleConnections
				result, err = claudeoauth.Token(exchangeCtx, p.network.client, code, p.flow.State, p.flow.Verifier)
			}
		}
		if err == nil {
			a.cacheOAuthResult(id, result, hash, false)
		}
	}
	if err == nil {
		result, err = claudeoauth.CompleteProfile(exchangeCtx, p.network.client, result)
	}
	if result.Auth != nil {
		a.cacheOAuthResult(id, result, hash, err == nil)
		if email, _ := result.Auth["email"].(string); email != "" {
			p.email = strings.ToLower(strings.TrimSpace(email))
			if p.fp != nil {
				a.persistOAuthProfile(exchangeCtx, p.accountID, p.email, *p.fp)
			}
		}
	}
	if err != nil {
		// Cached tokens that expired while the session waited can never finish
		// this session; retire it so the browser asks for a fresh authorization
		// instead of retrying the same stage forever.
		if errors.Is(err, claudeoauth.ErrTokenExpired) {
			a.dropOAuthPending(id)
			writeError(w, r, errs.New(errs.CodeOauthSessionExpired).WithDetail("oauth_session_expired", true))
			return
		}
		// Never destroy a usable token because a later profile stage failed.
		if p.tokens == nil && result.Auth == nil && oauthTerminalError(err) {
			a.dropOAuthPending(id)
		}
		a.oauthFailure(w, r, p, id, err)
		return
	}
	// Seed the post-success cooldown for the identity the exchange learned
	// (accountID and email), so a different browser session hitting start with
	// the same account honors the same window. The owner key is left alone:
	// it would otherwise throttle this operator's next, unrelated account
	// ("继续添加下一个") as if Claude had rate-limited it. 429 extensions keep
	// deadlines via oauthCooldown.extend's keep-the-later semantics.
	for _, key := range oauthIdentityKeys(p) {
		a.oauthCooldown.extend(key, time.Now(), oauthCooldownDefault)
	}
	if err := a.bindAccountProxy(p.accountID, p.proxyID); err != nil {
		slog.Warn("oauth proxy binding write-back failed", "account", p.accountID, "err", err)
	}
	a.auditFor(r, "account.oauth", "account", p.accountID, "session="+id+" ready")
	a.writeOAuthResult(w, id, result)
}
func (a *App) writeOAuthResult(w http.ResponseWriter, id string, result claudeoauth.Result) {
	encoded, _ := json.Marshal(result)
	var body map[string]any
	json.Unmarshal(encoded, &body)
	body["oauth_session_id"] = id
	write(w, 200, body)
}

// oauthTerminalStatus marks authorization codes whose outcome cannot succeed
// on retry with the same session. 400/404/409 are malformed or conflicting
// submissions; 401/403 indicate a rejected identity (suspected ban), so the
// pending is retired and the browser must start a fresh authorization.
func oauthTerminalStatus(status int) bool {
	return status == 400 || status == 401 || status == 403 || status == 404 || status == 409
}

// An intercepted 401/403 (non-JSON page from the exit, see
// StatusError.Intercepted) never reached the OAuth server: the code is still
// unspent, so the session survives and the failure is the exit's, not the
// identity's.
func oauthTerminalError(err error) bool {
	if se, ok := claudeoauth.AsStatusError(err); ok {
		return se.Op == "token" && oauthTerminalStatus(se.Status) && !se.Intercepted
	}
	if re, ok := claudeoauth.AsResponseError(err); ok {
		return re.Op == "token"
	}
	return false
}
func (a *App) writeOAuthBusy(w http.ResponseWriter, r *http.Request, wait time.Duration) {
	secs := int(oauthRetryAfterSeconds(wait) / time.Second)
	writeError(w, r, errs.New(errs.CodeOauthBusy).WithDetail("retry_after_seconds", secs))
}

// Kept for callers/tests that classify an error without an active session.
func (a *App) writeOAuthExchangeError(w http.ResponseWriter, r *http.Request, key string, err error) {
	if se, ok := claudeoauth.AsStatusError(err); ok && se.Status == 429 {
		a.writeOAuthRateLimited(w, r, a.oauthCooldown.extend(key, time.Now(), se.RetryAfter))
		return
	}
	if re, ok := claudeoauth.AsReachBusy(err); ok {
		a.writeOAuthRateLimited(w, r, a.oauthCooldown.extend(key, time.Now(), re.RetryAfter))
		return
	}
	a.oauthFailure(w, r, oauthPending{}, "", err)
}
func (a *App) oauthFailure(w http.ResponseWriter, r *http.Request, p oauthPending, id string, err error) {
	stage := "token"
	code := "OAUTH_RETRYABLE"
	status := 0
	if busy, ok := claudeoauth.AsReachBusy(err); ok {
		a.noteOAuth429(r, p, id, "reach")
		a.writeOAuthRateLimited(w, r, a.extendOAuthCooldown(p, busy.RetryAfter))
		return
	}
	if se, ok := claudeoauth.AsStatusError(err); ok {
		stage, status = se.Op, se.Status
		switch {
		case status == 429:
			a.noteOAuth429(r, p, id, stage)
			a.writeOAuthRateLimited(w, r, a.extendOAuthCooldown(p, se.RetryAfter))
			return
		case se.Intercepted:
			code = "OAUTH_PROXY_UNREACHABLE"
		case status == 401 || status == 403:
			code = "OAUTH_IDENTITY_REJECTED"
		case status >= 500:
			code = "OAUTH_SUPPLIER_REJECTED"
		case stage == "token" && oauthTerminalStatus(status):
			code = "OAUTH_EXCHANGE_FAILED"
		default:
			code = "OAUTH_SUPPLIER_REJECTED"
		}
	}
	if te, ok := claudeoauth.AsTransportError(err); ok {
		stage = te.Op
		code = "OAUTH_PROXY_UNREACHABLE"
	}
	if re, ok := claudeoauth.AsResponseError(err); ok {
		stage = re.Op
		code = "OAUTH_RESPONSE_INVALID"
	}
	e := errs.Adhoc(code, 0, "").WithDetail("oauth_stage", stage).WithDetail("upstream_status", status)
	if id != "" {
		e = e.WithDetail("oauth_session_id", id)
	}
	if oauthTerminalError(err) {
		e = e.WithDetail("oauth_session_expired", true)
	}
	writeError(w, r, e)
}
func (a *App) noteOAuth429(r *http.Request, p oauthPending, id, stage string) {
	a.oauthStatCounters.rateLimited429.Add(1)
	a.auditFor(r, "account.oauth.rate_limited", "account", p.accountID, "session="+id+" stage="+stage)
}

// enforceOAuthProxyBinding pins a re-authorization to the account's bound
// egress once one exists (accounts.proxy_id). Switching to another proxy or
// dropping to direct requires the explicit PATCH on the account, never a
// silent proxy_id override in the OAuth start payload. Accounts without a
// binding yet (legacy imports, brand-new rows) may pick any exit; whichever
// one the exchange actually uses is written back by bindAccountProxy.
// Returns false after writing a 409 response.
func (a *App) enforceOAuthProxyBinding(w http.ResponseWriter, r *http.Request, p oauthPending) bool {
	if p.accountID == "" {
		return true
	}
	var bound string
	if err := a.DB.QueryRowContext(r.Context(), `SELECT COALESCE(proxy_id,'') FROM accounts WHERE id=?`, p.accountID).Scan(&bound); err != nil {
		writeError(w, r, storageError(err, "账号代理绑定读取失败"))
		return false
	}
	if bound == "" || bound == p.proxyID {
		return true
	}
	if p.proxyID == "" {
		fail(w, http.StatusConflict, "PROXY_BINDING_CHANGED",
			"该账号已绑定专用代理，请在授权时使用同一代理；如需更换请先在账号设置中重绑")
		return false
	}
	fail(w, http.StatusConflict, "PROXY_BINDING_CHANGED",
		"该账号已绑定其他代理，请使用绑定代理授权或先显式重绑")
	return false
}

// bindAccountProxy persists the egress an OAuth exchange actually used, so
// worker submit and future re-authorizations stay on the same exit.
func (a *App) bindAccountProxy(accountID, proxyID string) error {
	if accountID == "" {
		return nil
	}
	stamp := now()
	_, err := a.DB.Exec(`UPDATE accounts SET proxy_id=?,updated_at=? WHERE id=?`, proxyID, stamp, accountID)
	return err
}
