package app

import (
	"math"
	"sync"
	"time"
)

const (
	writeLimiterMaxItems = 10000
	writeLimiterIdle     = 10 * time.Minute
)

// writeLimiter is a per-user token bucket for mutating requests. Login has
// its own backoff; this one stops an authenticated user from hammering the
// supplier through proxy tests, imports or bulk edits. Memory only, single
// process, reset on restart — the same trade-off as the session store.
type writeLimiter struct {
	mu    sync.Mutex
	rate  float64 // tokens per second; 0 disables the limiter
	burst float64
	items map[string]tokenBucket
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// newWriteLimiter allows perMinute sustained writes with a burst of a quarter
// of that (at least 5), so short bursts from a form submit never trip it.
func newWriteLimiter(perMinute int) *writeLimiter {
	l := &writeLimiter{items: map[string]tokenBucket{}}
	if perMinute > 0 {
		l.rate = float64(perMinute) / 60
		l.burst = math.Max(5, float64(perMinute)/4)
	}
	return l
}

// allow consumes one token for key and reports the wait until the next token
// when the bucket is empty.
func (l *writeLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	if l.rate == 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.items) >= writeLimiterMaxItems {
		l.sweepLocked(now)
	}
	b, ok := l.items[key]
	if !ok {
		b = tokenBucket{tokens: l.burst, last: now}
	} else {
		b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
		b.last = now
	}
	if b.tokens < 1 {
		l.items[key] = b
		wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
		return false, wait
	}
	b.tokens--
	l.items[key] = b
	return true, 0
}

func (l *writeLimiter) sweep(now time.Time) {
	l.mu.Lock()
	l.sweepLocked(now)
	l.mu.Unlock()
}

func (l *writeLimiter) sweepLocked(now time.Time) {
	for k, b := range l.items {
		if now.Sub(b.last) > writeLimiterIdle {
			delete(l.items, k)
		}
	}
}
