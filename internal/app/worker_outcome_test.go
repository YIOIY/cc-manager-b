package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"cc-manager/internal/errs"
	"cc-manager/internal/supplier"
)

func TestRejectedSubmissionStoresStructuredOutcome(t *testing.T) {
	a, _, _, mode := deliveryFixture(t)
	mode.Store(1)
	w := onboardRequest(a, deliveryBody(), "structured-outcome")
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	id := responseData(t, w)["account_id"].(string)
	a.runOne(context.Background())
	var result string
	if e := a.DB.QueryRow(`SELECT result_json FROM jobs WHERE account_id=?`, id).Scan(&result); e != nil {
		t.Fatal(e)
	}
	var payload struct {
		Code      string         `json:"code"`
		Message   string         `json:"message"`
		Action    string         `json:"action"`
		Retryable bool           `json:"retryable"`
		Details   map[string]any `json:"details"`
	}
	if e := json.Unmarshal([]byte(result), &payload); e != nil {
		t.Fatal(e, result)
	}
	if payload.Code != "PLATFORM_REJECTED" || !payload.Retryable || payload.Action != "fix_input" {
		t.Fatalf("payload=%+v", payload)
	}
	if fmt.Sprint(payload.Details["upstream_status"]) != "422" {
		t.Fatalf("details=%+v", payload.Details)
	}
	if !strings.Contains(payload.Message, "平台返回") || strings.Contains(result, "fixture-password") {
		t.Fatalf("message=%q", payload.Message)
	}
	job := deliveryAccount(t, a)["submission_job"].(map[string]any)
	if job["message"] != payload.Message || job["action"] != "fix_input" || job["retryable"] != true {
		t.Fatalf("job=%+v", job)
	}
	actions, _ := job["allowed_actions"].([]any)
	if len(actions) != 1 || actions[0] != "retry" {
		t.Fatalf("allowed_actions=%+v", job["allowed_actions"])
	}
}

func TestLegacyResultJsonGainsMessageOnRead(t *testing.T) {
	a, _, _, mode := deliveryFixture(t)
	mode.Store(1)
	w := onboardRequest(a, deliveryBody(), "legacy-result")
	id := responseData(t, w)["account_id"].(string)
	a.runOne(context.Background())
	if _, e := a.DB.Exec(`UPDATE jobs SET result_json='{"code":"PLATFORM_REJECTED"}' WHERE account_id=?`, id); e != nil {
		t.Fatal(e)
	}
	job := deliveryAccount(t, a)["submission_job"].(map[string]any)
	def, _ := errs.Lookup("PLATFORM_REJECTED")
	if job["message"] != def.Message {
		t.Fatalf("legacy message=%q want %q", job["message"], def.Message)
	}
}

func TestOutcomeOfClassifiesUpstreamErrorsAndSanitizes(t *testing.T) {
	rejected := supplier.NewSubmitError("rejected", 422, "detail Bearer sk-ant-oat01-"+strings.Repeat("x", 60))
	e := outcomeOf(fmt.Errorf("wrap: %w", rejected))
	if e.Code() != "PLATFORM_REJECTED" || e.Details()["upstream_status"] != 422 {
		t.Fatalf("code=%s details=%+v", e.Code(), e.Details())
	}
	if strings.Contains(e.Message(), "sk-ant") || strings.Contains(e.Message(), "Bearer") {
		t.Fatalf("leaked: %q", e.Message())
	}
	auth := supplier.NewSubmitError("rejected", 403, "forbidden")
	if outcomeOf(auth).Code() != "PLATFORM_AUTH_FAILED" {
		t.Fatalf("auth code=%s", outcomeOf(auth).Code())
	}
	if outcomeOf(supplier.NewSubmitError("unknown", 502, "")).Code() != "PLATFORM_UNAVAILABLE" {
		t.Fatal("5xx must classify as PLATFORM_UNAVAILABLE")
	}
	if outcomeOf(supplier.NewSubmitError("unknown", 0, "connection reset")).Code() != "SUBMIT_OUTCOME_UNKNOWN" {
		t.Fatal("network failure must stay SUBMIT_OUTCOME_UNKNOWN")
	}
	if outcomeOf(fmt.Errorf("plain")).Code() != "SUBMIT_OUTCOME_UNKNOWN" {
		t.Fatal("plain error must stay unknown")
	}
}
