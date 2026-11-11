package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sync"
	"time"

	"cc-manager/internal/errs"
	"cc-manager/internal/supplier"
)

// The supplier web (console) channel: a second, read-only way to reach the
// platform with a console account instead of the OpenAPI token. One logged-in
// client per connection lives in memory; nothing about the session is stored.

const (
	webLoginFailureLimit = 3
	webSuspendReason     = "登录连续失败 3 次"
)

var (
	errWebNotConfigured = errs.New(errs.CodeWebNotConfigured)
	errWebSuspended     = errs.New(errs.CodeWebSuspended)
)

// webKinds are the sync_runs kinds the web channel writes.
var webKinds = []string{"web-accounts", "web-bills"}

type webConnection struct {
	ID, BaseURL, Timezone, Username string
	Enabled, Configured             bool
	SuspendedAt, SuspendedReason    string
}

// loadWebConnection reads the connection and, when the channel is
// configured, the decrypted console password (never stored anywhere else).
func (a *App) loadWebConnection(ctx context.Context, id string) (webConnection, string, error) {
	var c webConnection
	var enabled int
	var secret string
	err := a.DB.QueryRowContext(ctx, `SELECT id,base_url,timezone,enabled,web_username,web_password_secret,web_suspended_at,web_suspended_reason FROM platform_connections WHERE id=?`, id).Scan(&c.ID, &c.BaseURL, &c.Timezone, &enabled, &c.Username, &secret, &c.SuspendedAt, &c.SuspendedReason)
	if err != nil {
		return c, "", err
	}
	c.Enabled = enabled == 1
	c.Configured = c.Username != "" && secret != ""
	if !c.Configured {
		return c, "", nil
	}
	password, err := a.openSecret(secret, id+":web")
	if err != nil {
		return c, "", err
	}
	return c, string(password), nil
}

// webClientFor returns the connection's logged-in (or ready-to-log-in)
// console client. The connection row is re-read every time so a suspension
// or a cleared credential takes effect immediately.
func (a *App) webClientFor(ctx context.Context, id string) (*supplier.WebClient, webConnection, error) {
	conn, password, err := a.loadWebConnection(ctx, id)
	if err != nil {
		return nil, conn, err
	}
	if !conn.Configured {
		return nil, conn, errWebNotConfigured
	}
	if conn.SuspendedAt != "" {
		return nil, conn, errWebSuspended
	}
	a.webMu.Lock()
	defer a.webMu.Unlock()
	if a.webClients == nil {
		a.webClients = map[string]*supplier.WebClient{}
	}
	if client, ok := a.webClients[id]; ok {
		return client, conn, nil
	}
	client, err := supplier.NewWebClient(conn.BaseURL, conn.Username, password)
	if err != nil {
		return nil, conn, err
	}
	a.webClients[id] = client
	return client, conn, nil
}

// dropWebClient forgets the cached client (credentials changed, connection
// deleted, or a login test replaced the session).
func (a *App) dropWebClient(id string) {
	a.webMu.Lock()
	delete(a.webClients, id)
	a.webMu.Unlock()
}

// webSyncLock serialises the two web syncs of one connection; TryLock lets a
// tick skip instead of queueing behind a slow run.
func (a *App) webSyncLock(id string) *sync.Mutex {
	a.webMu.Lock()
	defer a.webMu.Unlock()
	if a.webSyncMu == nil {
		a.webSyncMu = map[string]*sync.Mutex{}
	}
	m, ok := a.webSyncMu[id]
	if !ok {
		m = &sync.Mutex{}
		a.webSyncMu[id] = m
	}
	return m
}

// noteWebLogin records a login failure of the web channel and suspends the
// channel on the third consecutive one. Call it before the failed run is
// finalised: it counts the finalised failures already in sync_runs since the
// last web success or credential change, plus the one being reported.
func (a *App) noteWebLogin(ctx context.Context, id string, err error) bool {
	if err == nil || upstreamCode(err) != errs.CodeWebLoginFailed {
		return false
	}
	var edited string
	if e := a.DB.QueryRowContext(ctx, `SELECT COALESCE(updated_at,'') FROM platform_connections WHERE id=?`, id).Scan(&edited); e != nil {
		return false
	}
	var since string
	if e := a.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(started_at),'') FROM sync_runs WHERE connection_id=? AND kind IN ('web-accounts','web-bills') AND state='succeeded'`, id).Scan(&since); e != nil {
		return false
	}
	if since < edited {
		since = edited
	}
	var previous int
	if e := a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_runs WHERE connection_id=? AND kind IN ('web-accounts','web-bills') AND state NOT IN ('succeeded','running') AND error_code=? AND started_at>?`, id, errs.CodeWebLoginFailed, since).Scan(&previous); e != nil {
		return false
	}
	if previous+1 < webLoginFailureLimit {
		return false
	}
	if _, e := a.DB.ExecContext(ctx, `UPDATE platform_connections SET web_suspended_at=?,web_suspended_reason=? WHERE id=? AND web_suspended_at=''`, now(), webSuspendReason, id); e != nil {
		return false
	}
	a.dropWebClient(id)
	a.audit("connection.web_suspended", "platform_connection", id, webSuspendReason)
	return true
}

// clearWebSuspension lifts a suspension after credentials were saved or a
// login test succeeded.
func (a *App) clearWebSuspension(ctx context.Context, id string) error {
	_, err := a.DB.ExecContext(ctx, `UPDATE platform_connections SET web_suspended_at='',web_suspended_reason='' WHERE id=?`, id)
	a.dropWebClient(id)
	return err
}

// upstreamCode classifies any error the supplier clients return.
func upstreamCode(err error) string {
	if err == nil {
		return ""
	}
	var up errs.Upstream
	if errors.As(err, &up) {
		return up.UpstreamCode()
	}
	var local *errs.Error
	if errors.As(err, &local) {
		return local.Code()
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return errs.CodePlatformUnavailable
	}
	return errs.CodePlatformUnavailable
}

// webUpstreamError turns a console failure into an API error without
// leaking the console's text beyond the registry message.
func webUpstreamError(err error) *errs.Error {
	code := upstreamCode(err)
	if _, ok := errs.Lookup(code); !ok {
		code = errs.CodePlatformUnavailable
	}
	out := errs.New(code)
	var up errs.Upstream
	if errors.As(err, &up) && up.UpstreamStatus() != 0 {
		out = out.WithDetail("upstream_status", up.UpstreamStatus())
	}
	return out
}

// webLoginTest answers POST /api/v1/platform-connections/{id}/web-login-test:
// one login with the saved credentials, no sync. Success lifts a suspension.
func (a *App) webLoginTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	conn, password, err := a.loadWebConnection(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "NOT_FOUND", "平台连接不存在")
		return
	}
	if err != nil {
		writeError(w, r, storageError(err, "平台连接读取失败"))
		return
	}
	if !conn.Configured {
		writeError(w, r, errWebNotConfigured)
		return
	}
	client, err := supplier.NewWebClient(conn.BaseURL, conn.Username, password)
	if err != nil {
		writeError(w, r, errs.New(errs.CodeConnectionInvalid).WithCause(err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), supplierRequestTimeout)
	defer cancel()
	session, err := client.Login(ctx)
	if err != nil {
		writeError(w, r, webUpstreamError(err))
		return
	}
	if err = a.clearWebSuspension(r.Context(), id); err != nil {
		writeError(w, r, storageError(err, "平台连接更新失败"))
		return
	}
	a.auditFor(r, "connection.web_login_test", "platform_connection", id, "ok")
	write(w, http.StatusOK, map[string]any{"ok": true, "supplier_name": session.SupplierName, "expires_in": session.ExpiresIn, "tested_at": time.Now().UTC().Format(time.RFC3339)})
}
