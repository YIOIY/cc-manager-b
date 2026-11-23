package app

import (
	"context"
	"sync"
	"time"
)

// exitGate bounds how many token exchanges share one resolved proxy exit. It
// complements the global OAuth gate and prevents a single high-throughput
// proxy from absorbing the whole process burst.
type exitGate struct {
	mu    sync.Mutex
	slots map[string]chan struct{}
	size  int
	wait  time.Duration
}

func newExitGate(size int, wait time.Duration) *exitGate {
	if size < 1 {
		size = 1
	}
	if wait <= 0 {
		wait = 30 * time.Second
	}
	return &exitGate{slots: map[string]chan struct{}{}, size: size, wait: wait}
}

// acquire lazily creates a per-exit queue. The release func is safe to call
// twice so callers can pair it with defer plus an early return.
func (g *exitGate) acquire(ctx context.Context, key string) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	g.mu.Lock()
	slot, ok := g.slots[key]
	if !ok {
		slot = make(chan struct{}, g.size)
		g.slots[key] = slot
	}
	g.mu.Unlock()
	timer := time.NewTimer(g.wait)
	defer timer.Stop()
	select {
	case slot <- struct{}{}:
		var once sync.Once
		release := func() { once.Do(func() { <-slot }) }
		return release, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, errOAuthGateBusy
	}
}

// applyOAuthExitGate hot-swaps the per-exit gate after settings change.
func (a *App) applyOAuthExitGate(size int) {
	if size < 1 {
		size = 1
	}
	a.oauthExitGate = newExitGate(size, oauthGateWait)
}

// exitExchangePacing spaces token exchanges through one exit. The gate above
// limits simultaneity; this prevents back-to-back calls when each operation
// finishes quickly.
type exitExchangePacing struct {
	mu   sync.Mutex
	last map[string]time.Time
}

var oauthExitPacing = &exitExchangePacing{last: map[string]time.Time{}}

func (p *exitExchangePacing) wait(ctx context.Context, key string, interval time.Duration) (release func(), err error) {
	if p == nil || interval <= 0 {
		return func() {}, nil
	}
	p.mu.Lock()
	next := p.last[key].Add(interval)
	now := time.Now()
	if next.Before(now) {
		next = now
	}
	wait := next.Sub(now)
	p.last[key] = next
	p.mu.Unlock()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return func() {}, nil
	case <-ctx.Done():
		p.mu.Lock()
		if t, ok := p.last[key]; ok && t.Equal(next) {
			p.last[key] = next.Add(-wait)
		}
		p.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (g *exitGate) release() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.slots = map[string]chan struct{}{}
}
