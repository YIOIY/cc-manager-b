package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOAuthLifecycleAtomicStart(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	var probes atomic.Int32
	a.probeOAuthReach = func(context.Context, *http.Client) error {
		probes.Add(1)
		time.Sleep(15 * time.Millisecond)
		return nil
	}
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := retryStart(a, `{}`)
			var env struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			json.Unmarshal(w.Body.Bytes(), &env)
			ids <- env.Data.ID
		}()
	}
	wg.Wait()
	close(ids)
	id := ""
	for got := range ids {
		if got == "" {
			t.Fatal("start failed")
		}
		if id == "" {
			id = got
		}
		if got != id {
			t.Fatal("concurrent start replaced a live link")
		}
	}
	// Direct (no-proxy) starts skip the upfront reachability gate; the intent
	// here is the atomic "single live link per owner" guarantee, not the probe.
	if probes.Load() != 0 {
		t.Fatalf("probes=%d want 0 (direct start skips the gate)", probes.Load())
	}
}
func TestOAuthLifecycleStartFreezesWritesUntilCancel(t *testing.T) {
	a, _, c := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	saved := reauthAuth("one@example.test")
	saved["proxy_url"] = "http://proxy.example:8080"
	sealAccountAuth(t, a, id, saved)
	res := c.do("POST", "/api/v1/claude-oauth/sessions", `{"account_id":"`+id+`"}`)
	var env struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.NewDecoder(res.Body).Decode(&env)
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatalf("start=%d", res.StatusCode)
	}
	if release, ok := a.accountLocks.tryLock(id); ok {
		release()
		t.Fatal("OAuth did not hold the worker account lock")
	}
	res = c.do("PATCH", "/api/v1/accounts/"+id, `{"plan":"claude/cc-pro"}`)
	body := readBody(res)
	if res.StatusCode != 409 || !strings.Contains(body, "ACCOUNT_IN_PROGRESS") {
		t.Fatalf("write not frozen: %d %s", res.StatusCode, body)
	}
	res = c.do("DELETE", "/api/v1/claude-oauth/sessions/"+env.Data.ID, "")
	res.Body.Close()
	release, ok := a.accountLocks.tryLock(id)
	if !ok {
		t.Fatal("cancel leaked account lease")
	}
	release()
}
func TestOAuthLifecycleFullRetryAfter(t *testing.T) {
	var cd oauthAccountCooldown
	got := cd.extend("account", time.Now(), 5*time.Hour)
	if got != 5*time.Hour {
		t.Fatalf("upstream Retry-After truncated to %v", got)
	}
}

// Cooldown expiry jitter (oauthExitJitter*) must add a bounded extra wait on
// the read side without altering stored deadlines or keep-later semantics.
func TestOAuthRemainingAppliesExitJitter(t *testing.T) {
	app, _, _ := routesFixture(t)
	p := oauthPending{accountID: "acct-jit"}

	base := 2 * time.Minute
	app.extendOAuthCooldown(p, base)
	first := app.oauthRemaining(p)
	if first < base || first > base+oauthExitJitterCap {
		t.Fatalf("remaining %v outside [%v,%v+jittercap]", first, base, base)
	}

	// Spread across many draws: some jitter must occur but never negative.
	prev := first
	draws := 0
	for i := 0; i < 40; i++ {
		// Re-arm each round so we sample fresh jitter values.
		app.oauthCooldown.until["acct:jit"] = time.Now().Add(base)
		got := app.oauthRemaining(p)
		if got < base {
			t.Fatalf("draw %d under baseline: %v", i, got)
		}
		if got != prev {
			draws++
		}
		prev = got
	}
	if draws == 0 {
		t.Fatal("jitter never varied; RNG path dead?")
	}
}
