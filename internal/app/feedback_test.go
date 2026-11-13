package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type feedbackItem struct {
	ID         string            `json:"id"`
	Username   string            `json:"username"`
	Kind       string            `json:"kind"`
	Title      string            `json:"title"`
	Body       string            `json:"body"`
	Page       string            `json:"page"`
	Context    map[string]string `json:"context"`
	AppVersion string            `json:"app_version"`
	Status     string            `json:"status"`
	Reply      string            `json:"reply"`
	HandledBy  string            `json:"handled_by"`
	HandledAt  string            `json:"handled_at"`
	Version    int               `json:"version"`
	CreatedAt  string            `json:"created_at"`
	Mine       bool              `json:"mine"`
}

type feedbackList struct {
	Data struct {
		Items      []feedbackItem `json:"items"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
		Counts    map[string]int `json:"counts"`
		CanManage bool           `json:"can_manage"`
	} `json:"data"`
}

func listFeedback(t *testing.T, c *testClient, query string) feedbackList {
	t.Helper()
	var out feedbackList
	res := c.do(http.MethodGet, "/api/v1/feedback?page=1&page_size=20"+query, "")
	if err := jsonDecode(res, &out); err != nil || res.StatusCode != 200 {
		t.Fatalf("list feedback: %d %v", res.StatusCode, err)
	}
	return out
}

func postFeedback(t *testing.T, c *testClient, body string) (int, string) {
	t.Helper()
	res := c.do(http.MethodPost, "/api/v1/feedback", body)
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = jsonDecode(res, &out)
	return res.StatusCode, out.Data.ID
}

// feedbackUsers returns the app with an admin, an operator and a viewer signed in.
func feedbackUsers(t *testing.T) (*App, *testClient, *testClient, *testClient) {
	t.Helper()
	a, admin := rbacApp(t)
	for _, body := range []string{
		`{"username":"ops","password":"operator-password","role_ids":["` + roleOperatorID + `"]}`,
		`{"username":"ro","password":"viewer-password-1","role_ids":["` + roleViewerID + `"]}`,
	} {
		if res := admin.do(http.MethodPost, "/api/v1/users", body); res.StatusCode != http.StatusCreated {
			t.Fatalf("create user: %d", res.StatusCode)
		}
	}
	ops, viewer := newTestClient(t, a), newTestClient(t, a)
	ops.loginAs("ops", "operator-password")
	viewer.loginAs("ro", "viewer-password-1")
	return a, admin, ops, viewer
}

func TestFeedbackSubmitAndSeeOwn(t *testing.T) {
	a, admin, ops, viewer := feedbackUsers(t)
	status, id := postFeedback(t, ops, `{"kind":"problem","title":"上号 403","body":"提交后提示当前供应商未获授权该厂商","page":"/accounts?q=secret@example.test","context":{"error_code":"PLATFORM_AUTH_FAILED","request_id":"req-123","user_agent":"UA","password":"must-not-be-kept"}}`)
	if status != http.StatusCreated || id == "" {
		t.Fatalf("submit: %d %q", status, id)
	}
	// A title is optional: the start of the text is used.
	if status, _ = postFeedback(t, viewer, `{"kind":"suggestion","body":"希望仪表盘能导出\n第二行"}`); status != http.StatusCreated {
		t.Fatalf("viewer submit: %d", status)
	}

	mine := listFeedback(t, ops, "")
	if mine.Data.CanManage || len(mine.Data.Items) != 1 || mine.Data.Pagination.Total != 1 {
		t.Fatalf("operator sees %d items, manage=%v", len(mine.Data.Items), mine.Data.CanManage)
	}
	it := mine.Data.Items[0]
	if it.ID != id || it.Kind != "problem" || it.Title != "上号 403" || it.Status != "open" || it.Username != "ops" || !it.Mine || it.Version != 1 || it.CreatedAt == "" {
		t.Fatalf("item=%+v", it)
	}
	// Only the path is kept, and only the allowed context keys.
	if it.Page != "/accounts" || it.Context["error_code"] != "PLATFORM_AUTH_FAILED" || it.Context["request_id"] != "req-123" || len(it.Context) != 3 {
		t.Fatalf("page=%q context=%v", it.Page, it.Context)
	}
	if it.AppVersion != a.cfg.Version {
		t.Fatalf("app version=%q want %q", it.AppVersion, a.cfg.Version)
	}
	if mine.Data.Counts["open"] != 1 || mine.Data.Counts["resolved"] != 0 {
		t.Fatalf("counts=%v", mine.Data.Counts)
	}

	other := listFeedback(t, viewer, "")
	if len(other.Data.Items) != 1 || other.Data.Items[0].Title != "希望仪表盘能导出" || other.Data.Items[0].Username != "ro" {
		t.Fatalf("viewer sees %+v", other.Data.Items)
	}

	all := listFeedback(t, admin, "")
	if !all.Data.CanManage || len(all.Data.Items) != 2 || all.Data.Counts["open"] != 2 {
		t.Fatalf("admin sees %d items, counts=%v", len(all.Data.Items), all.Data.Counts)
	}
	// Newest first; the admin's own flag is false for other people's feedback.
	if all.Data.Items[0].Username != "ro" || all.Data.Items[0].Mine {
		t.Fatalf("order/mine: %+v", all.Data.Items[0])
	}
	if got := listFeedback(t, admin, "&kind=problem"); len(got.Data.Items) != 1 || got.Data.Items[0].ID != id {
		t.Fatalf("kind filter: %+v", got.Data.Items)
	}
	if got := listFeedback(t, admin, "&q=导出"); len(got.Data.Items) != 1 {
		t.Fatalf("keyword filter: %d", len(got.Data.Items))
	}
	if n := queryInt(t, a, `SELECT COUNT(*) FROM audit_events WHERE action='feedback.create' AND actor='ops' AND resource_id=?`, id); n != 1 {
		t.Fatalf("audit rows=%d", n)
	}
	raw := queryString(t, a, `SELECT context_json FROM feedback WHERE id=?`, id)
	if strings.Contains(raw, "must-not-be-kept") || strings.Contains(queryString(t, a, `SELECT page FROM feedback WHERE id=?`, id), "secret") {
		t.Fatalf("sensitive input was stored: %s", raw)
	}
}

func TestFeedbackValidation(t *testing.T) {
	_, admin, _, _ := feedbackUsers(t)
	for name, body := range map[string]string{
		"no body":      `{"kind":"problem","body":"   "}`,
		"bad kind":     `{"kind":"rant","body":"something is wrong"}`,
		"long title":   `{"kind":"problem","title":"` + strings.Repeat("长", 81) + `","body":"something is wrong"}`,
		"long body":    `{"kind":"problem","body":"` + strings.Repeat("字", 2001) + `"}`,
		"context type": `{"kind":"problem","body":"something is wrong","context":"nope"}`,
	} {
		res := admin.do(http.MethodPost, "/api/v1/feedback", body)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status=%d", name, res.StatusCode)
		}
	}
	if got := listFeedback(t, admin, ""); len(got.Data.Items) != 0 {
		t.Fatalf("invalid feedback was stored: %d", len(got.Data.Items))
	}
	assertEnvelope(t, admin.do(http.MethodGet, "/api/v1/feedback?page=1&page_size=20&status=bogus", ""), http.StatusUnprocessableEntity, "QUERY_INVALID")
}

func TestFeedbackHandling(t *testing.T) {
	a, admin, ops, viewer := feedbackUsers(t)
	_, id := postFeedback(t, ops, `{"kind":"problem","body":"授权链接复制不了"}`)
	patch := func(c *testClient, body string) *http.Response {
		return c.do(http.MethodPatch, "/api/v1/feedback/"+id, body)
	}
	// Only people with feedback.manage may handle feedback, not even the author.
	assertEnvelope(t, patch(ops, `{"status":"resolved","expected_version":1}`), http.StatusForbidden, "PERMISSION_DENIED")
	assertEnvelope(t, patch(viewer, `{"status":"resolved","expected_version":1}`), http.StatusForbidden, "PERMISSION_DENIED")

	assertEnvelope(t, patch(admin, `{"status":"done","expected_version":1}`), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	assertEnvelope(t, patch(admin, `{"reply":"`+strings.Repeat("回", 1001)+`","expected_version":1}`), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	assertEnvelope(t, patch(admin, `{"expected_version":1}`), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	assertEnvelope(t, patch(admin, `{"status":"resolved","expected_version":9}`), http.StatusConflict, "VERSION_CONFLICT")
	assertEnvelope(t, admin.do(http.MethodPatch, "/api/v1/feedback/nope", `{"status":"resolved"}`), http.StatusNotFound, "NOT_FOUND")

	if res := patch(admin, `{"status":"in_progress","reply":"已定位，下个版本修复","expected_version":1}`); res.StatusCode != 200 {
		t.Fatalf("handle: %d", res.StatusCode)
	}
	it := listFeedback(t, ops, "").Data.Items[0]
	if it.Status != "in_progress" || it.Reply != "已定位，下个版本修复" || it.HandledBy != "admin" || it.HandledAt == "" || it.Version != 2 {
		t.Fatalf("after handling: %+v", it)
	}
	// Changing only the status keeps the reply.
	if res := patch(admin, `{"status":"resolved","expected_version":2}`); res.StatusCode != 200 {
		t.Fatalf("resolve: %d", res.StatusCode)
	}
	it = listFeedback(t, ops, "").Data.Items[0]
	if it.Status != "resolved" || it.Reply != "已定位，下个版本修复" || it.Version != 3 {
		t.Fatalf("after resolving: %+v", it)
	}
	if got := listFeedback(t, admin, "&status=open"); len(got.Data.Items) != 0 || got.Data.Counts["resolved"] != 1 {
		t.Fatalf("status filter: items=%d counts=%v", len(got.Data.Items), got.Data.Counts)
	}
	if n := queryInt(t, a, `SELECT COUNT(*) FROM audit_events WHERE action='feedback.update' AND actor='admin' AND resource_id=?`, id); n != 2 {
		t.Fatalf("audit rows=%d", n)
	}
}

func TestFeedbackSummaryAndAccess(t *testing.T) {
	a, admin, ops, viewer := feedbackUsers(t)
	summary := func(c *testClient) int {
		var out struct {
			Data struct {
				Open int `json:"open"`
			} `json:"data"`
		}
		res := c.do(http.MethodGet, "/api/v1/feedback/summary", "")
		if err := jsonDecode(res, &out); err != nil || res.StatusCode != 200 {
			t.Fatalf("summary: %d %v", res.StatusCode, err)
		}
		return out.Data.Open
	}
	postFeedback(t, ops, `{"kind":"problem","body":"第一条问题"}`)
	postFeedback(t, ops, `{"kind":"other","body":"第二条"}`)
	_, third := postFeedback(t, viewer, `{"kind":"problem","body":"第三条问题"}`)
	if summary(admin) != 3 || summary(ops) != 2 || summary(viewer) != 1 {
		t.Fatalf("summary admin=%d ops=%d viewer=%d", summary(admin), summary(ops), summary(viewer))
	}
	admin.do(http.MethodPatch, "/api/v1/feedback/"+third, `{"status":"closed","expected_version":1}`)
	if summary(admin) != 2 || summary(viewer) != 0 {
		t.Fatalf("after closing: admin=%d viewer=%d", summary(admin), summary(viewer))
	}
	anonymous := newTestClient(t, a)
	for _, call := range []string{"GET /api/v1/feedback", "POST /api/v1/feedback", "GET /api/v1/feedback/summary"} {
		m, p, _ := strings.Cut(call, " ")
		if res := anonymous.do(m, p, `{"kind":"problem","body":"anonymous"}`); res.StatusCode != http.StatusUnauthorized && res.StatusCode != http.StatusForbidden {
			t.Fatalf("anonymous %s: %d", call, res.StatusCode)
		}
	}
	if n := queryInt(t, a, `SELECT COUNT(*) FROM feedback`); n != 3 {
		t.Fatalf("rows=%d", n)
	}
}

func TestFeedbackPermissionInCatalogue(t *testing.T) {
	found := false
	for _, mod := range permissionCatalogue {
		for _, def := range mod.Permissions {
			if def.Key == PermFeedbackManage {
				found = mod.Key == "feedback"
			}
		}
	}
	if !found {
		t.Fatal("feedback.manage is not in the 用户之声 module")
	}
	for _, role := range builtinRoles() {
		if role.id == roleAdminID {
			continue
		}
		for _, p := range role.permissions {
			if p == string(PermFeedbackManage) {
				t.Fatalf("built-in role %s grants feedback.manage", role.name)
			}
		}
	}
	_ = fmt.Sprint
	_ = json.Valid
}
