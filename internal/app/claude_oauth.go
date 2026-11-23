package app

import (
	"crypto/sha256"
	"errors"
	"fmt"
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

// Cooldowns have a one-minute floor and no business-duration ceiling.
// Retry-After parsing saturates only at Go's representable duration limit.
const (
	oauthCooldownDefault = time.Minute
	oauthCooldownMin     = time.Minute
)

// oauthGateWait is how long an exchange queues for a slot before OAUTH_BUSY.
const oauthGateWait = 30 * time.Second

// oauthExchangeTimeout covers token + profile + roles including the deliberate
// inter-stage pauses (worst case ~2.4s) plus per-call RTTs through the proxy.
const oauthExchangeTimeout = 60 * time.Second

type oauthPending struct {
	owner                                    [32]byte
	expires                                  time.Time
	proxyID, accountID, email                string
	flow                                     claudeoauth.Pending
	exchanging, submitting, ready, cancelled bool
	tokens                                   *claudeoauth.Result
	codeHash                                 [32]byte
	network                                  *oauthNetwork
	fp                                       *claudeoauth.HeaderProfile
	lease                                    *oauthLease
	completed                                *oauthSubmission
}
type oauthPendingStore struct {
	mu       sync.Mutex
	configMu sync.Mutex
	starts   accountLocks
	items    map[string]oauthPending
	stats    oauthStatistics
}

func (s *oauthPendingStore) sweep(stamp time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, p := range s.items {
		if !stamp.Before(p.expires) && !p.exchanging && !p.submitting {
			s.dropLocked(id)
		}
	}
}
func (s *oauthPendingStore) dropLocked(id string) {
	p, ok := s.items[id]
	if !ok {
		return
	}
	if p.exchanging || p.submitting {
		p.cancelled = true
		s.items[id] = p
		return
	}
	delete(s.items, id)
	if p.network != nil {
		p.network.close()
	}
	if p.lease != nil {
		p.lease.release()
	}
}
func (s *oauthPendingStore) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.items {
		s.dropLocked(id)
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

// remaining reports how long key is still cooling down. An empty key does
// not identify a cooldown; callers use either an account or browser-owner key.
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

// cooldownKey remains stable when a new account refreshes or cancels a link.
// No account identity is invented before OAuth has returned one.
func cooldownKey(accountID string, owner [32]byte) string {
	if accountID != "" {
		return "id:" + accountID
	}
	return fmt.Sprintf("owner:%x", owner)
}

func oauthRetryAfterSeconds(wait time.Duration) time.Duration {
	if wait <= 0 {
		return time.Second
	}
	// Round up without overflowing duration at the numeric limit.
	const maximum = time.Duration(1<<63 - 1)
	if wait > maximum-time.Second {
		return maximum / time.Second * time.Second
	}
	return ((wait + time.Second - 1) / time.Second) * time.Second
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

func (a *App) savedCredentialProxy(accountID string) (string, error) {
	auth, err := a.loadCredential(accountID)
	if err != nil {
		return "", err
	}
	proxyURL, _ := auth["proxy_url"].(string)
	if strings.TrimSpace(proxyURL) == "" {
		// Credentials without a stored URL mean the account was bound direct;
		// that is valid, not an error. Only a populated-but-invalid URL fails.
		return "", nil
	}
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
