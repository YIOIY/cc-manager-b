package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"cc-manager/internal/errs"
)

// 用户之声: anyone signed in can report a problem or a suggestion from any
// page and follow what happened to it; people holding feedback.manage see
// everything and answer. What is stored is what the user typed plus a small,
// whitelisted description of where they were — never query strings, form
// contents or anything else the page held.

const (
	feedbackTitleMax   = 80
	feedbackBodyMax    = 2000
	feedbackReplyMax   = 1000
	feedbackPageMax    = 200
	feedbackAutoTitle  = 30
	feedbackContextMax = 300
)

var (
	feedbackKinds    = map[string]bool{"problem": true, "suggestion": true, "other": true}
	feedbackStatuses = []string{"open", "in_progress", "resolved", "closed"}
	// feedbackContextKeys are the only keys kept from a request's "context".
	feedbackContextKeys = []string{"error_code", "request_id", "user_agent", "viewport"}
)

func validFeedbackStatus(s string) bool {
	for _, v := range feedbackStatuses {
		if v == s {
			return true
		}
	}
	return false
}

// feedbackPage keeps the path of the page the user was on and drops the
// query and fragment, which can carry search terms.
func feedbackPage(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	if !strings.HasPrefix(raw, "/") || utf8.RuneCountInString(raw) > feedbackPageMax {
		return ""
	}
	return raw
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

// feedbackCreate answers POST /api/v1/feedback for any signed-in user.
func (a *App) feedbackCreate(w http.ResponseWriter, r *http.Request) {
	var in map[string]any
	if !decodeJSON(w, r, &in, defaultBodyLimit) {
		return
	}
	var problems errs.Fields
	kind := str(in, "kind")
	if !feedbackKinds[kind] {
		problems.Add("kind", errs.FieldUnsupportedValue, "请选择反馈类型")
	}
	body := str(in, "body")
	switch n := utf8.RuneCountInString(body); {
	case n == 0:
		problems.Add("body", errs.FieldRequired, "请描述遇到的问题或建议")
	case n > feedbackBodyMax:
		problems.Add("body", errs.FieldOutOfRange, "描述最多 2000 字")
	}
	title := str(in, "title")
	if utf8.RuneCountInString(title) > feedbackTitleMax {
		problems.Add("title", errs.FieldOutOfRange, "标题最多 80 字")
	}
	context := map[string]string{}
	if raw, present := in["context"]; present && raw != nil {
		obj, ok := raw.(map[string]any)
		if !ok {
			problems.Invalid("context", "附带信息格式不正确")
		}
		for _, key := range feedbackContextKeys {
			if v, _ := obj[key].(string); strings.TrimSpace(v) != "" {
				context[key] = truncateRunes(strings.TrimSpace(v), feedbackContextMax)
			}
		}
	}
	if err := problems.Err(); err != nil {
		writeError(w, r, err)
		return
	}
	if title == "" {
		first, _, _ := strings.Cut(body, "\n")
		title = truncateRunes(strings.TrimSpace(first), feedbackAutoTitle)
	}
	contextJSON, _ := json.Marshal(context)
	user := sessionUser(r)
	id, stamp := newID(), now()
	if _, err := a.DB.ExecContext(r.Context(), `INSERT INTO feedback(id,user_id,username,kind,title,body,page,context_json,app_version,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,'open',?,?)`,
		id, user.ID, user.Username, kind, title, body, feedbackPage(str(in, "page")), string(contextJSON), a.cfg.Version, stamp, stamp); err != nil {
		writeError(w, r, storageError(err, "反馈保存失败"))
		return
	}
	a.auditFor(r, "feedback.create", "feedback", id, kind)
	write(w, http.StatusCreated, map[string]any{"id": id})
}

// feedbackScope limits a query to what the caller may see: everything with
// feedback.manage, only their own feedback otherwise.
func feedbackScope(r *http.Request, f *listFilter) (manage bool) {
	user := sessionUser(r)
	if user.Access.allows(PermFeedbackManage) {
		return true
	}
	f.where = append(f.where, "user_id=?")
	f.args = append(f.args, user.ID)
	return false
}

// feedbackList answers GET /api/v1/feedback.
func (a *App) feedbackList(w http.ResponseWriter, r *http.Request) {
	page, size, paged, ok := pageParams(w, r)
	if !ok {
		return
	}
	if !paged {
		page, size = 1, 20
	}
	q := r.URL.Query()
	status, kind := strings.TrimSpace(q.Get("status")), strings.TrimSpace(q.Get("kind"))
	var problems errs.Fields
	if status != "" && !validFeedbackStatus(status) {
		problems.Add("status", errs.FieldUnsupportedValue, "状态无效")
	}
	if kind != "" && !feedbackKinds[kind] {
		problems.Add("kind", errs.FieldUnsupportedValue, "类型无效")
	}
	if problems.Len() > 0 {
		writeError(w, r, errs.New(errs.CodeQueryInvalid).WithFields(problems.Items()...))
		return
	}
	// The per-status counts follow every filter except the status itself, so
	// the tabs show how many there are of each.
	var scope listFilter
	manage := feedbackScope(r, &scope)
	scope.equals("kind", kind)
	scope.keyword(q.Get("q"), "title", "body", "username")
	ctx := r.Context()
	counts := map[string]int{}
	for _, s := range feedbackStatuses {
		counts[s] = 0
	}
	rows, err := a.DB.QueryContext(ctx, `SELECT status,COUNT(*) FROM feedback`+scope.sql()+` GROUP BY status`, scope.args...)
	if err != nil {
		writeError(w, r, storageError(err, "反馈读取失败"))
		return
	}
	for rows.Next() {
		var s string
		var n int
		if err = rows.Scan(&s, &n); err != nil {
			break
		}
		counts[s] = n
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		writeError(w, r, storageError(err, "反馈读取失败"))
		return
	}

	filter := scope
	filter.where = append([]string{}, scope.where...)
	filter.args = append([]any{}, scope.args...)
	filter.equals("status", status)
	total := 0
	if status == "" {
		for _, n := range counts {
			total += n
		}
	} else {
		total = counts[status]
	}
	rows, err = a.DB.QueryContext(ctx, `SELECT id,user_id,username,kind,title,body,page,context_json,app_version,status,reply,handled_by,handled_at,version,created_at,updated_at FROM feedback`+filter.sql()+` ORDER BY created_at DESC,rowid DESC LIMIT ? OFFSET ?`,
		append(append([]any{}, filter.args...), size, (page-1)*size)...)
	if err != nil {
		writeError(w, r, storageError(err, "反馈读取失败"))
		return
	}
	defer rows.Close()
	me := sessionUser(r).ID
	items := []any{}
	for rows.Next() {
		var id, userID, username, kind, title, body, pagePath, contextJSON, appVersion, status, reply, handledBy, handledAt, created, updated string
		var version int
		if err = rows.Scan(&id, &userID, &username, &kind, &title, &body, &pagePath, &contextJSON, &appVersion, &status, &reply, &handledBy, &handledAt, &version, &created, &updated); err != nil {
			writeError(w, r, storageError(err, "反馈读取失败"))
			return
		}
		context := map[string]string{}
		_ = json.Unmarshal([]byte(contextJSON), &context)
		items = append(items, map[string]any{
			"id": id, "username": username, "kind": kind, "title": title, "body": body, "page": pagePath, "context": context, "app_version": appVersion,
			"status": status, "reply": reply, "handled_by": handledBy, "handled_at": handledAt, "version": version, "created_at": created, "updated_at": updated, "mine": userID == me,
		})
	}
	if err = rows.Err(); err != nil {
		writeError(w, r, storageError(err, "反馈读取失败"))
		return
	}
	out := pagedEnvelope(items, page, size, total)
	out["counts"] = counts
	out["can_manage"] = manage
	write(w, http.StatusOK, out)
}

// feedbackSummary answers GET /api/v1/feedback/summary: how many items are
// still waiting, within what the caller may see.
func (a *App) feedbackSummary(w http.ResponseWriter, r *http.Request) {
	var f listFilter
	feedbackScope(r, &f)
	f.equals("status", "open")
	var open int
	if err := a.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM feedback`+f.sql(), f.args...).Scan(&open); err != nil {
		writeError(w, r, storageError(err, "反馈读取失败"))
		return
	}
	write(w, http.StatusOK, map[string]any{"open": open})
}

// feedbackUpdate answers PATCH /api/v1/feedback/{id} (feedback.manage): a
// new status, a reply, or both.
func (a *App) feedbackUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in map[string]any
	if !decodeJSON(w, r, &in, defaultBodyLimit) {
		return
	}
	var problems errs.Fields
	status, hasStatus := in["status"].(string)
	if _, present := in["status"]; present && (!hasStatus || !validFeedbackStatus(status)) {
		problems.Add("status", errs.FieldUnsupportedValue, "状态无效")
	}
	reply, hasReply := in["reply"].(string)
	if _, present := in["reply"]; present && !hasReply {
		problems.Invalid("reply", "回复格式不正确")
	}
	reply = strings.TrimSpace(reply)
	if utf8.RuneCountInString(reply) > feedbackReplyMax {
		problems.Add("reply", errs.FieldOutOfRange, "回复最多 1000 字")
	}
	if err := problems.Err(); err != nil {
		writeError(w, r, err)
		return
	}
	if !hasStatus && !hasReply {
		fail(w, http.StatusUnprocessableEntity, errs.CodeValidationFailed, "没有可更新字段")
		return
	}
	ctx := r.Context()
	var version int
	var oldStatus, oldReply string
	err := a.DB.QueryRowContext(ctx, `SELECT version,status,reply FROM feedback WHERE id=?`, id).Scan(&version, &oldStatus, &oldReply)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "NOT_FOUND", "反馈不存在")
		return
	}
	if err != nil {
		writeError(w, r, storageError(err, "反馈读取失败"))
		return
	}
	if expected := expectedVersion(in); expected != 0 && expected != version {
		fail(w, http.StatusConflict, errs.CodeVersionConflict, "这条反馈刚被其他人处理过，请刷新后重试")
		return
	}
	if !hasStatus {
		status = oldStatus
	}
	if !hasReply {
		reply = oldReply
	}
	stamp := now()
	res, err := a.DB.ExecContext(ctx, `UPDATE feedback SET status=?,reply=?,handled_by=?,handled_at=?,version=version+1,updated_at=? WHERE id=? AND version=?`,
		status, reply, sessionUser(r).Username, stamp, stamp, id, version)
	if err != nil {
		writeError(w, r, storageError(err, "反馈更新失败"))
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, http.StatusConflict, errs.CodeVersionConflict, "这条反馈刚被其他人处理过，请刷新后重试")
		return
	}
	detail := "status=" + status
	if hasReply && reply != oldReply {
		detail += ",reply"
	}
	a.auditFor(r, "feedback.update", "feedback", id, detail)
	write(w, http.StatusOK, map[string]any{"id": id, "version": version + 1, "status": status})
}
