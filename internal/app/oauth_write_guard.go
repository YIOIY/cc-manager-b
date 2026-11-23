package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"cc-manager/internal/errs"
)

type accountWriteContext struct{}

func (a *App) lockAccountWrite(ctx context.Context, id string) (func(), error) {
	if held, ok := ctx.Value(accountWriteContext{}).(map[string]bool); ok && held[id] {
		return func() {}, nil
	}
	return a.accountLocks.lock(ctx, id)
}

type oauthSubmission struct {
	status int
	body   []byte
	hash   [32]byte
	path   string
}
type oauthBufferedWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *oauthBufferedWriter) Header() http.Header { return w.header }
func (w *oauthBufferedWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *oauthBufferedWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.body.Write(p)
}
func (w *oauthBufferedWriter) flush(out http.ResponseWriter) {
	for k, v := range w.header {
		out.Header()[k] = v
	}
	out.WriteHeader(w.status)
	out.Write(w.body.Bytes())
}

// All HTTP account mutations use the same lock as the submit worker. An OAuth
// proof borrows its retained lease; other writers receive an actionable 409.
// configMu establishes a fixed lock order for proxy changes and lease creation.
func (a *App) guardOAuthWrites(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" || r.Method == "HEAD" || strings.Contains(r.URL.Path, "/claude-oauth/") {
			next(w, r)
			return
		}
		path := r.URL.Path
		accountRoute := strings.HasPrefix(path, "/api/v1/accounts")
		proxyRoute := strings.HasPrefix(path, "/api/v1/proxies/") || path == "/api/v1/proxy-settings"
		if !accountRoute && !proxyRoute {
			next(w, r)
			return
		}
		if strings.HasSuffix(path, "/preflight") || strings.HasSuffix(path, "/test") {
			next(w, r)
			return
		}
		id := ""
		if accountRoute {
			id = r.PathValue("id")
		}
		a.oauth.sweep(time.Now())
		var input map[string]any
		var original []byte
		if accountRoute && r.Body != nil {
			var err error
			original, err = io.ReadAll(http.MaxBytesReader(w, r.Body, defaultBodyLimit))
			if err != nil {
				fail(w, 413, "PAYLOAD_TOO_LARGE", "请求体过大")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(original))
			_ = json.Unmarshal(original, &input)
		}
		proof := str(input, "oauth_session_id")
		if proof != "" {
			a.guardOAuthSubmission(w, r, input, original, id, proof, next)
			return
		}
		a.oauth.configMu.Lock()
		var ids []string
		if id != "" {
			ids = append(ids, id)
		}
		proxyID := ""
		if proxyRoute {
			proxyID = r.PathValue("id")
		} else if strings.HasSuffix(path, "/assign") {
			_ = a.DB.QueryRowContext(r.Context(), `SELECT COALESCE(proxy_id,'') FROM accounts WHERE id=?`, id).Scan(&proxyID)
		}
		email := strings.ToLower(strings.TrimSpace(str(input, "account")))
		a.oauth.mu.Lock()
		conflict := false
		for _, p := range a.oauth.items {
			if p.cancelled || p.completed != nil {
				continue
			}
			if (id != "" && p.accountID == id) || (email != "" && strings.EqualFold(p.email, email)) || (proxyID != "" && p.proxyID == proxyID) || (path == "/api/v1/proxy-settings") {
				conflict = true
				break
			}
		}
		a.oauth.mu.Unlock()
		if conflict {
			a.oauth.configMu.Unlock()
			fail(w, 409, "ACCOUNT_IN_PROGRESS", "账号正在授权，请完成或显式取消授权后再修改")
			return
		}
		if proxyRoute {
			query := `SELECT id FROM accounts`
			var args []any
			if proxyID != "" {
				query += ` WHERE proxy_id=?`
				args = append(args, proxyID)
			}
			rows, err := a.DB.QueryContext(r.Context(), query, args...)
			if err != nil {
				a.oauth.configMu.Unlock()
				writeError(w, r, storageError(err, "关联账号读取失败"))
				return
			}
			for rows.Next() {
				var key string
				if rows.Scan(&key) == nil {
					ids = append(ids, key)
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				a.oauth.configMu.Unlock()
				writeError(w, r, storageError(err, "关联账号读取失败"))
				return
			}
		}
		sort.Strings(ids)
		held := map[string]bool{}
		releases := []func(){}
		release := func() {
			for i := len(releases) - 1; i >= 0; i-- {
				releases[i]()
			}
		}
		for _, key := range ids {
			if held[key] {
				continue
			}
			unlock, ok := a.accountLocks.tryLock(key)
			if !ok {
				release()
				a.oauth.configMu.Unlock()
				fail(w, 409, "ACCOUNT_IN_PROGRESS", "账号正在写入，请稍后再试")
				return
			}
			held[key] = true
			releases = append(releases, unlock)
		}
		// Proxy and assignment writes retain topology mutex through their transaction.
		if !proxyRoute && !strings.HasSuffix(path, "/assign") {
			a.oauth.configMu.Unlock()
		} else {
			defer a.oauth.configMu.Unlock()
		}
		defer release()
		next(w, r.WithContext(context.WithValue(r.Context(), accountWriteContext{}, held)))
	}
}
func (a *App) guardOAuthSubmission(w http.ResponseWriter, r *http.Request, input map[string]any, original []byte, id, proof string, next http.HandlerFunc) {
	if !strings.HasSuffix(r.URL.Path, "/onboard") && !strings.HasSuffix(r.URL.Path, "/reauthorize") {
		fail(w, 409, "OAUTH_SESSION_CONFLICT", "授权会话只能用于本次凭证提交")
		return
	}
	digest := sha256.Sum256(original)
	a.oauth.configMu.Lock()
	a.oauth.mu.Lock()
	p, ok := a.oauth.items[proof]
	if !ok || p.cancelled || p.owner != oauthOwner(r) || !time.Now().Before(p.expires) {
		a.oauth.mu.Unlock()
		a.oauth.configMu.Unlock()
		fail(w, 404, "OAUTH_SESSION_EXPIRED", "授权会话已失效，请重新授权")
		return
	}
	if p.accountID != id {
		a.oauth.mu.Unlock()
		a.oauth.configMu.Unlock()
		fail(w, 409, "OAUTH_SESSION_CONFLICT", "授权会话与提交账号不一致")
		return
	}
	if p.completed != nil {
		done := *p.completed
		a.oauth.mu.Unlock()
		a.oauth.configMu.Unlock()
		if done.hash != digest || done.path != r.URL.Path {
			fail(w, 409, "OAUTH_SESSION_CONFLICT", "该授权已提交，不能改写为另一份请求")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(done.status)
		w.Write(done.body)
		return
	}
	if !p.ready || p.tokens == nil || p.exchanging || p.submitting {
		a.oauth.mu.Unlock()
		a.oauth.configMu.Unlock()
		fail(w, 409, "OAUTH_SESSION_CONFLICT", "授权未完成或正在提交，请稍后再试")
		return
	}
	a.oauth.mu.Unlock()
	// Authorization/ownership still enforced by the actual handler, but proof
	// validation occurs before mutating the submitted payload.
	if !a.oauthNetworkMatches(p) {
		a.oauth.configMu.Unlock()
		fail(w, 409, "OAUTH_PROXY_CHANGED", "授权出口配置已变化，请重新授权")
		return
	}
	if str(input, "proxy_id") != p.proxyID {
		a.oauth.configMu.Unlock()
		fail(w, 409, "OAUTH_PROXY_CHANGED", "提交代理与授权代理不一致，请重新授权")
		return
	}
	auth, err := decodeAuthInput(input["auth_json"])
	if err != nil {
		a.oauth.configMu.Unlock()
		fail(w, 422, "VALIDATION_FAILED", "请提交本次授权获得的凭证")
		return
	}
	for _, key := range []string{"access_token", "refresh_token", "last_refresh", "expired", "account_uuid", "organization_uuid"} {
		expected, _ := p.tokens.Auth[key].(string)
		actual, _ := auth[key].(string)
		if expected != "" && actual != expected {
			a.oauth.configMu.Unlock()
			fail(w, 409, "OAUTH_SESSION_CONFLICT", "提交凭证与本次授权结果不一致")
			return
		}
	}
	expectedEmail, _ := p.tokens.Auth["email"].(string)
	actualEmail, _ := auth["email"].(string)
	if expectedEmail != "" && !strings.EqualFold(expectedEmail, actualEmail) {
		a.oauth.configMu.Unlock()
		fail(w, 409, "OAUTH_SESSION_CONFLICT", "提交邮箱与授权身份不一致")
		return
	}
	if id == "" && (!strings.EqualFold(str(input, "account"), actualEmail) || actualEmail == "") {
		a.oauth.configMu.Unlock()
		fail(w, 409, "OAUTH_SESSION_CONFLICT", "账号邮箱与授权身份不一致")
		return
	}
	if p.network != nil {
		auth["proxy_url"] = p.network.raw
	}
	input["auth_json"] = auth
	body, _ := json.Marshal(input)
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	a.oauth.mu.Lock()
	p.submitting = true
	a.oauth.items[proof] = p
	a.oauth.mu.Unlock()
	a.oauth.configMu.Unlock()
	buffer := &oauthBufferedWriter{header: w.Header().Clone()}
	// Panic must also release the submitting flag. Expiry/cancel cannot release
	// this lease until the provider write finishes.
	defer func() {
		a.oauth.mu.Lock()
		defer a.oauth.mu.Unlock()
		current, exists := a.oauth.items[proof]
		if !exists {
			return
		}
		current.submitting = false
		completed := buffer.status >= 200 && buffer.status < 300
		// A remote success followed by local failure must never be re-pushed.
		partial := strings.Contains(buffer.body.String(), "平台已更新凭证")
		if completed || partial {
			current.completed = &oauthSubmission{status: buffer.status, body: append([]byte(nil), buffer.body.Bytes()...), hash: digest, path: r.URL.Path}
			current.tokens = nil
			current.ready = false
			if current.lease != nil {
				current.lease.release()
				current.lease = nil
			}
			if current.network != nil {
				current.network.close()
				current.network = nil
			}
		}
		a.oauth.items[proof] = current
		if current.cancelled || !time.Now().Before(current.expires) {
			a.oauth.dropLocked(proof)
		}
	}()
	held := map[string]bool{}
	if id != "" {
		held[id] = true
	}
	next(buffer, r.WithContext(context.WithValue(r.Context(), accountWriteContext{}, held)))
	buffer.flush(w)
}

var _ = errs.CodeAccountInProgress
