package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type oauthSettingsEnvelope struct {
	Data  map[string]any `json:"data"`
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func TestOAuthDispatchSettingsValidationVersionAndHotUpdate(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "oauth-settings-password")
	a, err := newApp(t.TempDir()+"/settings.db", false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c := newTestClient(t, a)
	c.login("oauth-settings-password")

	get := func() oauthSettingsEnvelope {
		res := c.do(http.MethodGet, "/api/v1/oauth-settings", "")
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET status=%d", res.StatusCode)
		}
		var env oauthSettingsEnvelope
		b, _ := io.ReadAll(res.Body)
		if err := json.Unmarshal(b, &env); err != nil {
			t.Fatal(err)
		}
		return env
	}
	original := get()
	version := int(original.Data["version"].(float64))

	put := func(status int, body string, code string) {
		res := c.do(http.MethodPut, "/api/v1/oauth-settings", body)
		defer res.Body.Close()
		if res.StatusCode != status {
			b2, _ := io.ReadAll(res.Body)
			t.Fatalf("PUT status=%d want=%d body=%s", res.StatusCode, status, string(b2))
		}
		b, _ := io.ReadAll(res.Body)
		if code != "" && !strings.Contains(string(b), code) {
			t.Fatalf("PUT body lacks %s: %s", code, string(b))
		}
	}
	put(http.StatusUnprocessableEntity, `{"global_concurrency":0,"exit_concurrency":2,"exchange_interval_seconds":3,"probe_cache_seconds":2,"manual_preheat_seconds_min":1,"manual_preheat_seconds_max":3,"batch_preheat_seconds_min":5,"batch_preheat_seconds_max":15,"version":`+itoa(version)+`}`, "OUT_OF_RANGE")
	// this invalid PUT should not increment the stored version
	put(http.StatusUnprocessableEntity, `{"global_concurrency":3,"exit_concurrency":2,"exchange_interval_seconds":3,"probe_cache_seconds":2,"manual_preheat_seconds_min":2,"manual_preheat_seconds_max":1,"batch_preheat_seconds_min":5,"batch_preheat_seconds_max":15,"version":`+itoa(version)+`}`, "上限不能小于下限")

	// this invalid PUT should not increment the stored version either

	latest := get()
	latestVersion := int(latest.Data["version"].(float64))
	put(http.StatusOK, `{"global_concurrency":5,"exit_concurrency":2,"exchange_interval_seconds":4,"probe_cache_seconds":10,"manual_preheat_seconds_min":1,"manual_preheat_seconds_max":2,"batch_preheat_seconds_min":5,"batch_preheat_seconds_max":9,"version":`+itoa(latestVersion)+`}`, "")

	// saved version incremented by the PUT above; verify in the get below
	saved := get()
	if saved.Data["global_concurrency"].(float64) != 5 || saved.Data["probe_cache_seconds"].(float64) != 10 || int(saved.Data["version"].(float64)) != version+1 {
		t.Fatalf("saved settings mismatch: %#v", saved.Data)
	}
	if got := cap(a.oauthGate.Load().slots); got != 5 {
		t.Fatalf("global gate not hot-updated: %d", got)
	}
	if gate := a.oauthExitGate.Load(); gate == nil || gate.size != 2 {
		t.Fatalf("exit gate not hot-updated")
	}
}

func itoa(v int) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestOAuthPreheatPayloadSurvivesRestart(t *testing.T) {
	payload := `{"connection_id":"conn","version":3}`
	stored, err := persistOAuthPreheat(payload, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored, `"not_before_at":"2023-11-14T22:13:20Z"`) {
		t.Fatalf("preheat timestamp missing or wrong: %s", stored)
	}
	var job submitJob
	if err := json.Unmarshal([]byte(stored), &job); err != nil {
		t.Fatal(err)
	}
	if job.ConnectionID != "conn" || job.Version != 3 {
		t.Fatalf("preheat payload lost submit data: %#v", job)
	}
	if d := oauthPreheatRemaining(stored, time.Unix(1699999900, 0)); d != 100*time.Second {
		t.Fatalf("remaining=%v want 100s", d)
	}
	if d := oauthPreheatRemaining(stored, time.Unix(1700000000, 0)); d != 0 {
		t.Fatalf("expired delay=%v", d)
	}
}

func TestExitExchangePacingSpacesCalls(t *testing.T) {
	pacing := &exitExchangePacing{last: map[string]time.Time{}}
	release, err := pacing.wait(context.Background(), "exit-a", 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	release()
	start := time.Now()
	release, err = pacing.wait(context.Background(), "exit-a", 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if d := time.Since(start); d < 15*time.Millisecond {
		t.Fatalf("second exchange returned too early: %v", d)
	}
}

// Saved concurrency limits must survive a restart: an operator who lowered the
// global gate to protect the provider would otherwise silently run at the
// built-in size until someone re-saves the settings page.
func TestOAuthDispatchSettingsAppliedAtStartup(t *testing.T) {
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "oauth-settings-password")
	path := t.TempDir() + "/restart.db"
	a, err := newApp(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`UPDATE oauth_dispatch_settings SET global_concurrency=1, exit_concurrency=4, version=version+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := newApp(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if got := cap(restarted.oauthGate.Load().slots); got != 1 {
		t.Fatalf("global gate after restart = %d, want 1", got)
	}
	if gate := restarted.oauthExitGate.Load(); gate == nil || gate.size != 4 {
		t.Fatalf("exit gate after restart = %+v, want size 4", gate)
	}
}
