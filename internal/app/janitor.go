package app

import (
	"context"
	"log/slog"
	"time"
)

// Retention windows for rows that only have operational value for a while.
// Bills, links, accounts and audit events are never pruned automatically.
const (
	importRetention        = 24 * time.Hour
	idempotencyRetention   = 7 * 24 * time.Hour
	usageSyncRunRetention  = 7 * 24 * time.Hour
	webUsageRetention      = 90 * 24 * time.Hour
	proxyChecksPerProxy    = 100
	janitorInterval        = 15 * time.Minute
	sessionSweeperInterval = time.Minute
)

func (a *App) startJanitor(ctx context.Context) {
	a.wg.Add(2)
	go func() {
		defer a.wg.Done()
		a.loop(ctx, sessionSweeperInterval, safeTick("sessions", func() {
			now := time.Now()
			a.sessions.sweep(now)
			a.logins.sweep(now)
			a.writes.sweep(now)
			a.oauth.sweep(now)
		}))
	}()
	go func() {
		defer a.wg.Done()
		a.loop(ctx, janitorInterval, safeTick("janitor", func() {
			if err := a.cleanup(ctx); err != nil {
				slog.Warn("janitor failed", "error", err)
			}
			a.ticks.janitor.Store(time.Now().Unix())
		}))
	}()
}

// loop runs fn once immediately after a short delay, then on every tick,
// until the context is cancelled.
func (a *App) loop(ctx context.Context, every time.Duration, fn func()) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn()
		}
	}
}

// safeTick wraps a background tick so a panic is logged instead of taking
// down the HTTP server with the shared process.
func safeTick(name string, fn func()) func() {
	return func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("background tick panicked", "worker", name, "panic", r)
			}
		}()
		fn()
	}
}

func (a *App) cleanup(ctx context.Context) error {
	cutoff := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339) }
	statements := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM imports WHERE expires_at<?`, []any{cutoff(importRetention)}},
		{`DELETE FROM idempotency_keys WHERE created_at<?`, []any{cutoff(idempotencyRetention)}},
		{`DELETE FROM sync_runs WHERE kind IN ('platform-usage','web-accounts','web-bills') AND started_at<?`, []any{cutoff(usageSyncRunRetention)}},
		{`DELETE FROM platform_usage_snapshots WHERE observed_at<?`, []any{cutoff(webUsageRetention)}},
		{`DELETE FROM proxy_checks WHERE id IN (SELECT id FROM (SELECT id,ROW_NUMBER() OVER (PARTITION BY proxy_id ORDER BY checked_at DESC,rowid DESC) AS n FROM proxy_checks) WHERE n>?)`, []any{proxyChecksPerProxy}},
		{`DELETE FROM proxy_check_details WHERE check_id NOT IN (SELECT id FROM proxy_checks)`, nil},
	}
	for _, s := range statements {
		if _, err := a.DB.ExecContext(ctx, s.query, s.args...); err != nil {
			return err
		}
	}
	return nil
}
