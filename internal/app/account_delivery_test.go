package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func deliveryFixture(t *testing.T, validators ...func(map[string]any)) (*App, *atomic.Int64, *atomic.Int64, *atomic.Int32) {
	t.Helper()
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "workflow-test-password")
	t.Setenv("CC_MANAGER_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("CC_MANAGER_SUPPLIER_TLS_PINS", "")
	t.Setenv("CC_MANAGER_SUPPLIER_INSECURE_TLS", "1")
	calls, reads, mode := &atomic.Int64{}, &atomic.Int64{}, &atomic.Int32{}
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/supplier-account/api/openapi/claude/accounts" {
			reads.Add(1)
			http.Error(w, "metrics unavailable", 404)
			return
		}
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture-openapi-token" {
			t.Error("incorrect platform credential")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["password"] != "fixture-password" {
			t.Error("missing decrypted account credentials")
		}
		for _, validate := range validators {
			validate(body)
		}
		switch mode.Load() {
		case 1:
			http.Error(w, `{"detail":"rejected"}`, 422)
		case 2:
			w.Write([]byte(`{"message":"incomplete upstream response"}`))
		default:
			w.Write([]byte(`{"id":12345,"alias":"fixture-alias"}`))
		}
	}))
	t.Cleanup(remote.Close)
	a, e := newApp(t.TempDir()+"/workflow.db", false)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.DB.Close() })
	sealed, e := a.keys.Seal([]byte("fixture-openapi-token"), []byte("conn"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = a.DB.Exec(`INSERT INTO platform_connections(id,name,base_url,token_secret,timezone,enabled,version) VALUES('conn','Fixture',?,?,'Asia/Shanghai',1,1)`, remote.URL, sealed)
	if e != nil {
		t.Fatal(e)
	}
	return a, calls, reads, mode
}

func deliveryBody() map[string]any {
	return map[string]any{"account": "workflow@example.test", "password": "fixture-password", "plan": "claude/cc-max-20x", "register_location": "US", "register_time": "2026-11-04", "plan_start_time": "2026-11-04", "plan_end_time": "2026-12-04", "platform_connection_id": "conn", "auth_json": map[string]any{"id_token": "", "access_token": "fixture-access", "refresh_token": "fixture-refresh", "last_refresh": "2026-11-04T00:00:00Z", "email": "workflow@example.test", "account_uuid": "account-uuid", "organization_uuid": "org-uuid", "organization_name": "Fixture", "claude_device_ids": []any{"device-id"}, "type": "claude", "expired": "2026-12-04T00:00:00Z", "proxy_url": "http://127.0.0.1:9090", "verification_status": "verified"}}
}

func onboardRequest(a *App, body map[string]any, key string, existing ...string) *httptest.ResponseRecorder {
	encoded, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/api/v1/accounts/onboard", bytes.NewReader(encoded))
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	a.onboardAccount(w, r, existing...)
	return w
}
func responseData(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &envelope); e != nil {
		t.Fatal(e)
	}
	return envelope.Data
}
func deliveryAccount(t *testing.T, a *App) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	a.accounts(w, httptest.NewRequest("GET", "/accounts", nil))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &envelope); e != nil {
		t.Fatal(e)
	}
	if len(envelope.Data) != 1 {
		t.Fatalf("accounts: %s", w.Body.String())
	}
	return envelope.Data[0]
}
func submitRequest(a *App, id, key string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/accounts/"+id+"/submit", strings.NewReader(`{"platform_connection_id":"conn"}`))
	r.Header.Set("Idempotency-Key", key)
	a.submitExistingAccount(w, r, id)
	return w
}

func TestCreateAutomaticallyQueuesOnceAndAcceptedAccountSurvivesUnavailableUsage(t *testing.T) {
	a, calls, reads, _ := deliveryFixture(t)
	w := onboardRequest(a, deliveryBody(), "create-once")
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	id := responseData(t, w)["account_id"].(string)
	replay := onboardRequest(a, deliveryBody(), "create-once")
	if replay.Code != 202 || responseData(t, replay)["account_id"] != id {
		t.Fatal(replay.Body.String())
	}
	if calls.Load() != 0 || deliveryAccount(t, a)["onboarding_state"] != "submitting" {
		t.Fatal("create did not atomically queue delivery")
	}
	var credentials string
	if e := a.DB.QueryRow(`SELECT credentials_json FROM accounts WHERE id=?`, id).Scan(&credentials); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(credentials, "fixture-access") {
		t.Fatal("plaintext credential stored")
	}
	a.runOne(context.Background())
	account := deliveryAccount(t, a)
	if calls.Load() != 1 || reads.Load() != 0 || account["onboarding_state"] != "linked" || account["platform_alias"] != "fixture-alias" {
		t.Fatalf("acceptance improperly depends on usage: %+v calls=%d reads=%d", account, calls.Load(), reads.Load())
	}
	if strings.Contains(w.Body.String(), "fixture-password") {
		t.Fatal("credential leaked in response")
	}
	blocked := submitRequest(a, id, "duplicate")
	if blocked.Code != 409 || !strings.Contains(blocked.Body.String(), "ALREADY_LINKED") {
		t.Fatal(blocked.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatal("linked account uploaded again")
	}
}

func TestRejectedAccountRetainsCredentialsAndCanRetryWithoutReentry(t *testing.T) {
	a, calls, _, mode := deliveryFixture(t)
	mode.Store(1)
	w := onboardRequest(a, deliveryBody(), "rejected")
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	id := responseData(t, w)["account_id"].(string)
	a.runOne(context.Background())
	account := deliveryAccount(t, a)
	if account["onboarding_state"] != "failed" || account["password_configured"] != true || account["credentials_configured"] != true {
		t.Fatalf("failed delivery lost local data: %+v", account)
	}
	mode.Store(0)
	retry := submitRequest(a, id, "retry")
	if retry.Code != 202 {
		t.Fatal(retry.Body.String())
	}
	a.runOne(context.Background())
	if deliveryAccount(t, a)["onboarding_state"] != "linked" || calls.Load() != 2 {
		t.Fatal("retry failed")
	}
}

func TestUncertainSubmissionIsNeverAutomaticallyResent(t *testing.T) {
	a, calls, _, mode := deliveryFixture(t)
	mode.Store(2)
	w := onboardRequest(a, deliveryBody(), "uncertain")
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	id := responseData(t, w)["account_id"].(string)
	a.runOne(context.Background())
	if deliveryAccount(t, a)["onboarding_state"] != "unknown" {
		t.Fatal("uncertain outcome not visible")
	}
	blocked := submitRequest(a, id, "uncertain-retry")
	if blocked.Code != 409 || !strings.Contains(blocked.Body.String(), "SUBMIT_OUTCOME_UNKNOWN") {
		t.Fatal(blocked.Body.String())
	}
	blocked = onboardRequest(a, deliveryBody(), "uncertain-edit", id)
	if blocked.Code != 409 {
		t.Fatal(blocked.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatal("uncertain outcome was resent")
	}
}

func TestDraftCanBeCompletedAndSubmittedInOneOperation(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	w := httptest.NewRecorder()
	a.accounts(w, httptest.NewRequest("POST", "/accounts", strings.NewReader(`{"account":"workflow@example.test","plan":"claude/cc-max-20x"}`)))
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	id := responseData(t, w)["id"].(string)
	if deliveryAccount(t, a)["onboarding_state"] != "pending" {
		t.Fatal("draft not marked as requiring delivery")
	}
	w = onboardRequest(a, deliveryBody(), "complete-draft", id)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	if responseData(t, w)["account_id"] != id {
		t.Fatal("draft completion created a duplicate")
	}
	a.runOne(context.Background())
	if deliveryAccount(t, a)["onboarding_state"] != "linked" {
		t.Fatal("completed draft was not delivered")
	}
}

func TestParallelSubmitCreatesOnlyOneJob(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	w := onboardRequest(a, deliveryBody(), "parallel-create")
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	id := responseData(t, w)["account_id"].(string)
	_, e := a.DB.Exec(`UPDATE jobs SET state='failed'; UPDATE deliveries SET ingestion_state='rejected'`)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, key := range []string{"parallel-a", "parallel-b"} {
		wg.Add(1)
		go func(k string) { defer wg.Done(); codes <- submitRequest(a, id, k).Code }(key)
	}
	wg.Wait()
	close(codes)
	accepted := 0
	for code := range codes {
		if code == 202 {
			accepted++
		} else if code != 409 {
			t.Fatalf("unexpected code %d", code)
		}
	}
	var queued int
	a.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE state='queued'`).Scan(&queued)
	if accepted != 1 || queued != 1 {
		t.Fatalf("duplicate queue: accepted %d queued %d", accepted, queued)
	}
}

func TestCreateIdempotencyRejectsDifferentAccountWithoutWritingIt(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	w := onboardRequest(a, deliveryBody(), "same-key")
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	other := deliveryBody()
	other["account"] = "other@example.test"
	w = onboardRequest(a, other, "same-key")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "IDEMPOTENCY_CONFLICT") {
		t.Fatal(w.Body.String())
	}
	deliveryAccount(t, a)
}

func TestCompleteRejectedAccountReusesStoredCredentialsAndKeepsIdentity(t *testing.T) {
	a, calls, _, mode := deliveryFixture(t)
	mode.Store(1)
	w := onboardRequest(a, deliveryBody(), "create-to-complete")
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	id := responseData(t, w)["account_id"].(string)
	a.runOne(context.Background())
	mode.Store(0)
	body := map[string]any{"platform_connection_id": "conn", "remark": "updated before retry"}
	w = onboardRequest(a, body, "complete-with-saved-secrets", id)
	if w.Code != 202 || responseData(t, w)["account_id"] != id {
		t.Fatal(w.Body.String())
	}
	replay := onboardRequest(a, body, "complete-with-saved-secrets", id)
	if replay.Code != 202 || responseData(t, replay)["job_id"] != responseData(t, w)["job_id"] {
		t.Fatal(replay.Body.String())
	}
	a.runOne(context.Background())
	account := deliveryAccount(t, a)
	if calls.Load() != 2 || account["onboarding_state"] != "linked" || account["remark"] != "updated before retry" {
		t.Fatalf("completion failed: %+v", account)
	}
}

func deleteRequest(a *App, id string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/accounts/"+id, nil)
	r.SetPathValue("id", id)
	a.accountDelete(w, r)
	return w
}

func TestUndeliveredAccountCanBeDeletedButDeliveredCannot(t *testing.T) {
	a, _, _, mode := deliveryFixture(t)
	mode.Store(1)
	w := onboardRequest(a, deliveryBody(), "to-delete")
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	id := responseData(t, w)["account_id"].(string)
	// Still queued: the outcome is unknown, so deletion must wait.
	if res := deleteRequest(a, id); res.Code != 409 || !strings.Contains(res.Body.String(), "ACCOUNT_IN_PROGRESS") {
		t.Fatalf("queued delete=%d %s", res.Code, res.Body.String())
	}
	a.runOne(context.Background())
	if deliveryAccount(t, a)["onboarding_state"] != "failed" {
		t.Fatal("fixture did not fail")
	}
	if res := deleteRequest(a, id); res.Code != 200 {
		t.Fatalf("failed-account delete=%d %s", res.Code, res.Body.String())
	}
	for _, table := range []string{"accounts", "account_secrets", "jobs", "deliveries"} {
		var n int
		col := "account_id"
		if table == "accounts" {
			col = "id"
		}
		if e := a.DB.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+col+`=?`, id).Scan(&n); e != nil || n != 0 {
			t.Fatalf("%s still has %d rows (err %v)", table, n, e)
		}
	}
	if res := deleteRequest(a, id); res.Code != 404 {
		t.Fatalf("second delete=%d", res.Code)
	}

	mode.Store(0)
	body := deliveryBody()
	body["account"] = "linked@example.test"
	body["auth_json"].(map[string]any)["email"] = "linked@example.test"
	w = onboardRequest(a, body, "to-keep")
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	linked := responseData(t, w)["account_id"].(string)
	a.runOne(context.Background())
	if deliveryAccount(t, a)["onboarding_state"] != "linked" {
		t.Fatal("fixture did not link")
	}
	if res := deleteRequest(a, linked); res.Code != 409 || !strings.Contains(res.Body.String(), "ACCOUNT_DELIVERED") {
		t.Fatalf("linked delete=%d %s", res.Code, res.Body.String())
	}
	var n int
	if e := a.DB.QueryRow(`SELECT COUNT(*) FROM accounts WHERE id=?`, linked).Scan(&n); e != nil || n != 1 {
		t.Fatal("linked account was removed")
	}
}

func TestOnboardReportsEveryProblemAtOnce(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	body := deliveryBody()
	delete(body, "password")
	body["plan_end_time"] = "2026-10-01"
	body["auth_json"].(map[string]any)["refresh_token"] = ""
	w := onboardRequest(a, body, "all-at-once")
	var env struct {
		Error struct {
			Code   string `json:"code"`
			Fields []struct {
				Field, Code, Message string
			} `json:"fields"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if w.Code != 422 || env.Error.Code != "VALIDATION_FAILED" || len(env.Error.Fields) != 3 {
		t.Fatal(w.Body.String())
	}
	want := map[string]string{"password": "REQUIRED", "plan_end_time": "OUT_OF_RANGE", "auth_json.refresh_token": "REQUIRED"}
	for _, f := range env.Error.Fields {
		if want[f.Field] != f.Code || f.Message == "" {
			t.Fatalf("%+v", f)
		}
	}
}

func TestPreflightChecksCarryMessagesAndFields(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	w := httptest.NewRecorder()
	a.accounts(w, httptest.NewRequest("POST", "/accounts", strings.NewReader(`{"account":"draft@example.test","plan":"claude/cc-max-20x"}`)))
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	id := responseData(t, w)["id"].(string)
	checks := a.preflight(id, "conn")["checks"].([]map[string]any)
	if len(checks) == 0 {
		t.Fatal("expected failing checks for a draft")
	}
	seenPassword := false
	for _, c := range checks {
		if c["message"] == nil || c["message"] == "" {
			t.Fatalf("check without message: %+v", c)
		}
		if c["code"] == "PASSWORD_REQUIRED" {
			seenPassword = true
			if c["field"] != "password" {
				t.Fatalf("password check lacks field: %+v", c)
			}
		}
	}
	if !seenPassword {
		t.Fatalf("no PASSWORD_REQUIRED among %+v", checks)
	}
}
