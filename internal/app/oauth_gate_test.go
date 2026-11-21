package app

import (
	"context"
	"testing"
	"time"
)

// A full gate must hand the caller OAUTH_BUSY instead of waiting forever, so a
// burst cannot pile up unbounded POSTs behind the provider.
func TestOAuthGateReportsBusyAfterWait(t *testing.T) {
	g := newOAuthGate(1)
	release, err := g.acquire(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	start := time.Now()
	if _, err := g.acquire(context.Background(), 50*time.Millisecond); err != errOAuthGateBusy {
		t.Fatalf("err=%v", err)
	}
	if waited := time.Since(start); waited < 40*time.Millisecond {
		t.Fatalf("returned before the wait elapsed: %v", waited)
	}
}

// Releasing twice must not free a second slot, otherwise the bound silently
// widens every time a handler double-releases.
func TestOAuthGateReleaseIsIdempotent(t *testing.T) {
	g := newOAuthGate(1)
	release, err := g.acquire(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	second, err := g.acquire(context.Background(), 50*time.Millisecond)
	if err != nil {
		t.Fatalf("second slot: %v", err)
	}
	if _, err := g.acquire(context.Background(), 20*time.Millisecond); err != errOAuthGateBusy {
		t.Fatalf("double release widened the gate: %v", err)
	}
	second()
}

// A nil gate keeps handlers working in tests that build the App by hand.
func TestOAuthGateNilIsNoop(t *testing.T) {
	var g *oauthGate
	release, err := g.acquire(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestAccountLocksTryLockRefusesBusyAccountAndReleases(t *testing.T) {
	var l accountLocks
	release, ok := l.tryLock("acc")
	if !ok {
		t.Fatal("first tryLock refused a free account")
	}
	if _, ok := l.tryLock("acc"); ok {
		t.Fatal("tryLock took a held account")
	}
	other, ok := l.tryLock("other")
	if !ok {
		t.Fatal("tryLock refused an unrelated account")
	}
	release()
	again, ok := l.tryLock("acc")
	if !ok {
		t.Fatal("account stayed locked after release")
	}
	again()
	other()
	// The map must not keep entries for accounts nobody holds.
	if len(l.items) != 0 {
		t.Fatalf("leaked lock entries: %d", len(l.items))
	}
}

func TestAccountLocksLockWaitsAndHonoursContext(t *testing.T) {
	var l accountLocks
	release, err := l.lock(context.Background(), "acc")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := l.lock(ctx, "acc"); err == nil {
		t.Fatal("lock ignored a held account")
	}
	release()
	second, err := l.lock(context.Background(), "acc")
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	second()
}
