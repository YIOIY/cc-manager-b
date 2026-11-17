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

// accountReauthorize replaces Claude credentials on a linked account and
// pushes them to the supplier console so login_failed / re_login states can
// recover without creating a duplicate delivery.
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
	auth, err := decodeAuthInput(in["auth_json"])
	if err != nil {
		writeError(w, r, errs.New(errs.CodeValidationFailed).WithFields(errs.FieldError{Field: "auth_json", Code: errs.FieldInvalidFormat, Message: err.Error()}))
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
	if email, _ := auth["email"].(string); email != "" && !strings.EqualFold(strings.TrimSpace(email), accountEmail) {
		writeError(w, r, errs.New(errs.CodeValidationFailed).WithFields(errs.FieldError{
			Field: "auth_json.email", Code: errs.FieldMismatch, Message: "凭证邮箱必须与账号一致",
		}))
		return
	}

	// Explicit proxy_id (including "") replaces the stored binding; omitted keeps it.
	proxyID := storedProxyID
	proxyProvided := false
	if raw, ok := in["proxy_id"]; ok {
		proxyProvided = true
		proxyID, _ = raw.(string)
		proxyID = strings.TrimSpace(proxyID)
	}
	if !a.requireProxyBindable(w, r, proxyID) {
		return
	}
	// Fill proxy_url from the pool before credential validation — the browser
	// leaves auth_json.proxy_url empty when a pool entry is selected.
	if proxyID != "" {
		proxyURL, e := a.resolveProxyURL(proxyID, "")
		if e != nil {
			writeError(w, r, errs.New(errs.CodeValidationFailed).WithFields(errs.FieldError{
				Field: "proxy_id", Code: errs.FieldInvalidFormat, Message: e.Error(),
			}))
			return
		}
		auth["proxy_url"] = proxyURL
	} else if proxyProvided {
		// Cleared pool selection: keep whatever proxy_url the credential carries.
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

	first, err := a.linkedSnapshot(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusConflict, errs.CodeValidationFailed, "账号尚未完成上号，请先完善资料并提交")
		return
	}
	if err != nil {
		writeError(w, r, storageError(err, "平台账号读取失败"))
		return
	}

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
	// Re-check account version under the same lock so a concurrent reauthorize
	// cannot push to the platform and then fail the local write.
	var lockedVersion int
	if a.DB.QueryRowContext(ctx, `SELECT version FROM accounts WHERE id=? AND archived=0`, id).Scan(&lockedVersion) != nil {
		fail(w, http.StatusNotFound, "NOT_FOUND", "账号不存在")
		return
	}
	if expected != 0 && expected != lockedVersion {
		fail(w, http.StatusConflict, "VERSION_CONFLICT", "账号资料已更新，请刷新后重试")
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
	// Platform already has the new credential: audit and local persist must
	// finish even if the client cancelled, matching completeProfileUpdate.
	if err = a.completeReauthorize(ctx, id, actor, snapshot.UpstreamID, sealed, proxyID, expected); err != nil {
		if errors.Is(err, errReauthorizeLocalConflict) {
			fail(w, http.StatusConflict, errs.CodeVersionConflict, "平台已更新凭证，但本地版本冲突，请刷新后重试")
			return
		}
		writeError(w, r, storageError(err, "平台已更新凭证，但本地保存失败，请刷新后重试"))
		return
	}
	write(w, http.StatusOK, map[string]any{
		"account_id":      id,
		"platform_pushed": true,
		"upstream_id":     snapshot.UpstreamID,
	})
}

var errReauthorizeLocalConflict = errors.New("reauthorize local version conflict")

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
