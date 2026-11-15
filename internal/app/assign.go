package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// accountAssign lets an admin set (or clear) the owning user of an account.
// Bound proxies are handled automatically: an exclusive proxy moves with the
// account; a shared proxy is unbound from this account only.
func (a *App) accountAssign(w http.ResponseWriter, r *http.Request) {
	if !ownershipFrom(r).Admin {
		fail(w, http.StatusForbidden, "PERMISSION_DENIED", "仅管理员可将资源分配给用户")
		return
	}
	id := r.PathValue("id")
	currentOwner, ok := a.requireVisible(w, r, "accounts", id, "账号不存在")
	if !ok {
		return
	}
	var in struct {
		UserID string `json:"user_id"`
	}
	if !decodeJSON(w, r, &in, 4<<10) {
		return
	}
	in.UserID = strings.TrimSpace(in.UserID)
	label, ok := a.resolveAssignee(w, r, in.UserID)
	if !ok {
		return
	}
	if in.UserID == currentOwner {
		write(w, http.StatusOK, map[string]any{
			"id":                 id,
			"created_by_user_id": in.UserID,
			"assigned":           true,
			"unchanged":          true,
			"proxy_action":       "none",
		})
		return
	}
	if !a.assignAccountIdle(w, r, id) {
		return
	}

	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, storageError(err, "分配失败"))
		return
	}
	defer tx.Rollback()

	stamp := now()
	proxyAction, proxyID, err := a.applyAccountProxyOnAssign(r.Context(), tx, id, in.UserID, stamp)
	if err != nil {
		writeError(w, r, storageError(err, "分配失败"))
		return
	}
	if _, err = tx.ExecContext(r.Context(),
		`UPDATE accounts SET created_by_user_id=?,updated_at=? WHERE id=?`,
		in.UserID, stamp, id); err != nil {
		writeError(w, r, storageError(err, "分配失败"))
		return
	}
	if err = tx.Commit(); err != nil {
		writeError(w, r, storageError(err, "分配失败"))
		return
	}

	detail := "unassigned"
	if in.UserID != "" {
		detail = "user=" + in.UserID
		if label != "" {
			detail += " (" + label + ")"
		}
	}
	detail += " proxy=" + proxyAction
	if proxyID != "" {
		detail += ":" + proxyID
	}
	a.auditFor(r, "account.assign", "account", id, detail)
	write(w, http.StatusOK, map[string]any{
		"id":                 id,
		"created_by_user_id": in.UserID,
		"assigned":           true,
		"proxy_action":       proxyAction,
		"proxy_id":           proxyID,
		"message":            accountAssignMessage(proxyAction),
	})
}

// proxyAssign lets an admin set (or clear) the owning user of a proxy.
func (a *App) proxyAssign(w http.ResponseWriter, r *http.Request) {
	if !ownershipFrom(r).Admin {
		fail(w, http.StatusForbidden, "PERMISSION_DENIED", "仅管理员可将资源分配给用户")
		return
	}
	id := r.PathValue("id")
	currentOwner, ok := a.requireVisible(w, r, "proxies", id, "代理不存在")
	if !ok {
		return
	}
	var in struct {
		UserID string `json:"user_id"`
	}
	if !decodeJSON(w, r, &in, 4<<10) {
		return
	}
	in.UserID = strings.TrimSpace(in.UserID)
	label, ok := a.resolveAssignee(w, r, in.UserID)
	if !ok {
		return
	}
	if in.UserID == currentOwner {
		write(w, http.StatusOK, map[string]any{
			"id":                 id,
			"created_by_user_id": in.UserID,
			"assigned":           true,
			"unchanged":          true,
		})
		return
	}
	if !a.assignProxyUnboundOrOwned(w, r, id, in.UserID) {
		return
	}
	// Ownership is not part of the onboard optimistic lock; bumping version
	// would fail queued submit jobs that captured the prior version.
	res, err := a.DB.ExecContext(r.Context(),
		`UPDATE proxies SET created_by_user_id=?,updated_at=? WHERE id=?`,
		in.UserID, now(), id)
	if err != nil {
		writeError(w, r, storageError(err, "分配失败"))
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fail(w, http.StatusNotFound, "NOT_FOUND", "代理不存在")
		return
	}
	detail := "unassigned"
	if in.UserID != "" {
		detail = "user=" + in.UserID
		if label != "" {
			detail += " (" + label + ")"
		}
	}
	a.auditFor(r, "proxy.assign", "proxy", id, detail)
	write(w, http.StatusOK, map[string]any{
		"id":                 id,
		"created_by_user_id": in.UserID,
		"assigned":           true,
	})
}

func accountAssignMessage(proxyAction string) string {
	switch proxyAction {
	case "transferred":
		return "已分配，并一并转移独占代理"
	case "unbound":
		return "已分配；共享代理已从该账号解绑，请新用户自行绑定"
	case "kept":
		return "已分配，代理归属已一致"
	default:
		return "已分配"
	}
}

// applyAccountProxyOnAssign adjusts the bound proxy inside tx.
// Returns proxy_action and the proxy id involved (empty when none).
// Archived accounts still count as bindings: otherwise restoring an archived
// co-user of the proxy would recreate a cross-owner bind after a mistaken
// "exclusive" transfer.
func (a *App) applyAccountProxyOnAssign(ctx context.Context, tx *sql.Tx, accountID, assignee, stamp string) (action, proxyID string, err error) {
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(proxy_id,'') FROM accounts WHERE id=?`, accountID).Scan(&proxyID)
	if err != nil {
		return "", "", err
	}
	if proxyID == "" {
		return "none", "", nil
	}
	var proxyOwner string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(created_by_user_id,'') FROM proxies WHERE id=?`, proxyID).Scan(&proxyOwner)
	if errors.Is(err, sql.ErrNoRows) {
		if err = a.clearAccountProxyBinding(ctx, tx, accountID, stamp); err != nil {
			return "", proxyID, err
		}
		return "unbound", proxyID, nil
	}
	if err != nil {
		return "", proxyID, err
	}
	if proxyOwner == assignee {
		return "kept", proxyID, nil
	}
	var others int
	err = tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM accounts WHERE proxy_id=? AND id<>?`,
		proxyID, accountID).Scan(&others)
	if err != nil {
		return "", proxyID, err
	}
	if others == 0 {
		if _, err = tx.ExecContext(ctx,
			`UPDATE proxies SET created_by_user_id=?,updated_at=? WHERE id=?`,
			assignee, stamp, proxyID); err != nil {
			return "", proxyID, err
		}
		return "transferred", proxyID, nil
	}
	if err = a.clearAccountProxyBinding(ctx, tx, accountID, stamp); err != nil {
		return "", proxyID, err
	}
	return "unbound", proxyID, nil
}

// clearAccountProxyBinding drops proxy_id and removes proxy_url from sealed
// credentials so a later submit cannot keep using the previous owner's proxy.
func (a *App) clearAccountProxyBinding(ctx context.Context, tx *sql.Tx, accountID, stamp string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET proxy_id='',updated_at=? WHERE id=?`, stamp, accountID); err != nil {
		return err
	}
	var enc string
	if err := tx.QueryRowContext(ctx, `SELECT credentials_json FROM accounts WHERE id=?`, accountID).Scan(&enc); err != nil {
		return err
	}
	if enc == "" || enc == "{}" {
		return nil
	}
	if a.keys == nil {
		return errors.New("encryption key unavailable")
	}
	plain, err := a.keys.Open(enc, []byte(accountID))
	if err != nil {
		return err
	}
	var auth map[string]any
	if err = json.Unmarshal(plain, &auth); err != nil {
		return err
	}
	if _, had := auth["proxy_url"]; !had {
		return nil
	}
	delete(auth, "proxy_url")
	raw, err := json.Marshal(auth)
	if err != nil {
		return err
	}
	sealed, err := a.keys.Seal(raw, []byte(accountID))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET credentials_json=?,updated_at=? WHERE id=?`, sealed, stamp, accountID)
	return err
}

// assignAccountIdle rejects assign while a submit job is still queued/running,
// so ownership does not change under an in-flight onboard.
func (a *App) assignAccountIdle(w http.ResponseWriter, r *http.Request, accountID string) bool {
	var n int
	err := a.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM jobs WHERE account_id=? AND kind='account.submit' AND state IN ('queued','running')`,
		accountID).Scan(&n)
	if err != nil {
		writeError(w, r, storageError(err, "分配失败"))
		return false
	}
	if n > 0 {
		fail(w, http.StatusConflict, "ACCOUNT_IN_PROGRESS", "账号正在上号中，请等待结果后再分配")
		return false
	}
	return true
}

// assignProxyUnboundOrOwned rejects proxy reassignment when non-archived
// accounts still bind it and are not owned by the new assignee. That keeps
// proxy ownership aligned with the accounts that use it.
func (a *App) assignProxyUnboundOrOwned(w http.ResponseWriter, r *http.Request, proxyID, assignee string) bool {
	var n int
	var err error
	if assignee == "" {
		err = a.DB.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM accounts WHERE proxy_id=?`, proxyID).Scan(&n)
	} else {
		err = a.DB.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM accounts WHERE proxy_id=? AND created_by_user_id<>?`,
			proxyID, assignee).Scan(&n)
	}
	if err != nil {
		writeError(w, r, storageError(err, "分配失败"))
		return false
	}
	if n > 0 {
		fail(w, http.StatusConflict, "PROXY_IN_USE", "仍有其他用户的账号绑定该代理，请先解绑后再分配")
		return false
	}
	return true
}

// resolveAssignee validates the target user. Empty userID clears ownership
// (admin-only orphan). Non-empty must refer to an enabled user.
func (a *App) resolveAssignee(w http.ResponseWriter, r *http.Request, userID string) (label string, ok bool) {
	if userID == "" {
		return "", true
	}
	var username, nickname string
	var enabled int
	err := a.DB.QueryRowContext(r.Context(),
		`SELECT username,COALESCE(nickname,''),enabled FROM users WHERE id=?`, userID,
	).Scan(&username, &nickname, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusUnprocessableEntity, "USER_INVALID", "目标用户不存在")
		return "", false
	}
	if err != nil {
		writeError(w, r, storageError(err, "读取用户失败"))
		return "", false
	}
	if enabled != 1 {
		fail(w, http.StatusUnprocessableEntity, "USER_INVALID", "目标用户已停用")
		return "", false
	}
	if nickname != "" {
		return nickname, true
	}
	return username, true
}
