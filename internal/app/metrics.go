package app

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// httpStats are process counters exposed at /metrics. Only the status class
// and method are labelled, so cardinality stays bounded regardless of paths.
type httpStats struct {
	requests  [5]atomic.Int64 // index = status/100 - 1 (1xx..5xx)
	durations atomic.Int64    // microseconds, summed
	inflight  atomic.Int64
}

func (s *httpStats) observe(status int, d time.Duration) {
	idx := status/100 - 1
	if idx < 0 || idx >= len(s.requests) {
		idx = 4
	}
	s.requests[idx].Add(1)
	s.durations.Add(d.Microseconds())
}

// metrics renders Prometheus text exposition. It is unauthenticated like
// /healthz: the service listens on loopback and nothing here is sensitive,
// only counts. Database gauges are read on each scrape.
func (a *App) metrics(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	a.writeOAuthMetrics(&b)
	emit := func(name, help, kind string, rows ...string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
		for _, row := range rows {
			b.WriteString(row)
			b.WriteByte('\n')
		}
	}
	var total int64
	classes := make([]string, 0, 5)
	for i := range a.http.requests {
		n := a.http.requests[i].Load()
		total += n
		classes = append(classes, fmt.Sprintf(`cc_manager_http_requests_total{class="%dxx"} %d`, i+1, n))
	}
	emit("cc_manager_http_requests_total", "HTTP responses by status class.", "counter", classes...)
	emit("cc_manager_http_request_duration_seconds_sum", "Total time spent serving HTTP requests.", "counter", fmt.Sprintf("cc_manager_http_request_duration_seconds_sum %.6f", float64(a.http.durations.Load())/1e6))
	emit("cc_manager_http_request_duration_seconds_count", "Number of HTTP requests measured.", "counter", fmt.Sprintf("cc_manager_http_request_duration_seconds_count %d", total))
	emit("cc_manager_http_inflight", "Requests currently being served.", "gauge", fmt.Sprintf("cc_manager_http_inflight %d", a.http.inflight.Load()))
	emit("cc_manager_sessions", "Sessions held in memory (anonymous and authenticated).", "gauge", fmt.Sprintf("cc_manager_sessions %d", a.sessions.len()))
	emit("cc_manager_uptime_seconds", "Seconds since the process started.", "gauge", fmt.Sprintf("cc_manager_uptime_seconds %d", int(time.Since(a.started).Seconds())))
	ticks := []struct {
		name string
		v    *atomic.Int64
	}{{"jobs", &a.ticks.jobs}, {"metrics", &a.ticks.metrics}, {"bills", &a.ticks.bills}, {"schedule", &a.ticks.schedule}, {"janitor", &a.ticks.janitor}, {"web-accounts", &a.ticks.webAccounts}, {"web-bills", &a.ticks.webBills}}
	rows := make([]string, 0, len(ticks))
	for _, t := range ticks {
		rows = append(rows, fmt.Sprintf(`cc_manager_worker_last_tick_seconds{worker="%s"} %d`, t.name, t.v.Load()))
	}
	emit("cc_manager_worker_last_tick_seconds", "Unix time of each background worker's last loop (0 = never).", "gauge", rows...)
	a.gaugeRows(&b, "cc_manager_jobs", "Jobs by state.", `SELECT state,COUNT(*) FROM jobs GROUP BY state`, "state")
	a.gaugeRows(&b, "cc_manager_sync_runs", "Sync runs by kind and state.", `SELECT kind||'",state="'||state,COUNT(*) FROM sync_runs GROUP BY kind,state`, "kind")
	a.gaugeRows(&b, "cc_manager_accounts", "Accounts by archive flag.", `SELECT CASE archived WHEN 1 THEN 'true' ELSE 'false' END,COUNT(*) FROM accounts GROUP BY archived`, "archived")
	a.gaugeRows(&b, "cc_manager_proxies", "Proxies by enabled flag.", `SELECT CASE enabled WHEN 1 THEN 'true' ELSE 'false' END,COUNT(*) FROM proxies GROUP BY enabled`, "enabled")
	a.gaugeRows(&b, "cc_manager_users", "Users by enabled flag.", `SELECT CASE enabled WHEN 1 THEN 'true' ELSE 'false' END,COUNT(*) FROM users GROUP BY enabled`, "enabled")
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(b.String()))
}

// gaugeRows writes one gauge family from a two-column (label, count) query.
// The label value is embedded verbatim; callers only pass enum-like columns.
func (a *App) gaugeRows(b *strings.Builder, name, help, query, label string) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
	rows, err := a.DB.Query(query)
	if err != nil {
		fmt.Fprintf(b, "# query failed: %s\n", strings.ReplaceAll(err.Error(), "\n", " "))
		return
	}
	defer rows.Close()
	lines := []string{}
	for rows.Next() {
		var value string
		var n int64
		if err := rows.Scan(&value, &n); err != nil {
			break
		}
		lines = append(lines, fmt.Sprintf(`%s{%s="%s"} %d`, name, label, value, n))
	}
	sort.Strings(lines)
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
}
