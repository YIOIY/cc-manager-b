package app

import (
	"cc-manager/internal/errs"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
)

type userRecord struct {
	ID         string
	Username   string
	Nickname   string
	Enabled    bool
	CanOnboard bool
	Version    int
	CreatedAt  string
	UpdatedAt  string
	LastLogin  string
}

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9._@-]{2,64}$`)

const nicknameMaxRunes = 32

func normalizeNickname(raw string) (string, string) {
	nickname := strings.TrimSpace(raw)
	if nickname == "" {
		return "", ""
	}
	if len([]rune(nickname)) > nicknameMaxRunes {
		return "", "昵称最多 32 个字"
	}
	return nickname, ""
}

func (a *App) userPublic(u userRecord) map[string]any {
	ac, _ := a.userAccess(u.ID)
	return map[string]any{"id": u.ID, "username": u.Username, "nickname": u.Nickname, "enabled": u.Enabled, "can_onboard": u.CanOnboard, "version": u.Version, "created_at": u.CreatedAt, "updated_at": u.UpdatedAt, "last_login_at": u.LastLogin, "roles": ac.Roles, "permissions": ac.list(), "is_admin": ac.Super}
}

func sessionUserPayload(u userRecord, ac access) map[string]any {
	return map[string]any{"id": u.ID, "username": u.Username, "nickname": u.Nickname, "can_onboard": u.CanOnboard, "roles": ac.Roles, "permissions": ac.list(), "is_admin": ac.Super}
}

// ensureBootstrapAdmin creates the first administrator from the environment
// when the users table is empty. After that the environment password is no
// longer consulted; use `cc-manager reset-password` to recover access.
func (a *App) ensureBootstrapAdmin() error {
	var count int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hash := a.cfg.AdminPasswordHash
	if hash == "" {
		var err error
		if hash, err = HashPassword(a.cfg.AdminPassword); err != nil {
			return err
		}
	}
	id, stamp := newID(), now()
	if _, err := a.DB.Exec(`INSERT INTO users(id,username,password_hash,enabled,version,created_at,updated_at) VALUES(?,?,?,1,1,?,?)`, id, a.cfg.AdminUser, hash, stamp, stamp); err != nil {
		return err
	}
	if _, err := a.DB.Exec(`INSERT INTO user_roles(user_id,role_id) VALUES(?,?)`, id, roleAdminID); err != nil {
		return err
	}
	slog.Info("bootstrap administrator created", "username", a.cfg.AdminUser)
	return nil
}

func scanUser(row interface{ Scan(...any) error }) (userRecord, string, error) {
	var u userRecord
	var hash string
	var enabled, canOnboard int
	err := row.Scan(&u.ID, &u.Username, &u.Nickname, &hash, &enabled, &canOnboard, &u.Version, &u.CreatedAt, &u.UpdatedAt, &u.LastLogin)
	u.Enabled = enabled == 1
	u.CanOnboard = canOnboard == 1
	return u, hash, err
}

const userColumns = `id,username,COALESCE(nickname,''),password_hash,enabled,can_onboard,version,created_at,updated_at,COALESCE(last_login_at,'')`

func (a *App) userByName(username string) (userRecord, string, error) {
	return scanUser(a.DB.QueryRow(`SELECT `+userColumns+` FROM users WHERE username=?`, username))
}

func (a *App) userByID(id string) (userRecord, error) {
	u, _, err := scanUser(a.DB.QueryRow(`SELECT `+userColumns+` FROM users WHERE id=?`, id))
	return u, err
}

// ResetPassword sets a new password for username, re-enables the account and
// revokes its sessions. Used by the CLI for lock-out recovery.
func (a *App) ResetPassword(username, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	res, err := a.DB.Exec(`UPDATE users SET password_hash=?,enabled=1,version=version+1,updated_at=? WHERE username=?`, hash, now(), username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("user not found: " + username)
	}
	if u, _, err := a.userByName(username); err == nil {
		a.sessions.revokeUser(u.ID)
	}
	a.auditAs("cli", "user.password_reset", "user", username, "")
	return nil
}

// replaceUserRoles rewrites the user's role set inside tx.
func replaceUserRoles(tx *sql.Tx, userID string, roleIDs []string) error {
	if _, err := tx.Exec(`DELETE FROM user_roles WHERE user_id=?`, userID); err != nil {
		return err
	}
	for _, id := range roleIDs {
		if _, err := tx.Exec(`INSERT INTO user_roles(user_id,role_id) VALUES(?,?)`, userID, id); err != nil {
			return err
		}
	}
	return nil
}

// validateRoleIDs checks every id exists and that a non-superuser is not
// handing out the administrator role.
func (a *App) validateRoleIDs(ids []string, actor access) ([]string, string) {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		if _, err := a.roleByID(id); err != nil {
			return nil, "角色不存在：" + id
		}
		if id == roleAdminID && !actor.Super {
			return nil, "只有管理员可以授予管理员角色"
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, "至少选择一个角色"
	}
	return out, ""
}

// ---- handlers ----

func (a *App) listUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.QueryContext(r.Context(), `SELECT `+userColumns+` FROM users ORDER BY created_at,rowid`)
	if err != nil {
		writeError(w, r, storageError(err, "读取用户失败"))
		return
	}
	users := []userRecord{}
	for rows.Next() {
		u, _, err := scanUser(rows)
		if err != nil {
			rows.Close()
			writeError(w, r, storageError(err, "读取用户失败"))
			return
		}
		users = append(users, u)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		writeError(w, r, storageError(err, "读取用户失败"))
		return
	}
	out := []any{}
	for _, u := range users {
		out = append(out, a.userPublic(u))
	}
	write(w, http.StatusOK, out)
}

func (a *App) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string   `json:"username"`
		Nickname string   `json:"nickname"`
		Password string   `json:"password"`
		RoleIDs  []string `json:"role_ids"`
	}
	if !decodeJSON(w, r, &in, 16<<10) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	var problems errs.Fields
	if !usernamePattern.MatchString(in.Username) {
		problems.Invalid("username", "账号需为 2–64 位字母、数字或 . _ @ -")
	}
	nickname, nickProblem := normalizeNickname(in.Nickname)
	if nickProblem != "" {
		problems.Add("nickname", errs.FieldOutOfRange, nickProblem)
	}
	roleIDs, problem := a.validateRoleIDs(in.RoleIDs, sessionUser(r).Access)
	if problem != "" {
		problems.Add("role_ids", errs.FieldUnsupportedValue, problem)
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		problems.Add("password", errs.FieldOutOfRange, "密码至少 12 位")
	}
	if e := problems.Err(); e != nil {
		writeError(w, r, e)
		return
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, storageError(err, "用户保存失败"))
		return
	}
	defer tx.Rollback()
	id, stamp := newID(), now()
	if _, err = tx.Exec(`INSERT INTO users(id,username,nickname,password_hash,enabled,version,created_at,updated_at) VALUES(?,?,?,?,1,1,?,?)`, id, in.Username, nickname, hash, stamp, stamp); err != nil {
		dbError(w, r, err, "USER_EXISTS", "账号已存在", "用户保存失败")
		return
	}
	if err = replaceUserRoles(tx, id, roleIDs); err != nil {
		writeError(w, r, storageError(err, "用户角色保存失败"))
		return
	}
	if err = tx.Commit(); err != nil {
		writeError(w, r, storageError(err, "用户保存失败"))
		return
	}
	a.auditFor(r, "user.create", "user", id, in.Username+" roles="+strings.Join(roleIDs, ","))
	u, _ := a.userByID(id)
	write(w, http.StatusCreated, a.userPublic(u))
}

func (a *App) updateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		RoleIDs         []string `json:"role_ids"`
		Enabled         *bool    `json:"enabled"`
		CanOnboard      *bool    `json:"can_onboard"`
		Password        string   `json:"password"`
		Nickname        *string  `json:"nickname"`
		ExpectedVersion int      `json:"expected_version"`
	}
	if !decodeJSON(w, r, &in, 16<<10) {
		return
	}
	actor := sessionUser(r)
	target, err := a.userByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "NOT_FOUND", "用户不存在")
			return
		}
		writeError(w, r, storageError(err, "读取用户失败"))
		return
	}
	if in.ExpectedVersion != 0 && in.ExpectedVersion != target.Version {
		fail(w, http.StatusConflict, "VERSION_CONFLICT", "用户资料已更新，请刷新后重试")
		return
	}
	if in.Password != "" && !actor.Access.allows(PermUsersResetPassword) {
		fail(w, http.StatusForbidden, "PERMISSION_DENIED", "没有重置他人密码的权限")
		return
	}
	currentRoles, _ := a.userRoleIDs(id)
	targetIsAdmin := hasRole(currentRoles, roleAdminID)
	var roleIDs []string
	if in.RoleIDs != nil {
		var problem string
		if roleIDs, problem = a.validateRoleIDs(in.RoleIDs, actor.Access); problem != "" {
			writeError(w, r, fieldErr("role_ids", errs.FieldUnsupportedValue, problem))
			return
		}
		if target.ID == actor.ID && actor.Access.Super && !hasRole(roleIDs, roleAdminID) {
			fail(w, http.StatusUnprocessableEntity, "SELF_DEMOTION", "不能移除自己的管理员角色")
			return
		}
		if targetIsAdmin && !actor.Access.Super {
			fail(w, http.StatusForbidden, "PERMISSION_DENIED", "只有管理员可以修改管理员用户")
			return
		}
	}
	if in.Enabled != nil && !*in.Enabled {
		if target.ID == actor.ID {
			fail(w, http.StatusUnprocessableEntity, "SELF_DISABLE", "不能停用自己")
			return
		}
		if targetIsAdmin && !actor.Access.Super {
			fail(w, http.StatusForbidden, "PERMISSION_DENIED", "只有管理员可以停用管理员用户")
			return
		}
	}
	losesAdmin := targetIsAdmin && target.Enabled && ((in.RoleIDs != nil && !hasRole(roleIDs, roleAdminID)) || (in.Enabled != nil && !*in.Enabled))
	if losesAdmin && a.enabledAdminCount() <= 1 {
		fail(w, http.StatusUnprocessableEntity, "LAST_ADMIN", "至少保留一个启用的管理员")
		return
	}
	sets, args, detail := []string{}, []any{}, []string{}
	if in.Enabled != nil {
		sets, args = append(sets, "enabled=?"), append(args, boolInt(*in.Enabled))
		detail = append(detail, map[bool]string{true: "enabled", false: "disabled"}[*in.Enabled])
	}
	if in.CanOnboard != nil {
		sets, args = append(sets, "can_onboard=?"), append(args, boolInt(*in.CanOnboard))
		detail = append(detail, map[bool]string{true: "can_onboard", false: "can_onboard_off"}[*in.CanOnboard])
	}
	if in.Password != "" {
		hash, err := HashPassword(in.Password)
		if err != nil {
			writeError(w, r, fieldErr("password", errs.FieldOutOfRange, "密码至少 12 位"))
			return
		}
		sets, args = append(sets, "password_hash=?"), append(args, hash)
		detail = append(detail, "password")
	}
	if in.Nickname != nil {
		nickname, nickProblem := normalizeNickname(*in.Nickname)
		if nickProblem != "" {
			writeError(w, r, fieldErr("nickname", errs.FieldOutOfRange, nickProblem))
			return
		}
		sets, args = append(sets, "nickname=?"), append(args, nickname)
		detail = append(detail, "nickname")
	}
	if len(sets) == 0 && in.RoleIDs == nil {
		fail(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "没有可更新字段")
		return
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, storageError(err, "用户更新失败"))
		return
	}
	defer tx.Rollback()
	sets = append(sets, "version=version+1", "updated_at=?")
	args = append(args, now(), id, target.Version)
	if _, err = tx.Exec(`UPDATE users SET `+strings.Join(sets, ",")+` WHERE id=? AND version=?`, args...); err != nil {
		writeError(w, r, storageError(err, "用户更新失败"))
		return
	}
	if in.RoleIDs != nil {
		if err = replaceUserRoles(tx, id, roleIDs); err != nil {
			writeError(w, r, storageError(err, "用户角色保存失败"))
			return
		}
		detail = append(detail, "roles="+strings.Join(roleIDs, ","))
	}
	if err = tx.Commit(); err != nil {
		writeError(w, r, storageError(err, "用户更新失败"))
		return
	}
	// Disabling or changing a password ends that user's sessions; role
	// changes apply on the next request because access is resolved per call.
	if (in.Enabled != nil && !*in.Enabled) || in.Password != "" {
		a.sessions.revokeUser(target.ID)
	}
	a.auditFor(r, "user.update", "user", id, target.Username+" "+strings.Join(detail, ","))
	u, _ := a.userByID(id)
	write(w, http.StatusOK, a.userPublic(u))
}

func (a *App) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	actor := sessionUser(r)
	if id == actor.ID {
		fail(w, http.StatusUnprocessableEntity, "SELF_DELETE", "不能删除自己")
		return
	}
	target, err := a.userByID(id)
	if err != nil {
		fail(w, http.StatusNotFound, "NOT_FOUND", "用户不存在")
		return
	}
	roles, _ := a.userRoleIDs(id)
	if hasRole(roles, roleAdminID) {
		if !actor.Access.Super {
			fail(w, http.StatusForbidden, "PERMISSION_DENIED", "只有管理员可以删除管理员用户")
			return
		}
		if target.Enabled && a.enabledAdminCount() <= 1 {
			fail(w, http.StatusUnprocessableEntity, "LAST_ADMIN", "至少保留一个启用的管理员")
			return
		}
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, storageError(err, "用户删除失败"))
		return
	}
	defer tx.Rollback()
	// Drop ownership so leftover accounts/proxies become admin-editable orphans
	// instead of permanently locked to a deleted user id.
	if _, err = tx.Exec(`UPDATE accounts SET created_by_user_id='' WHERE created_by_user_id=?`, id); err != nil {
		writeError(w, r, storageError(err, "用户删除失败"))
		return
	}
	if _, err = tx.Exec(`UPDATE proxies SET created_by_user_id='' WHERE created_by_user_id=?`, id); err != nil {
		writeError(w, r, storageError(err, "用户删除失败"))
		return
	}
	if _, err = tx.Exec(`DELETE FROM user_roles WHERE user_id=?`, id); err != nil {
		writeError(w, r, storageError(err, "用户删除失败"))
		return
	}
	if _, err = tx.Exec(`DELETE FROM users WHERE id=?`, id); err != nil {
		writeError(w, r, storageError(err, "用户删除失败"))
		return
	}
	if err = tx.Commit(); err != nil {
		writeError(w, r, storageError(err, "用户删除失败"))
		return
	}
	a.sessions.revokeUser(id)
	a.auditFor(r, "user.delete", "user", id, target.Username)
	write(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

// changePassword lets any signed-in user rotate their own password after
// proving the current one. Other sessions of the user are revoked.
func (a *App) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &in, 16<<10) {
		return
	}
	me := sessionUser(r)
	_, hash, err := a.userByName(me.Username)
	if err != nil {
		writeError(w, r, storageError(err, "读取用户失败"))
		return
	}
	if !verifyPassword(in.Current, "", hash) {
		fail(w, http.StatusForbidden, "PASSWORD_INVALID", "当前密码不正确")
		return
	}
	newHash, err := HashPassword(in.New)
	if err != nil {
		writeError(w, r, fieldErr("password", errs.FieldOutOfRange, "新密码至少 12 位"))
		return
	}
	if _, err = a.DB.ExecContext(r.Context(), `UPDATE users SET password_hash=?,version=version+1,updated_at=? WHERE id=?`, newHash, now(), me.ID); err != nil {
		writeError(w, r, storageError(err, "密码更新失败"))
		return
	}
	current, _, _ := a.currentSession(r)
	a.sessions.revokeUserExcept(me.ID, current)
	a.auditFor(r, "user.password_change", "user", me.ID, me.Username)
	write(w, http.StatusOK, map[string]any{"changed": true})
}
