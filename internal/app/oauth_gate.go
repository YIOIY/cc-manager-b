package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// oauthGate bounds how many token exchanges may be in flight at once. One user
// retrying a login page 404, or ten users arriving together, otherwise turn
// into a burst of POSTs from the same machine to the same provider endpoint;
// the burst is what invites platform-level rate limiting.
//
// The gate is deliberately per process and in-memory: multi-instance
// deployments still burst, but a single instance no longer multiplies load.
const oauthGateSize = 3

var errOAuthGateBusy = errors.New("oauth gate busy")

type oauthGate struct {
	slots chan struct{}
	once  sync.Once
}

// oauthGatePointer lets saved settings change the gate size without restarting.
// Tests can replace this with a narrow one without touching every call site.
type oauthGatePointer = *atomic.Pointer[oauthGate]

func newOAuthGate(size int) *oauthGate {
	if size < 1 {
		size = 1
	}
	return &oauthGate{slots: make(chan struct{}, size)}
}

// acquire waits for a slot. The returned func releases it and is safe to call
// twice, so handlers can pair it with defer plus an early release.
func (g *oauthGate) acquire(ctx context.Context, wait time.Duration) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	if wait <= 0 {
		wait = 30 * time.Second
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case g.slots <- struct{}{}:
		var released bool
		var mu sync.Mutex
		return func() {
			mu.Lock()
			done := released
			released = true
			mu.Unlock()
			if !done {
				<-g.slots
			}
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, errOAuthGateBusy
	}
}

func newOAuthGateAtomic(size int) oauthGatePointer {
	p := &atomic.Pointer[oauthGate]{}
	p.Store(newOAuthGate(size))
	return p
}
