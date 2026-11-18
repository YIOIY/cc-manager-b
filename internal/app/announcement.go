package app

import (
	"database/sql"
	"errors"
	"net/http"
	"unicode/utf8"

	"cc-manager/internal/errs"
)

const (
	announcementID      = "default"
	announcementBodyMax = 500
)

// announcementGet answers GET /api/v1/announcement for any signed-in user.
func (a *App) announcementGet(w http.ResponseWriter, r *http.Request) {
	body, updatedBy, updatedAt, err := a.loadAnnouncement(r)
	if err != nil {
		writeError(w, r, storageError(err, "公告读取失败"))
		return
	}
	write(w, http.StatusOK, map[string]any{
		"body":           body,
		"updated_by":     updatedBy,
		"updated_at":     updatedAt,
		"can_manage":     sessionUser(r).Access.allows(PermAnnouncementManage),
		"max_body_runes": announcementBodyMax,
	})
}

// announcementPut answers PUT /api/v1/announcement. Empty body clears the
// banner for everyone; non-empty publishes the text shown in the header.
func (a *App) announcementPut(w http.ResponseWriter, r *http.Request) {
	var in map[string]any
	if !decodeJSON(w, r, &in, defaultBodyLimit) {
		return
	}
	body := str(in, "body")
	if n := utf8.RuneCountInString(body); n > announcementBodyMax {
		writeError(w, r, errs.New(errs.CodeValidationFailed).WithFields(errs.FieldError{
			Field: "body", Code: errs.FieldOutOfRange, Message: "公告最多 500 字",
		}))
		return
	}
	user := sessionUser(r)
	stamp := now()
	res, err := a.DB.ExecContext(r.Context(),
		`UPDATE app_announcement SET body=?,updated_by=?,updated_at=? WHERE id=?`,
		body, user.Username, stamp, announcementID)
	if err != nil {
		writeError(w, r, storageError(err, "公告保存失败"))
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		if _, err = a.DB.ExecContext(r.Context(),
			`INSERT INTO app_announcement(id,body,updated_by,updated_at) VALUES(?,?,?,?)`,
			announcementID, body, user.Username, stamp); err != nil {
			writeError(w, r, storageError(err, "公告保存失败"))
			return
		}
	}
	action := "announcement.clear"
	if body != "" {
		action = "announcement.publish"
	}
	a.auditFor(r, action, "announcement", announcementID, truncateRunes(body, 80))
	write(w, http.StatusOK, map[string]any{
		"body":       body,
		"updated_by": user.Username,
		"updated_at": stamp,
		"can_manage": true,
	})
}

func (a *App) loadAnnouncement(r *http.Request) (body, updatedBy, updatedAt string, err error) {
	err = a.DB.QueryRowContext(r.Context(),
		`SELECT body,updated_by,updated_at FROM app_announcement WHERE id=?`, announcementID).
		Scan(&body, &updatedBy, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", nil
	}
	return body, updatedBy, updatedAt, err
}
