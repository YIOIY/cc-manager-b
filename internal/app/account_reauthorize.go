package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"cc-manager/internal/errs"
)

// accountReauthorize pushes Claude credentials to the supplier console for a
// linked account. Callers may send a fresh auth_json, or omit/null it to reuse
// the sealed credential already stored locally.
func (a *App) accountReauthorize(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.requireMutable(w, r, "accounts", id, "账号不存在") {
		return
	}
	if a.keys == nil {
		fail(w, http.StatusServiceUnavailable, "ENCRYPTION_KEY_REQUIRED", "未配置凭证加密密钥")
		return
	}
	var in map[string]any
	if !decodeJSON(w, r, &in, defaultBodyLimit) {
		return
	}

	ctx := r.Context()
	var accountEmail, storedProxyID string
	var version int
	if a.DB.QueryRowContext(ctx, `SELECT account,COALESCE(proxy_id,''),version FROM accounts WHERE id=? AND archived=0`, id).Scan(&accountEmail, &storedProxyID, &version) != nil {
		fail(w, http.StatusNotFound, "NOT_FOUND", "账号不存在")
		return
	}
	expected := expectedVersion(in)
	if expected != 0 && expected != version {
		fail(w, http.StatusConflict, "VERSION_CONFLICT", "账号资料已更新，请刷新后重试")
		return
	}

	raw, authPresent := in["auth_json"]
	reused := !authPresent || raw == nil
	var auth map[string]any
	if !reused {
		decoded, err := decodeAuthInput(raw)
		if err != nil {
			writeError(w, r, errs.New(errs.CodeValidationFailed).WithFields(errs.FieldError{
				Field: "auth_json", Code: errs.FieldInvalidFormat, Message: err.Error(),
			}))
			return
		}
		auth = decoded
	}

	// Explicit proxy_id (including "") replaces the stored binding; omitted keeps it.
	// proxy_id "" clears the pool binding and keeps the credential's proxy_url — not "direct".
	proxyID := storedProxyID
	proxyProvided := false
	if rawProxy, ok := in["proxy_id"]; ok {
		proxyProvided = true
		proxyID, _ = rawProxy.(string)
		proxyID = strings.TrimSpace(proxyID)
	}
	first, err := a.linkedSnapshot(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusConflict, errs.CodeValidationFailed, "账号尚未完成上号，请先完善资料并提交")
		return
	}
	if err != nil {
		writeError(w, r, storageError(err, "平台账号读取失败"))
		return
	}

	// Account writes always lock account before connection. The HTTP guard can
	// lend an OAuth session's existing account lease through the context.
	unlock, err := a.lockAccountWrite(ctx, id)
	if err != nil {
		writeError(w, r, errs.New(errs.CodePlatformUnavailable).WithCause(err))
		return
	}
	defer unlock()
	lock := a.webSyncLock(first.ConnectionID)
	if err = lockWithContext(ctx, lock); err != nil {
		writeError(w, r, errs.New(errs.CodePlatformUnavailable).WithCause(err))
		return
	}
	defer lock.Unlock()

	snapshot, err := a.linkedSnapshot(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && snapshot.ConnectionID != first.ConnectionID) {
		fail(w, http.StatusConflict, errs.CodeVersionConflict, "平台记录已变化，请刷新后重试")
		return
	}
	if err != nil {
		writeError(w, r, storageError(err, "平台账号读取失败"))
		return
	}
	if snapshot.MissingSince != "" || snapshot.SupersededBy != "" {
		fail(w, http.StatusConflict, errs.CodeVersionConflict, "平台记录已变化，请刷新后重试")
		return
	}
	var lockedVersion int
	if a.DB.QueryRowContext(ctx, `SELECT account,COALESCE(proxy_id,''),version FROM accounts WHERE id=? AND archived=0`, id).Scan(&accountEmail, &storedProxyID, &lockedVersion) != nil {
		fail(w, http.StatusNotFound, "NOT_FOUND", "账号不存在")
		return
	}
	if expected != 0 && expected != lockedVersion {
		fail(w, http.StatusConflict, "VERSION_CONFLICT", "账号资料已更新，请刷新后重试")
		return
	}
	// Pin the local write to the version observed under the lock so a request
	// that omitted expected_version cannot overwrite a newer credential.
	writeExpected := expected
	if writeExpected == 0 {
		writeExpected = lockedVersion
	}
	if !proxyProvided {
		proxyID = storedProxyID
	}
	if !a.requireProxyBindable(w, r, proxyID) {
		return
	}

	// Load sealed credentials only after the lock so a concurrent reauthorize
	// that stored a fresher token cannot be overwritten by a stale reuse.
	saved, loadErr := a.loadCredential(id)
	if reused {
		if loadErr != nil || len(saved) == 0 {
			writeError(w, r, errs.New(errs.CodeCredentialsRequired))
			return
		}
		auth = saved
		// Optional device overlay: reuse tokens without a full Claude re-auth when
		// the sealed copy is missing or has invalid claude_device_ids.
		ids, valid := deviceIDsFromRequest(in["claude_device_ids"])
		if !valid {
			writeError(w, r, errs.New(errs.CodeValidationFailed).WithFields(errs.FieldError{
				Field: "auth_json.claude_device_ids", Code: errs.FieldInvalidFormat, Message: "设备标识必须是非空文本",
			}))
			return
		}
		if len(ids) > 0 {
			auth["claude_device_ids"] = ids
		}
	} else if loadErr == nil {
		inheritReauthorizeFields(auth, saved, accountEmail)
	}
	if email, _ := auth["email"].(string); email != "" && !strings.EqualFold(strings.TrimSpace(email), accountEmail) {
		writeError(w, r, errs.New(errs.CodeValidationFailed).WithFields(errs.FieldError{
			Field: "auth_json.email", Code: errs.FieldMismatch, Message: "凭证邮箱必须与账号一致",
		}))
		return
	}
	if proxyID != "" {
		proxyURL, e := a.resolveProxyURL(proxyID, "")
		if e != nil {
			writeError(w, r, errs.New(errs.CodeValidationFailed).WithFields(errs.FieldError{
				Field: "proxy_id", Code: errs.FieldInvalidFormat, Message: e.Error(),
			}))
			return
		}
		if existing, _ := auth["proxy_url"].(string); existing != "" && !sameProxyURL(existing, proxyURL) && (reused || !proxyProvided) {
			writeError(w, r, errs.Adhoc("PROXY_BINDING_CHANGED", http.StatusConflict, "代理配置已变化，请明确更换代理并重新授权"))
			return
		}
		auth["proxy_url"] = proxyURL
	} else if proxyProvided {
		// Cleared pool selection: keep whatever proxy_url the credential carries.
	}
	if e := a.loadDeviceAnchor(ctx, id, auth, saved); e != nil {
		writeError(w, r, storageError(e, "设备标识读取失败"))
		return
	}
	if e := applyCredentialInvariants(auth, saved); e != nil {
		writeError(w, r, e)
		return
	}
	if problems := validateAuthFields(auth); len(problems) > 0 {
		writeError(w, r, errs.New(errs.CodeValidationFailed).WithFields(problems...))
		return
	}
	if url, _ := auth["proxy_url"].(string); strings.TrimSpace(url) == "" {
		writeError(w, r, errs.New(errs.CodeValidationFailed).WithFields(errs.FieldError{
			Field: "auth_json.proxy_url", Code: errs.FieldRequired, Message: "请选择代理或填写 proxy_url",
		}))
		return
	}

	if e := a.persistDeviceAnchor(ctx, id, auth, lockedVersion); e != nil {
		writeError(w, r, storageError(e, "设备标识保存失败"))
		return
	}

	// Always seal the auth that will be pushed (including merged proxy_url) so
	// the local copy cannot drift from what the platform received.
	authBytes, err := json.Marshal(auth)
	if err != nil {
		writeError(w, r, storageError(err, "凭证编码失败"))
		return
	}
	sealed, err := a.keys.Seal(authBytes, []byte(id))
	if err != nil {
		writeError(w, r, secretError("account_credentials", "凭证加密失败", err))
		return
	}

	client, _, err := a.webClientFor(ctx, snapshot.ConnectionID)
	if err != nil {
		var local *errs.Error
		if errors.As(err, &local) {
			writeError(w, r, local)
		} else {
			writeError(w, r, storageError(err, "平台连接读取失败"))
		}
		return
	}
	update := buildProfileUpdate(snapshot, map[string]string{})
	update.AuthJSON = string(authBytes)
	upstreamID, _ := strconv.ParseInt(snapshot.UpstreamID, 10, 64)
	callCtx, cancel := context.WithTimeout(ctx, supplierRequestTimeout)
	err = client.UpdateAccountProfile(callCtx, upstreamID, update)
	cancel()
	if err != nil {
		a.noteWebLogin(ctx, snapshot.ConnectionID, err)
		writeError(w, r, webUpstreamError(err))
		return
	}

	actor := sessionUser(r).Username
	if actor == "" {
		actor = "anonymous"
	}
	detail := snapshot.UpstreamID + " replaced"
	if reused {
		detail = snapshot.UpstreamID + " saved"
	}
	// Platform already has the new credential: audit and local persist must
	// finish even if the client cancelled, matching completeProfileUpdate.
	a.oauthStatCounters.reauthorization.Add(1)
	if err = a.completeReauthorize(ctx, id, actor, detail, sealed, proxyID, writeExpected); err != nil {
		if errors.Is(err, errReauthorizeLocalConflict) {
			fail(w, http.StatusConflict, errs.CodeVersionConflict, "平台已更新凭证，但本地版本冲突，请刷新后重试")
			return
		}
		writeError(w, r, storageError(err, "平台已更新凭证，但本地保存失败，请刷新后重试"))
		return
	}
	source := "replaced"
	if reused {
		source = "saved"
	}
	write(w, http.StatusOK, map[string]any{
		"account_id":      id,
		"platform_pushed": true,
		"upstream_id":     snapshot.UpstreamID,
		"credential":      map[string]any{"source": source},
	})
}

var errReauthorizeLocalConflict = errors.New("reauthorize local version conflict")

// Inherit only non-token fields for the same account. Organization metadata is
// reused only when the new authorization explicitly identifies the same org.
func inheritReauthorizeFields(auth, saved map[string]any, accountEmail string) {
	text := func(m map[string]any, key string) string {
		value, _ := m[key].(string)
		return strings.TrimSpace(value)
	}
	missing := func(key string) bool {
		value := auth[key]
		if value == nil {
			return true
		}
		s, ok := value.(string)
		return ok && strings.TrimSpace(s) == ""
	}
	if missing("email") {
		auth["email"] = accountEmail
	}
	if !strings.EqualFold(text(auth, "email"), strings.TrimSpace(accountEmail)) ||
		!strings.EqualFold(text(saved, "email"), strings.TrimSpace(accountEmail)) {
		return
	}
	accountUUID := text(auth, "account_uuid")
	savedAccountUUID := text(saved, "account_uuid")
	if accountUUID != "" && savedAccountUUID != "" && accountUUID != savedAccountUUID {
		return
	}
	for _, key := range []string{"account_uuid", "proxy_url"} {
		if missing(key) && text(saved, key) != "" {
			auth[key] = saved[key]
		}
	}
	ids, valid := deviceIDsFromRequest(auth["claude_device_ids"])
	if valid && len(ids) == 0 {
		if oldIDs, ok := deviceIDsFromRequest(saved["claude_device_ids"]); ok && len(oldIDs) > 0 {
			auth["claude_device_ids"] = oldIDs
		}
	}
	orgUUID := text(auth, "organization_uuid")
	if orgUUID != "" && orgUUID == text(saved, "organization_uuid") && missing("organization_name") && text(saved, "organization_name") != "" {
		auth["organization_name"] = saved["organization_name"]
	}
}

// deviceIDsFromRequest accepts ["id"] or a comma/newline-separated string.
func deviceIDsFromRequest(raw any) ([]any, bool) {
	switch v := raw.(type) {
	case nil:
		return nil, true
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return nil, false
			}
			out = append(out, strings.TrimSpace(s))
		}
		return out, true
	case []string:
		out := make([]any, 0, len(v))
		for _, item := range v {
			if s := strings.TrimSpace(item); s != "" {
				out = append(out, s)
			}
		}
		return out, true
	case string:
		parts := strings.FieldsFunc(v, func(r rune) bool {
			return r == ',' || r == '，' || r == '\n' || r == '\r'
		})
		out := make([]any, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		return out, true
	default:
		return nil, false
	}
}

// completeReauthorize audits and writes the sealed credential locally after
// the platform accepted the change. Detached from request cancellation.
func (a *App) completeReauthorize(ctx context.Context, accountID, actor, upstreamID, sealed, proxyID string, expected int) error {
	a.auditAs(actor, "account.reauthorize", "account", accountID, upstreamID)
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*supplierRequestTimeout)
	defer cancel()
	stamp := now()
	query := `UPDATE accounts SET credentials_json=?,proxy_id=?,version=version+1,updated_at=? WHERE id=?`
	args := []any{sealed, proxyID, stamp, accountID}
	if expected != 0 {
		query += ` AND version=?`
		args = append(args, expected)
	}
	result, err := a.DB.ExecContext(bg, query, args...)
	if err != nil {
		slog.Error("reauthorize local save failed after platform update", "account", accountID, "error", err)
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		slog.Error("reauthorize local version conflict after platform update", "account", accountID)
		return errReauthorizeLocalConflict
	}
	return nil
}
