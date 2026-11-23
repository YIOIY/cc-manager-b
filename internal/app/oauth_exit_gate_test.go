package app

import (
	"context"
	"testing"
	"time"
)

// A queued caller that gives up must hand its slot back without moving the
// schedule: the slot before it belongs to an exchange that is still ahead, and
// the next arrival has to stay a full interval behind that one. Rolling back
// to the canceller's own arrival time (the old arithmetic) let the next caller
// land inside the interval.
func TestExitExchangePacingCancelRestoresPreviousSlot(t *testing.T) {
	const interval = time.Hour
	base := time.Now()
	slot := func(p *exitExchangePacing, key string) (time.Time, bool) {
		p.mu.Lock()
		defer p.mu.Unlock()
		v, ok := p.last[key]
		return v, ok
	}
	// Reserve the next slot in the background, then abandon the wait.
	reserveThenCancel := func(p *exitExchangePacing, key string, between func()) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := p.wait(ctx, key, interval)
			done <- err
		}()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if v, ok := slot(p, key); ok && v.After(base) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("reservation never appeared")
			}
			time.Sleep(time.Millisecond)
		}
		between()
		cancel()
		if err := <-done; err == nil {
			t.Fatal("cancelled wait returned no error")
		}
	}

	pacing := &exitExchangePacing{last: map[string]time.Time{"exit": base}}
	reserveThenCancel(pacing, "exit", func() {})
	if restored, _ := slot(pacing, "exit"); !restored.Equal(base) {
		t.Fatalf("slot after cancel = %v, want the previous slot %v", restored, base)
	}

	// A slot reserved after the canceller must not be rolled back by it.
	later := base.Add(5 * interval)
	reserveThenCancel(pacing, "exit", func() {
		pacing.mu.Lock()
		pacing.last["exit"] = later
		pacing.mu.Unlock()
	})
	if kept, _ := slot(pacing, "exit"); !kept.Equal(later) {
		t.Fatalf("later reservation disturbed: %v", kept)
	}

	// An already-cancelled caller never reserves anything.
	fresh := &exitExchangePacing{last: map[string]time.Time{}}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fresh.wait(cancelled, "new-exit", interval); err == nil {
		t.Fatal("cancelled context accepted")
	}
	if _, exists := slot(fresh, "new-exit"); exists {
		t.Fatal("cancelled first wait left a reservation behind")
	}
}
