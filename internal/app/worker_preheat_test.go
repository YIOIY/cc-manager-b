package app

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"
)

func setPreheat(t *testing.T, a *App, manualMin, manualMax, batchMin, batchMax int) {
	t.Helper()
	if _, err := a.DB.Exec(`UPDATE oauth_dispatch_settings SET manual_preheat_seconds_min=?,manual_preheat_seconds_max=?,batch_preheat_seconds_min=?,batch_preheat_seconds_max=? WHERE id=1`, manualMin, manualMax, batchMin, batchMax); err != nil {
		t.Fatal(err)
	}
}

func TestComputeOAuthPreheatUsesConfiguredRangePerSource(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	rng := rand.New(rand.NewPCG(7, 8))
	setPreheat(t, a, 1, 3, 5, 15)
	for i := 0; i < 40; i++ {
		if d := a.computeOAuthPreheat(`{"tags":["x"]}`, rng); d < time.Second || d > 3*time.Second {
			t.Fatalf("manual draw %d = %v outside [1s,3s]", i, d)
		}
		if d := a.computeOAuthPreheat(`{"source":"batch_import"}`, rng); d < 5*time.Second || d > 15*time.Second {
			t.Fatalf("batch draw %d = %v outside [5s,15s]", i, d)
		}
	}
	// An upper bound of 0 switches the wait off; malformed metadata is manual.
	setPreheat(t, a, 0, 0, 0, 0)
	if d := a.computeOAuthPreheat(`not-json`, rng); d != 0 {
		t.Fatalf("disabled manual preheat still waits %v", d)
	}
	if d := a.computeOAuthPreheat(`{"source":"batch_import"}`, rng); d != 0 {
		t.Fatalf("disabled batch preheat still waits %v", d)
	}
}

func TestOAuthPreheatScheduledCountsPastStamps(t *testing.T) {
	if oauthPreheatScheduled(`{"connection_id":"conn","version":1}`) {
		t.Fatal("fresh payload reported as scheduled")
	}
	past, err := persistOAuthPreheat(`{"connection_id":"conn","version":1}`, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	// The wait is over, but the decision was made: no second delay.
	if !oauthPreheatScheduled(past) || oauthPreheatRemaining(past, time.Now()) != 0 {
		t.Fatalf("expired stamp lost: scheduled=%v remaining=%v", oauthPreheatScheduled(past), oauthPreheatRemaining(past, time.Now()))
	}
}

func TestWaitOAuthPreheatHonoursContext(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	payload, err := persistOAuthPreheat(`{"connection_id":"conn","version":1}`, time.Now().Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	a.waitOAuthPreheat(ctx, "acc", "job", payload)
	if time.Since(start) > time.Second {
		t.Fatalf("cancel ignored, waited %v", time.Since(start))
	}
}

// The preheat wait happens while the job is still queued. A shutdown during
// the wait therefore leaves a queued job (not a running one that the restart
// would mark unknown), and a job whose stamp already passed is not handed a
// second delay when a busy account sends it back to the queue.
func TestPreheatWaitsWhileQueuedAndNeverRepeats(t *testing.T) {
	a, calls, _, _ := deliveryFixture(t)
	setPreheat(t, a, 1, 1, 5, 15)
	w := onboardRequest(a, deliveryBody(), "preheat-queued")
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	id := responseData(t, w)["account_id"].(string)

	// Shutdown mid-wait: the stamp is stored, the job stays queued.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	a.runOne(ctx)
	cancel()
	var state, payload string
	if err := a.DB.QueryRow(`SELECT state,payload_json FROM jobs WHERE account_id=?`, id).Scan(&state, &payload); err != nil {
		t.Fatal(err)
	}
	if state != "queued" || !oauthPreheatScheduled(payload) || calls.Load() != 0 {
		t.Fatalf("interrupted preheat: state=%s payload=%s calls=%d", state, payload, calls.Load())
	}
	if _, err := a.DB.Exec(`UPDATE jobs SET state='unknown' WHERE state='running'`); err != nil {
		t.Fatal(err)
	}
	if got := queryString(t, a, `SELECT state FROM jobs WHERE account_id=?`, id); got != "queued" {
		t.Fatalf("restart marking touched a preheating job: %s", got)
	}

	// Once the stamp has passed, a busy account requeues without a new delay.
	past, err := persistOAuthPreheat(payload, time.Now().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`UPDATE jobs SET payload_json=? WHERE account_id=?`, past, id); err != nil {
		t.Fatal(err)
	}
	unlock, ok := a.accountLocks.tryLock(id)
	if !ok {
		t.Fatal("lock")
	}
	started := time.Now()
	a.runOne(context.Background())
	unlock()
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("requeued job waited again for %v", elapsed.Round(time.Millisecond))
	}
	if err := a.DB.QueryRow(`SELECT state,payload_json FROM jobs WHERE account_id=?`, id).Scan(&state, &payload); err != nil {
		t.Fatal(err)
	}
	if state != "queued" || oauthPreheatRemaining(payload, time.Now()) != 0 {
		t.Fatalf("requeue re-scheduled the preheat: state=%s payload=%s", state, payload)
	}

	// Lock released: the job runs without any further wait and is delivered.
	started = time.Now()
	a.runOne(context.Background())
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("delivery waited again for %v", elapsed.Round(time.Millisecond))
	}
	if calls.Load() != 1 || deliveryAccount(t, a)["onboarding_state"] != "linked" {
		t.Fatalf("job not delivered after preheat: calls=%d", calls.Load())
	}
}
