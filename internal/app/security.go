package app

import (
	"cc-manager/internal/errs"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie        = "cc_manager_session"
	authenticatedTTL     = 12 * time.Hour
	anonymousTTL         = 30 * time.Minute
	maxSessions          = 10000
	loginFreeAttempts    = 3
	loginMaxBackoff      = 15 * time.Minute
	loginFailureMemory   = time.Hour
	loginLimiterMaxItems = 10000
)

type sessionState struct {
	csrf    string
	expires time.Time
	auth    bool
	userID  string
}

// sessionUserInfo is what handlers see about the caller.
type sessionUserInfo struct {
	ID         string
	Username   string
	CanOnboard bool
	Access     access
}

const sessionUserKey ctxKey = 2

func sessionUser(r *http.Request) sessionUserInfo {
	u, _ := r.Context().Value(sessionUserKey).(sessionUserInfo)
	return u
}

// sessionStore keeps sessions in memory. Anonymous sessions are short-lived
// and the total count is bounded so an unauthenticated client cannot grow the
// process without limit.
type sessionStore struct {
	mu    sync.Mutex
	items map[string]sessionState
}

func newSessionStore() *sessionStore { return &sessionStore{items: map[string]sessionState{}} }

func (s *sessionStore) get(id string) (sessionState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.items[id]
	if !ok {
		return sessionState{}, false
	}
	if time.Now().After(state.expires) {
		delete(s.items, id)
		return sessionState{}, false
	}
	return state, true
}

func (s *sessionStore) put(id string, state sessionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) >= maxSessions {
		s.sweepLocked(time.Now())
		if len(s.items) >= maxSessions {
			s.evictOldestAnonymousLocked()
		}
	}
	s.items[id] = state
}

func (s *sessionStore) delete(id string) {
	s.mu.Lock()
	delete(s.items, id)
	s.mu.Unlock()
}

func (s *sessionStore) sweep(now time.Time) {
	s.mu.Lock()
	s.sweepLocked(now)
	s.mu.Unlock()
}

func (s *sessionStore) sweepLocked(now time.Time) {
	for id, state := range s.items {
		if now.After(state.expires) {
			delete(s.items, id)
		}
	}
}

func (s *sessionStore) evictOldestAnonymousLocked() {
	var oldest string
	var when time.Time
	for id, state := range s.items {
		if !state.auth && (oldest == "" || state.expires.Before(when)) {
			oldest, when = id, state.expires
		}
	}
	if oldest != "" {
		delete(s.items, oldest)
	}
}

// revokeUser drops every session of one user (role change, disable, delete).
func (s *sessionStore) revokeUser(userID string) { s.revokeUserExcept(userID, "") }

func (s *sessionStore) revokeUserExcept(userID, keep string) {
	s.mu.Lock()
	for id, state := range s.items {
		if state.userID == userID && id != keep {
			delete(s.items, id)
		}
	}
	s.mu.Unlock()
}

func (s *sessionStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

// loginLimiter applies exponential backoff per client IP and username after a
// few failed attempts. It is deliberately simple: a single process, memory
// only, reset on restart. nginx keeps a second limit in front of it.
type loginLimiter struct {
	mu    sync.Mutex
	items map[string]loginFailure
}

type loginFailure struct {
	count     int
	lastFail  time.Time
	lockUntil time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{items: map[string]loginFailure{}} }

// retryAfter reports how long the caller must wait before another attempt.
func (l *loginLimiter) retryAfter(key string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, ok := l.items[key]
	if !ok {
		return 0
	}
	if now.Sub(f.lastFail) > loginFailureMemory {
		delete(l.items, key)
		return 0
	}
	if f.lockUntil.After(now) {
		return f.lockUntil.Sub(now)
	}
	return 0
}

func (l *loginLimiter) fail(key string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.items) >= loginLimiterMaxItems {
		for k, v := range l.items {
			if now.Sub(v.lastFail) > loginFailureMemory {
				delete(l.items, k)
			}
		}
	}
	f := l.items[key]
	if now.Sub(f.lastFail) > loginFailureMemory {
		f = loginFailure{}
	}
	f.count++
	f.lastFail = now
	var delay time.Duration
	if f.count >= loginFreeAttempts {
		delay = time.Duration(1<<uint(min(f.count-loginFreeAttempts, 12))) * time.Second
		if delay > loginMaxBackoff {
			delay = loginMaxBackoff
		}
	}
	f.lockUntil = now.Add(delay)
	l.items[key] = f
	return delay
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.items, key)
	l.mu.Unlock()
}

func (l *loginLimiter) sweep(now time.Time) {
	l.mu.Lock()
	for k, v := range l.items {
		if now.Sub(v.lastFail) > loginFailureMemory {
			delete(l.items, k)
		}
	}
	l.mu.Unlock()
}

func randomString() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// randomDeviceID returns a random UUID v4 for optional Claude device placeholders.
func randomDeviceID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (a *App) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, a.requestHost(r)) && (u.Scheme == "http" || u.Scheme == "https")
}

func (a *App) currentSession(r *http.Request) (string, sessionState, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", sessionState{}, false
	}
	s, ok := a.sessions.get(c.Value)
	if !ok {
		return "", sessionState{}, false
	}
	return c.Value, s, true
}

func (a *App) issueSession(w http.ResponseWriter, r *http.Request, userID string) sessionState {
	authenticated := userID != ""
	ttl := anonymousTTL
	if authenticated {
		ttl = authenticatedTTL
	}
	id := randomString()
	s := sessionState{csrf: randomString(), auth: authenticated, userID: userID, expires: time.Now().Add(ttl)}
	a.sessions.put(id, s)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.requestIsTLS(r), MaxAge: int(ttl / time.Second)})
	return s
}

func (a *App) revokeSession(w http.ResponseWriter, r *http.Request) {
	if id, _, ok := a.currentSession(r); ok {
		a.sessions.delete(id)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.requestIsTLS(r)})
}

func csrfValid(r *http.Request, s sessionState) bool {
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(s.csrf)) == 1
}

// authSession returns the current session state, issuing an anonymous one
// when the client has none so it can obtain a CSRF token for login.
func (a *App) authSession(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		fail(w, http.StatusForbidden, "ORIGIN_FAILED", "请求来源不匹配")
		return
	}
	_, s, ok := a.currentSession(r)
	if !ok {
		s = a.issueSession(w, r, "")
	}
	body := map[string]any{"authenticated": false, "csrf_token": s.csrf}
	if s.auth {
		if u, err := a.userByID(s.userID); err == nil && u.Enabled {
			ac, _ := a.userAccess(u.ID)
			body["authenticated"] = true
			body["user"] = sessionUserPayload(u, ac)
		}
	}
	write(w, http.StatusOK, body)
}

func (a *App) authLogin(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		fail(w, http.StatusForbidden, "ORIGIN_FAILED", "请求来源不匹配")
		return
	}
	id, s, ok := a.currentSession(r)
	if !ok || !csrfValid(r, s) {
		fail(w, http.StatusForbidden, "CSRF_FAILED", "会话验证失败")
		return
	}
	var in struct {
		Username  string `json:"username"`
		Password  string `json:"password"`
		CaptchaID string `json:"captcha_id"`
		Captcha   string `json:"captcha"`
	}
	if !decodeJSON(w, r, &in, 16<<10) {
		return
	}
	ip := a.clientIP(r)
	key := ip + "|" + strings.ToLower(strings.TrimSpace(in.Username))
	now := time.Now()
	if wait := a.logins.retryAfter(key, now); wait > 0 {
		writeError(w, r, errs.New(errs.CodeAuthRateLimited).WithDetail("retry_after_seconds", int(wait/time.Second)+1))
		return
	}
	if !a.verifyLoginCaptcha(in.CaptchaID, in.Captcha) {
		writeError(w, r, errs.New(errs.CodeCaptchaInvalid))
		return
	}
	username := strings.TrimSpace(in.Username)
	user, hash, err := a.userByName(username)
	// Run the hash even for unknown users so timing does not reveal existence.
	if err != nil {
		hash = unknownUserHash
	}
	if !verifyPassword(in.Password, "", hash) || err != nil || !user.Enabled {
		wait := a.logins.fail(key, now)
		a.auditAs(username, "auth.login_failed", "user", username, ip)
		if wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
		}
		fail(w, http.StatusUnauthorized, "AUTH_INVALID", "用户名或密码错误")
		return
	}
	a.logins.reset(key)
	a.sessions.delete(id)
	s = a.issueSession(w, r, user.ID)
	_, _ = a.DB.Exec(`UPDATE users SET last_login_at=? WHERE id=?`, now.UTC().Format(time.RFC3339), user.ID)
	a.auditAs(user.Username, "auth.login", "user", user.ID, ip)
	ac, _ := a.userAccess(user.ID)
	write(w, http.StatusOK, map[string]any{"authenticated": true, "csrf_token": s.csrf, "user": sessionUserPayload(user, ac)})
}

// unknownUserHash is a valid argon2id hash of a random secret; verifying
// against it costs the same as a real check.
var unknownUserHash = func() string { h, _ := HashPassword(randomString()); return h }()

func (a *App) authLogout(w http.ResponseWriter, r *http.Request) {
	me := sessionUser(r)
	a.revokeSession(w, r)
	a.auditAs(me.Username, "auth.logout", "user", me.ID, a.clientIP(r))
	write(w, http.StatusOK, map[string]any{"authenticated": false})
}

// protect enforces same-origin, an authenticated session and, for mutating
// requests, the CSRF token bound to that session.
func (a *App) protect(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.sameOrigin(r) {
			fail(w, http.StatusForbidden, "ORIGIN_FAILED", "请求来源不匹配")
			return
		}
		id, s, ok := a.currentSession(r)
		if !ok || !s.auth {
			fail(w, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !csrfValid(r, s) {
				fail(w, http.StatusForbidden, "CSRF_FAILED", "会话验证失败")
				return
			}
			if ok, wait := a.writes.allow(s.userID, time.Now()); !ok {
				writeError(w, r, errs.New(errs.CodeRateLimited).WithDetail("retry_after_seconds", int(wait.Seconds())+1))
				return
			}
		}
		// The user row is authoritative: a disabled or deleted user loses
		// access on the next request even with a valid cookie.
		u, err := a.userByID(s.userID)
		if err != nil || !u.Enabled {
			a.sessions.delete(id)
			writeError(w, r, errs.New(errs.CodeUserDisabled))
			return
		}
		ac, err := a.userAccess(u.ID)
		if err != nil {
			writeError(w, r, storageError(err, "读取权限失败"))
			return
		}
		ctx := context.WithValue(r.Context(), sessionUserKey, sessionUserInfo{ID: u.ID, Username: u.Username, CanOnboard: u.CanOnboard, Access: ac})
		next(w, r.WithContext(ctx))
	}
}

// require wraps a handler with a permission check; protect must run first.
func (a *App) require(p Permission, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !sessionUser(r).Access.allows(p) {
			writeError(w, r, errs.New(errs.CodePermissionDenied).WithDetail("permission", string(p)))
			return
		}
		next(w, r)
	}
}

// requireCanOnboard rejects onboarding-related writes when the user flag is off.
// Compose after require(...) so RBAC denial stays PERMISSION_DENIED.
func (a *App) requireCanOnboard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !sessionUser(r).CanOnboard {
			writeError(w, r, errs.New(errs.CodeOnboardDisabled))
			return
		}
		next(w, r)
	}
}

// requireAny passes when the user holds at least one of the permissions.
func (a *App) requireAny(ps []Permission, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ac := sessionUser(r).Access
		for _, p := range ps {
			if ac.allows(p) {
				next(w, r)
				return
			}
		}
		names := make([]string, 0, len(ps))
		for _, p := range ps {
			names = append(names, string(p))
		}
		writeError(w, r, errs.New(errs.CodePermissionDenied).WithDetail("permission", strings.Join(names, "|")))
	}
}

// requireFn resolves the permission from the request (method or path) for
// handlers that dispatch internally.
func (a *App) requireFn(pick func(*http.Request) Permission, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.require(pick(r), next)(w, r)
	}
}

// byMethod maps HTTP methods to permissions; GET/HEAD share the read one.
func byMethod(read Permission, writes map[string]Permission) func(*http.Request) Permission {
	return func(r *http.Request) Permission {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			return read
		}
		if p, ok := writes[r.Method]; ok {
			return p
		}
		return Permission("deny") // unknown method: never granted, handler returns 405 after auth
	}
}
