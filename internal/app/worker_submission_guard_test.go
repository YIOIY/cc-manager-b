package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkerQueuedArchivedAccountMustNotSubmit(t *testing.T) {
	a, calls, _, _ := deliveryFixture(t)
	w := onboardRequest(a, deliveryBody(), "worker-archive")
	if w.Code != http.StatusAccepted {
		t.Fatalf("enqueue: %d %s", w.Code, w.Body.String())
	}
	id := responseData(t, w)["account_id"].(string)
	r := withAdminSession(httptest.NewRequest("POST", "/archive", nil))
	r.SetPathValue("id", id)
	archived := httptest.NewRecorder()
	a.accountArchive(true)(archived, r)
	if archived.Code != http.StatusOK {
		t.Fatalf("archive: %d %s", archived.Code, archived.Body.String())
	}

	a.runOne(context.Background())
	job := queryString(t, a, `SELECT state FROM jobs WHERE account_id=?`, id)
	delivery := queryString(t, a, `SELECT ingestion_state FROM deliveries WHERE account_id=?`, id)
	code := queryString(t, a, `SELECT json_extract(result_json,'$.code') FROM jobs WHERE account_id=?`, id)
	t.Logf("supplier POSTs=%d; job=%s; delivery=%s; code=%s", calls.Load(), job, delivery, code)
	if calls.Load() != 0 || job != "failed" || delivery != "failed" || code != "ARCHIVED" {
		t.Fatal("archived queued account must fail locally without a supplier request")
	}
}

func TestWorkerBusyAccountPreservesDeliveryState(t *testing.T) {
	for _, state := range []string{"queued", "running", "accepted", "unknown"} {
		t.Run(state, func(t *testing.T) {
			a, calls, _, _ := deliveryFixture(t)
			w := onboardRequest(a, deliveryBody(), "worker-busy-"+state)
			if w.Code != http.StatusAccepted {
				t.Fatalf("enqueue: %d %s", w.Code, w.Body.String())
			}
			id := responseData(t, w)["account_id"].(string)
			unlock, err := a.accountLocks.lock(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			a.setDelivery(id, "conn", state, "")

			a.runOne(context.Background())
			job := queryString(t, a, `SELECT state FROM jobs WHERE account_id=?`, id)
			delivery := queryString(t, a, `SELECT ingestion_state FROM deliveries WHERE account_id=?`, id)
			t.Logf("lock held: supplier POSTs=%d; job=%s; delivery=%s", calls.Load(), job, delivery)
			if calls.Load() != 0 || job != "queued" || delivery != state {
				t.Fatalf("busy account changed delivery state: want queued job and %s delivery", state)
			}
			unlock()
			if state != "accepted" && state != "unknown" {
				return
			}
			a.runOne(context.Background())
			job = queryString(t, a, `SELECT state FROM jobs WHERE account_id=?`, id)
			delivery = queryString(t, a, `SELECT ingestion_state FROM deliveries WHERE account_id=?`, id)
			wantJob := "unknown"
			if state == "accepted" {
				wantJob = "succeeded"
			}
			if calls.Load() != 0 || job != wantJob || delivery != state {
				t.Fatalf("supplier POSTs=%d; job=%s; delivery=%s; want job=%s and unchanged delivery", calls.Load(), job, delivery, wantJob)
			}
		})
	}
}

func TestWorkerQueuedAccountUsesFreshCredential(t *testing.T) {
	a, calls, _, _ := deliveryFixture(t, func(body map[string]any) {
		auth, _ := body["auth_json"].(map[string]any)
		if auth["access_token"] != "fixture-fresh-access" {
			t.Errorf("supplier received stale credential: %v", auth["access_token"])
		}
	})
	body := deliveryBody()
	w := onboardRequest(a, body, "worker-fresh-credential")
	if w.Code != http.StatusAccepted {
		t.Fatalf("enqueue: %d %s", w.Code, w.Body.String())
	}
	id := responseData(t, w)["account_id"].(string)
	auth := body["auth_json"].(map[string]any)
	auth["access_token"] = "fixture-fresh-access"
	sealAccountAuth(t, a, id, auth)
	if _, err := a.DB.Exec(`UPDATE accounts SET version=version+1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}

	a.runOne(context.Background())
	job := queryString(t, a, `SELECT state FROM jobs WHERE account_id=?`, id)
	if calls.Load() != 1 || job != "succeeded" {
		t.Fatalf("supplier POSTs=%d; job=%s; fresh credential must remain eligible", calls.Load(), job)
	}
}
