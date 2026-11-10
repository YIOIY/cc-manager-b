package app

import (
	"log/slog"
	"net/http"
)

// audit records an operator-visible event performed by the system itself
// (workers, startup). Details must never contain secrets; callers pass
// identifiers and outcome codes only.
func (a *App) audit(action, resourceType, resourceID, detail string) {
	a.auditAs("system", action, resourceType, resourceID, detail)
}

// auditFor records an event attributed to the signed-in user of r.
func (a *App) auditFor(r *http.Request, action, resourceType, resourceID, detail string) {
	actor := sessionUser(r).Username
	if actor == "" {
		actor = "anonymous"
	}
	a.auditAs(actor, action, resourceType, resourceID, detail)
}

func (a *App) auditAs(actor, action, resourceType, resourceID, detail string) {
	if len(detail) > 500 {
		detail = detail[:500]
	}
	if _, err := a.DB.Exec(`INSERT INTO audit_events(action,resource_type,resource_id,actor,detail,created_at) VALUES(?,?,?,?,?,?)`, action, resourceType, resourceID, actor, detail, now()); err != nil {
		slog.Error("audit write failed", "action", action, "error", err)
	}
}

// auditEvents lists events newest first; paged and filterable like jobs.
func (a *App) auditEvents(w http.ResponseWriter, r *http.Request) {
	page, size, paged, ok := pageParams(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	var f listFilter
	f.contains("action", q.Get("action"))
	f.contains("resource_type", q.Get("resource_type"))
	f.contains("resource_id", q.Get("resource_id"))
	f.dateRange("created_at", q.Get("created_from"), q.Get("created_to"))
	f.keyword(q.Get("q"), "CAST(id AS TEXT)", "action", "resource_type", "resource_id", "detail")
	total := 0
	limit, offset := 500, 0
	if paged {
		if err := a.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM audit_events`+f.sql(), f.args...).Scan(&total); err != nil {
			writeError(w, r, storageError(err, "读取审计事件失败"))
			return
		}
		limit, offset = size, (page-1)*size
	}
	rows, err := a.DB.QueryContext(r.Context(), `SELECT id,action,resource_type,COALESCE(resource_id,''),actor,detail,created_at FROM audit_events`+f.sql()+` ORDER BY id DESC LIMIT ? OFFSET ?`, append(append([]any{}, f.args...), limit, offset)...)
	if err != nil {
		writeError(w, r, storageError(err, "读取审计事件失败"))
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id int64
		var action, resourceType, resourceID, actor, detail, created string
		if err = rows.Scan(&id, &action, &resourceType, &resourceID, &actor, &detail, &created); err != nil {
			writeError(w, r, storageError(err, "读取审计事件失败"))
			return
		}
		out = append(out, map[string]any{"id": id, "action": action, "resource_type": resourceType, "resource_id": resourceID, "actor": actor, "detail": detail, "created_at": created})
	}
	if err = rows.Err(); err != nil {
		writeError(w, r, storageError(err, "读取审计事件失败"))
		return
	}
	if paged {
		write(w, http.StatusOK, pagedEnvelope(out, page, size, total))
		return
	}
	write(w, http.StatusOK, out)
}
