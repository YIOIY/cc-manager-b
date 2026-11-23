package app

import (
	"sync"
	"time"
)

// oauthProbeEntry stores the last successful reachability result for a network
// fingerprint. Only successful probes are cached; failures keep retrying.
type oauthProbeEntry struct {
	expires time.Time
}

// oauthProbeCache is process-local and bounded by the configured TTL. Zero
// TTL disables caching. Concurrent starts from different owners read and
// write it at the same time, so every access takes the mutex.
type oauthProbeCache struct {
	mu      sync.Mutex
	entries map[[32]byte]oauthProbeEntry
}

func (c *oauthProbeCache) remaining(fp [32]byte, now time.Time) time.Duration {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[fp]
	if !ok || !now.Before(e.expires) {
		return 0
	}
	return e.expires.Sub(now)
}

func (c *oauthProbeCache) store(fp [32]byte, now time.Time, ttl time.Duration) {
	if c == nil || ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[[32]byte]oauthProbeEntry{}
	}
	c.entries[fp] = oauthProbeEntry{expires: now.Add(ttl)}
}
