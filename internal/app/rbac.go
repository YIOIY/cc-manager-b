package app

import (
	"cc-manager/internal/errs"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
)

type roleRecord struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Builtin     bool     `json:"builtin"`
	Permissions []string `json:"permissions"`
	Version     int      `json:"version"`
	UserCount   int      `json:"user_count"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// access is the resolved authorization of a signed-in user.
type access struct {
	Super       bool
	Roles       []roleRef
	Permissions map[Permission]bool
}

type roleRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Builtin bool   `json:"builtin"`
}

func (ac access) allows(p Permission) bool { return ac.Super || ac.Permissions[p] }

func (ac access) list() []string {
	if ac.Super {
		return allPermissionKeys()
	}
	out := make([]string, 0, len(ac.Permissions))
	for p := range ac.Permissions {
		out = append(out, string(p))
	}
	sort.Strings(out)
	return out
}

const roleColumns = `r.id,r.name,r.description,r.builtin,r.permissions_json,r.version,r.created_at,r.updated_at,(SELECT COUNT(*) FROM user_roles ur WHERE ur.role_id=r.id)`

func scanRole(row interface{ Scan(...any) error }) (roleRecord, error) {
	var r roleRecord
	var builtin int
	var perms string
	if err := row.Scan(&r.ID, &r.Name, &r.Description, &builtin, &perms, &r.Version, &r.CreatedAt, &r.UpdatedAt, &r.UserCount); err != nil {
		return r, err
	}
	r.Builtin = builtin == 1
	r.Permissions = []string{}
	_ = json.Unmarshal([]byte(perms), &r.Permissions)
	if r.ID == roleAdminID {
		r.Permissions = allPermissionKeys()
	}
	return r, nil
}

func (a *App) roleByID(id string) (roleRecord, error) {
	return scanRole(a.DB.QueryRow(`SELECT `+roleColumns+` FROM roles r WHERE r.id=?`, id))
}

// userAccess resolves the union of a user's role permissions. It runs on
// every request, so role edits and reassignments apply immediately.
func (a *App) userAccess(userID string) (access, error) {
	rows, err := a.DB.Query(`SELECT r.id,r.name,r.builtin,r.permissions_json FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=? ORDER BY r.builtin DESC,r.name`, userID)
	if err != nil {
		return access{}, err
	}
	defer rows.Close()
	ac := access{Permissions: map[Permission]bool{}}
	for rows.Next() {
		var ref roleRef
		var builtin int
		var perms string
		if err = rows.Scan(&ref.ID, &ref.Name, &builtin, &perms); err != nil {
			return access{}, err
		}
		ref.Builtin = builtin == 1
		ac.Roles = append(ac.Roles, ref)
		if ref.ID == roleAdminID {
			ac.Super = true
			continue
		}
		var list []string
		_ = json.Unmarshal([]byte(perms), &list)
		for _, p := range list {
			if permissionIndex[Permission(p)] {
				ac.Permissions[Permission(p)] = true
			}
		}
	}
	if ac.Roles == nil {
		ac.Roles = []roleRef{}
	}
	return ac, rows.Err()
}

func (a *App) userRoleIDs(userID string) ([]string, error) {
	rows, err := a.DB.Query(`SELECT role_id FROM user_roles WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// enabledAdminCount counts enabled users holding the administrator role.
func (a *App) enabledAdminCount() int {
	var n int
	_ = a.DB.QueryRow(`SELECT COUNT(DISTINCT u.id) FROM users u JOIN user_roles ur ON ur.user_id=u.id WHERE u.enabled=1 AND ur.role_id=?`, roleAdminID).Scan(&n)
	return n
}

func hasRole(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// validatePermissions rejects unknown names and, unless the actor is a
// superuser, any permission the actor does not hold themselves.
func validatePermissions(list []string, actor access) ([]string, string) {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range list {
		p := Permission(strings.TrimSpace(raw))
		if !permissionIndex[p] {
			return nil, "未知权限：" + string(p)
		}
		if !actor.allows(p) {
			return nil, "不能授予自己没有的权限：" + string(p)
		}
		if !seen[string(p)] {
			seen[string(p)] = true
			out = append(out, string(p))
		}
	}
	sort.Strings(out)
	return out, ""
}

// ---- handlers ----

func (a *App) permissionCatalogueHandler(w http.ResponseWriter, _ *http.Request) {
	write(w, http.StatusOK, permissionCatalogue)
}

func (a *App) listRoles(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.QueryContext(r.Context(), `SELECT `+roleColumns+` FROM roles r ORDER BY r.builtin DESC,r.created_at,r.rowid`)
	if err != nil {
		writeError(w, r, storageError(err, "读取角色失败"))
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil {
			writeError(w, r, storageError(err, "读取角色失败"))
			return
		}
		out = append(out, role)
	}
	if err = rows.Err(); err != nil {
		writeError(w, r, storageError(err, "读取角色失败"))
		return
	}
	write(w, http.StatusOK, out)
}

type roleInput struct {
	Name            *string  `json:"name"`
	Description     *string  `json:"description"`
	Permissions     []string `json:"permissions"`
	ExpectedVersion int      `json:"expected_version"`
}

func (a *App) createRole(w http.ResponseWriter, r *http.Request) {
	var in roleInput
	if !decodeJSON(w, r, &in, 64<<10) {
		return
	}
	name := ""
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	var problems errs.Fields
	if name == "" || len([]rune(name)) > 40 {
		problems.Add("name", errs.FieldOutOfRange, "角色名称为 1–40 个字符")
	}
	perms, problem := validatePermissions(in.Permissions, sessionUser(r).Access)
	if problem != "" {
		problems.Add("permissions", errs.FieldUnsupportedValue, problem)
	}
	if e := problems.Err(); e != nil {
		writeError(w, r, e)
		return
	}
	description := ""
	if in.Description != nil {
		description = strings.TrimSpace(*in.Description)
	}
	encoded, _ := json.Marshal(perms)
	id, stamp := newID(), now()
	if _, err := a.DB.ExecContext(r.Context(), `INSERT INTO roles(id,name,description,builtin,permissions_json,version,created_at,updated_at) VALUES(?,?,?,0,?,1,?,?)`, id, name, description, string(encoded), stamp, stamp); err != nil {
		dbError(w, r, err, "ROLE_EXISTS", "角色名称已存在", "角色保存失败")
		return
	}
	a.auditFor(r, "role.create", "role", id, name)
	role, _ := a.roleByID(id)
	write(w, http.StatusCreated, role)
}

func (a *App) updateRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in roleInput
	if !decodeJSON(w, r, &in, 64<<10) {
		return
	}
	role, err := a.roleByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "NOT_FOUND", "角色不存在")
			return
		}
		writeError(w, r, storageError(err, "读取角色失败"))
		return
	}
	if in.ExpectedVersion != 0 && in.ExpectedVersion != role.Version {
		fail(w, http.StatusConflict, "VERSION_CONFLICT", "角色已被修改，请刷新后重试")
		return
	}
	if role.ID == roleAdminID && in.Permissions != nil {
		fail(w, http.StatusUnprocessableEntity, "ROLE_IMMUTABLE", "管理员角色的权限不可修改")
		return
	}
	sets, args, detail := []string{}, []any{}, []string{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || len([]rune(name)) > 40 {
			writeError(w, r, fieldErr("name", errs.FieldOutOfRange, "角色名称为 1–40 个字符"))
			return
		}
		if role.Builtin && name != role.Name {
			fail(w, http.StatusUnprocessableEntity, "ROLE_IMMUTABLE", "内置角色不能改名")
			return
		}
		sets, args = append(sets, "name=?"), append(args, name)
		detail = append(detail, "name="+name)
	}
	if in.Description != nil {
		sets, args = append(sets, "description=?"), append(args, strings.TrimSpace(*in.Description))
	}
	if in.Permissions != nil {
		perms, problem := validatePermissions(in.Permissions, sessionUser(r).Access)
		if problem != "" {
			writeError(w, r, fieldErr("permissions", errs.FieldUnsupportedValue, problem))
			return
		}
		encoded, _ := json.Marshal(perms)
		sets, args = append(sets, "permissions_json=?"), append(args, string(encoded))
		detail = append(detail, "permissions="+strings.Join(perms, ","))
	}
	if len(sets) == 0 {
		fail(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "没有可更新字段")
		return
	}
	sets = append(sets, "version=version+1", "updated_at=?")
	args = append(args, now(), id, role.Version)
	if _, err = a.DB.ExecContext(r.Context(), `UPDATE roles SET `+strings.Join(sets, ",")+` WHERE id=? AND version=?`, args...); err != nil {
		dbError(w, r, err, "ROLE_EXISTS", "角色名称已存在", "角色更新失败")
		return
	}
	a.auditFor(r, "role.update", "role", id, role.Name+" "+strings.Join(detail, " "))
	role, _ = a.roleByID(id)
	write(w, http.StatusOK, role)
}

func (a *App) deleteRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	role, err := a.roleByID(id)
	if err != nil {
		fail(w, http.StatusNotFound, "NOT_FOUND", "角色不存在")
		return
	}
	if role.Builtin {
		fail(w, http.StatusUnprocessableEntity, "ROLE_IMMUTABLE", "内置角色不能删除")
		return
	}
	if role.UserCount > 0 {
		fail(w, http.StatusConflict, "ROLE_IN_USE", "仍有用户绑定此角色，请先解除")
		return
	}
	if _, err = a.DB.ExecContext(r.Context(), `DELETE FROM roles WHERE id=?`, id); err != nil {
		writeError(w, r, storageError(err, "角色删除失败"))
		return
	}
	a.auditFor(r, "role.delete", "role", id, role.Name)
	write(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}
