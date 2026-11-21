package app

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cc-manager/internal/errs"
)

type errorEnvelope struct {
	Error struct {
		Code      string            `json:"code"`
		Category  string            `json:"category"`
		Message   string            `json:"message"`
		Hint      string            `json:"hint"`
		Action    string            `json:"action"`
		Retryable bool              `json:"retryable"`
		Fields    []errs.FieldError `json:"fields"`
		Details   map[string]any    `json:"details"`
	} `json:"error"`
	RequestID string `json:"request_id"`
}

func TestErrorEnvelopeCarriesClassificationAndRequestID(t *testing.T) {
	_, admin := rbacApp(t)
	res := admin.do(http.MethodGet, "/api/v1/accounts/missing", "")
	var body errorEnvelope
	_ = jsonDecode(res, &body)
	if res.StatusCode != 404 || body.Error.Code != "NOT_FOUND" || body.Error.Category != "not_found" || body.Error.Action != "none" || body.Error.Retryable {
		t.Fatalf("status=%d body=%+v", res.StatusCode, body)
	}
	if body.RequestID == "" || body.RequestID != res.Header.Get("X-Request-ID") {
		t.Fatalf("request id missing or mismatched: %q vs %q", body.RequestID, res.Header.Get("X-Request-ID"))
	}
	ok := admin.do(http.MethodGet, "/api/v1/auth/session", "")
	var okBody map[string]any
	_ = jsonDecode(ok, &okBody)
	if id, _ := okBody["request_id"].(string); id == "" {
		t.Fatalf("success body lacks request_id: %v", okBody)
	}
}

func TestLegacyFailKeepsRegistryMessageUnlessCustomAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	fail(w, 429, "RATE_LIMITED", "自定义")
	def, _ := errs.Lookup("RATE_LIMITED")
	if !strings.Contains(w.Body.String(), def.Message) || strings.Contains(w.Body.String(), "自定义") {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	fail(w, 404, "NOT_FOUND", "代理不存在")
	if !strings.Contains(w.Body.String(), "代理不存在") || !strings.Contains(w.Body.String(), `"category":"not_found"`) {
		t.Fatal(w.Body.String())
	}
	// status comes from the registry, not the caller
	w = httptest.NewRecorder()
	fail(w, 400, "NOT_FOUND", "")
	if w.Code != 404 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestWriteErrorAddsRetryAfterForRateLimits(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	writeError(w, r, errs.New("RATE_LIMITED").WithDetail("retry_after_seconds", 7))
	if w.Code != 429 || w.Header().Get("Retry-After") != "7" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Retry-After"))
	}
	w = httptest.NewRecorder()
	writeError(w, r, errSentinelForHTTP)
	if w.Code != 500 || !strings.Contains(w.Body.String(), `"code":"INTERNAL_ERROR"`) || strings.Contains(w.Body.String(), "sentinel") {
		t.Fatal(w.Body.String())
	}
}

func TestIdempotentReplayStillReturns202WithRequestID(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	body := deliveryBody()
	first := onboardRequest(a, body, "replay-envelope")
	second := onboardRequest(a, body, "replay-envelope")
	if first.Code != 202 || second.Code != 202 {
		t.Fatalf("codes %d %d: %s", first.Code, second.Code, second.Body.String())
	}
	if responseData(t, first)["account_id"] != responseData(t, second)["account_id"] {
		t.Fatal("replay returned a different account")
	}
}

func assertEnvelope(t *testing.T, res *http.Response, status int, code string) errorEnvelope {
	t.Helper()
	var body errorEnvelope
	_ = jsonDecode(res, &body)
	if res.StatusCode != status || body.Error.Code != code {
		t.Fatalf("want %d %s, got %d %+v", status, code, res.StatusCode, body.Error)
	}
	return body
}

func fieldSet(env errorEnvelope) map[string]string {
	out := map[string]string{}
	for _, f := range env.Error.Fields {
		out[f.Field] = f.Code
	}
	return out
}

func TestMergedCodesAndCollectedFields(t *testing.T) {
	t.Setenv("CC_MANAGER_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	a, admin := rbacApp(t)
	// malformed body → REQUEST_MALFORMED
	assertEnvelope(t, admin.do(http.MethodPost, "/api/v1/accounts", `{"account":`), 400, "REQUEST_MALFORMED")
	// bad paging and filters → QUERY_INVALID with fields
	env := assertEnvelope(t, admin.do(http.MethodGet, "/api/v1/accounts?page=0&page_size=7", ""), 422, "QUERY_INVALID")
	if fs := fieldSet(env); fs["page"] == "" || fs["page_size"] == "" {
		t.Fatalf("paging fields %+v", env.Error.Fields)
	}
	env = assertEnvelope(t, admin.do(http.MethodGet, "/api/v1/accounts?archived=maybe", ""), 422, "QUERY_INVALID")
	if fieldSet(env)["archived"] == "" {
		t.Fatalf("filter fields %+v", env.Error.Fields)
	}
	// connection create collects every field problem
	env = assertEnvelope(t, admin.do(http.MethodPost, "/api/v1/platform-connections", `{"timezone":"Mars/Olympus"}`), 422, "VALIDATION_FAILED")
	if fs := fieldSet(env); fs["token"] != "REQUIRED" || fs["base_url"] == "" || fs["timezone"] == "" {
		t.Fatalf("connection fields %+v", env.Error.Fields)
	}
	// user create collects username, password and roles
	env = assertEnvelope(t, admin.do(http.MethodPost, "/api/v1/users", `{"username":"x","password":"short","role_ids":[]}`), 422, "VALIDATION_FAILED")
	if fs := fieldSet(env); fs["username"] == "" || fs["password"] == "" || fs["role_ids"] == "" {
		t.Fatalf("user fields %+v", env.Error.Fields)
	}
	// proxy create collects scheme, host and port
	env = assertEnvelope(t, admin.do(http.MethodPost, "/api/v1/proxies", `{"scheme":"ftp","host":"","port":0}`), 422, "VALIDATION_FAILED")
	if fs := fieldSet(env); fs["scheme"] == "" || fs["host"] == "" || fs["port"] == "" {
		t.Fatalf("proxy fields %+v", env.Error.Fields)
	}
	// duplicate account → ACCOUNT_EXISTS
	if res := admin.do(http.MethodPost, "/api/v1/accounts", `{"account":"dup@example.test","plan":"claude/cc-pro"}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("create=%d", res.StatusCode)
	}
	assertEnvelope(t, admin.do(http.MethodPost, "/api/v1/accounts", `{"account":"dup@example.test","plan":"claude/cc-pro"}`), 409, "ACCOUNT_EXISTS")
	// permission denial names the permission
	if res := admin.do(http.MethodPost, "/api/v1/users", `{"username":"ro2","password":"operator-password","role_ids":["`+roleViewerID+`"]}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("create viewer=%d", res.StatusCode)
	}
	viewer := newTestClient(t, a)
	viewer.loginAs("ro2", "operator-password")
	env = assertEnvelope(t, viewer.do(http.MethodPost, "/api/v1/proxies", `{"scheme":"http","host":"1.2.3.4","port":8080}`), 403, "PERMISSION_DENIED")
	if env.Error.Details["permission"] != "proxies.create" {
		t.Fatalf("details %+v", env.Error.Details)
	}
}

func TestWriteRateLimitCarriesRetryAfter(t *testing.T) {
	t.Setenv("CC_MANAGER_WRITE_RATE_PER_MINUTE", "3")
	_, admin := rbacApp(t)
	var limited *http.Response
	for i := 0; i < 8 && limited == nil; i++ {
		res := admin.do(http.MethodPost, "/api/v1/proxies", `{"scheme":"http","host":"10.0.0.`+string(rune('1'+i))+`","port":8080}`)
		if res.StatusCode == http.StatusTooManyRequests {
			limited = res
		}
	}
	if limited == nil {
		t.Fatal("write limiter never triggered")
	}
	env := assertEnvelope(t, limited, 429, "RATE_LIMITED")
	if limited.Header.Get("Retry-After") == "" || fmt.Sprint(env.Error.Details["retry_after_seconds"]) != limited.Header.Get("Retry-After") {
		t.Fatalf("retry-after header %q vs details %v", limited.Header.Get("Retry-After"), env.Error.Details)
	}
}

func TestDisabledUserGetsUserDisabled(t *testing.T) {
	a, admin := rbacApp(t)
	res := admin.do(http.MethodPost, "/api/v1/users", `{"username":"gone","password":"operator-password","role_ids":["`+roleViewerID+`"]}`)
	var created struct {
		Data struct{ ID string }
	}
	_ = jsonDecode(res, &created)
	user := newTestClient(t, a)
	if res := user.loginAs("gone", "operator-password"); res.StatusCode != http.StatusOK {
		t.Fatalf("login=%d", res.StatusCode)
	}
	// Disabling through the API also revokes sessions; flip the row directly so
	// the still-valid cookie meets a disabled user on its next request.
	if _, err := a.DB.Exec(`UPDATE users SET enabled=0 WHERE id=?`, created.Data.ID); err != nil {
		t.Fatal(err)
	}
	assertEnvelope(t, user.do(http.MethodGet, "/api/v1/accounts", ""), 401, "USER_DISABLED")
	// and the session is gone afterwards
	assertEnvelope(t, user.do(http.MethodGet, "/api/v1/accounts", ""), 401, "AUTH_REQUIRED")
}
