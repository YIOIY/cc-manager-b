package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
)

// ownershipScope is the row-level view/mutate rule for accounts and proxies:
// admins see everything; everyone else only sees rows they own
// (created_by_user_id); empty created_by is admin-only. Mutations require
// ownership, except admins may mutate unowned rows. Admins reassign ownership
// via /assign without needing canMutate.
type ownershipScope struct {
	Admin  bool
	UserID string
}

func ownershipFrom(r *http.Request) ownershipScope {
	u := sessionUser(r)
	return ownershipScope{Admin: u.Access.Super, UserID: u.ID}
}

func (s ownershipScope) canView(createdBy string) bool {
	if s.Admin {
		return true
	}
	return createdBy != "" && createdBy == s.UserID
}

func (s ownershipScope) canMutate(createdBy string) bool {
	if createdBy != "" && createdBy == s.UserID {
		return true
	}
	return createdBy == "" && s.Admin
}

// filterCreatedBy appends a listFilter constraint for non-admins.
func (s ownershipScope) filterCreatedBy(f *listFilter, column string) {
	if s.Admin {
		return
	}
	f.where = append(f.where, column+"=?")
	f.args = append(f.args, s.UserID)
}

// sqlAnd returns " AND col=?" args for non-admins, or empty for admins.
func (s ownershipScope) sqlAnd(column string) (string, []any) {
	if s.Admin {
		return "", nil
	}
	return " AND " + column + "=?", []any{s.UserID}
}

func (a *App) creatorOf(ctx context.Context, table, id string) (string, error) {
	var createdBy string
	err := a.DB.QueryRowContext(ctx, `SELECT COALESCE(created_by_user_id,'') FROM `+table+` WHERE id=?`, id).Scan(&createdBy)
	return createdBy, err
}

// requireVisible ensures the caller may see the row. Invisible rows look like
// missing ones so existence is not leaked across owners.
func (a *App) requireVisible(w http.ResponseWriter, r *http.Request, table, id, notFoundMsg string) (createdBy string, ok bool) {
	createdBy, err := a.creatorOf(r.Context(), table, id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "NOT_FOUND", notFoundMsg)
		return "", false
	}
	if err != nil {
		writeError(w, r, storageError(err, "读取失败"))
		return "", false
	}
	if !ownershipFrom(r).canView(createdBy) {
		fail(w, http.StatusNotFound, "NOT_FOUND", notFoundMsg)
		return "", false
	}
	return createdBy, true
}

// requireMutable ensures the caller may change the row. Cross-owner access is
// view-only even for admins; only the creator (or admin on unowned rows) may write.
func (a *App) requireMutable(w http.ResponseWriter, r *http.Request, table, id, notFoundMsg string) bool {
	createdBy, ok := a.requireVisible(w, r, table, id, notFoundMsg)
	if !ok {
		return false
	}
	if !ownershipFrom(r).canMutate(createdBy) {
		fail(w, http.StatusForbidden, "PERMISSION_DENIED", "只能查看他人创建的资源，不能修改")
		return false
	}
	return true
}

// requireProxyBindable checks that proxyID may be attached to an account by
// the caller: own proxies, or (admin only) unowned proxies.
func (a *App) requireProxyBindable(w http.ResponseWriter, r *http.Request, proxyID string) bool {
	if proxyID == "" {
		return true
	}
	createdBy, err := a.creatorOf(r.Context(), "proxies", proxyID)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusUnprocessableEntity, "PROXY_INVALID", "代理不存在")
		return false
	}
	if err != nil {
		writeError(w, r, storageError(err, "代理读取失败"))
		return false
	}
	scope := ownershipFrom(r)
	if createdBy == scope.UserID {
		return true
	}
	if scope.Admin && createdBy == "" {
		return true
	}
	fail(w, http.StatusUnprocessableEntity, "PROXY_INVALID", "只能绑定自己创建的代理")
	return false
}

// jobsOwnershipSQL limits jobs to those whose account the caller can see.
// Jobs without an account_id are admin-only.
func (s ownershipScope) jobsOwnershipSQL(accountColumn string) (string, []any) {
	if s.Admin {
		return "", nil
	}
	return " AND " + accountColumn + ` IN (SELECT id FROM accounts WHERE created_by_user_id=?)`, []any{s.UserID}
}
