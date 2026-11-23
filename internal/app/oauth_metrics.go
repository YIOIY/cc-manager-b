package app

import (
	"cc-manager/internal/claudeoauth"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var oauthDurationBuckets = []float64{.05, .1, .25, .5, 1, 2, 5, 10, 20, 40, 75}

type oauthMeasurement struct {
	count, success uint64
	sum            float64
	buckets        [11]uint64
}

// oauthStatCounters tracks process totals used to tune concurrency and
// backoff. They are unbounded counters, not account-identifying data.
type oauthStatCounters struct {
	rateLimited429  atomic.Uint64
	reauthorization atomic.Uint64
}

type oauthStatistics struct {
	mu     sync.Mutex
	values map[string]*oauthMeasurement
}

func (s *oauthStatistics) observe(stage string, status int, d time.Duration, success bool) {
	switch stage {
	case "reach", "token", "profile", "roles", "queue":
	default:
		return
	}
	class := "none"
	if status >= 100 && status < 600 {
		class = fmt.Sprintf("%dxx", status/100)
	}
	key := stage + "|" + class
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = map[string]*oauthMeasurement{}
	}
	v := s.values[key]
	if v == nil {
		v = &oauthMeasurement{}
		s.values[key] = v
	}
	v.count++
	if success {
		v.success++
	}
	v.sum += d.Seconds()
	for i, b := range oauthDurationBuckets {
		if d.Seconds() <= b {
			v.buckets[i]++
		}
	}
}
func (a *App) observeOAuthContext(ctx context.Context, r *http.Request, p oauthPending, id string) context.Context {
	if p.fp != nil {
		ctx = claudeoauth.WithHeaderProfile(ctx, *p.fp)
	}
	return claudeoauth.WithObserver(ctx, func(o claudeoauth.Observation) {
		a.oauth.stats.observe(o.Stage, o.Status, o.Duration, o.Kind == "success")
		slog.Info("oauth request", "session", id, "stage", o.Stage, "status", o.Status, "duration_ms", o.Duration.Milliseconds(), "result", o.Kind)
	})
}

// writeOAuthMetrics renders the OAuth families in Prometheus text format.
// The exposition format requires every sample of one metric family to form a
// single group right after its HELP/TYPE lines, so the three families are
// written one after another rather than interleaved per stage/class key.
func (a *App) writeOAuthMetrics(b *strings.Builder) {
	s := &a.oauth.stats
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.values))
	for k := range s.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	labelsOf := func(k string) string {
		parts := strings.Split(k, "|")
		return fmt.Sprintf(`stage="%s",class="%s"`, parts[0], parts[1])
	}
	b.WriteString("# HELP cc_manager_oauth_requests_total OAuth requests by stage and status class.\n# TYPE cc_manager_oauth_requests_total counter\n")
	for _, k := range keys {
		fmt.Fprintf(b, "cc_manager_oauth_requests_total{%s} %d\n", labelsOf(k), s.values[k].count)
	}
	b.WriteString("# HELP cc_manager_oauth_success_total Successful OAuth requests by stage and status class.\n# TYPE cc_manager_oauth_success_total counter\n")
	for _, k := range keys {
		fmt.Fprintf(b, "cc_manager_oauth_success_total{%s} %d\n", labelsOf(k), s.values[k].success)
	}
	b.WriteString("# HELP cc_manager_oauth_duration_seconds OAuth request duration by stage and status class.\n# TYPE cc_manager_oauth_duration_seconds histogram\n")
	for _, k := range keys {
		labels, v := labelsOf(k), s.values[k]
		for i, upper := range oauthDurationBuckets {
			fmt.Fprintf(b, "cc_manager_oauth_duration_seconds_bucket{%s,le=\"%g\"} %d\n", labels, upper, v.buckets[i])
		}
		fmt.Fprintf(b, "cc_manager_oauth_duration_seconds_bucket{%s,le=\"+Inf\"} %d\ncc_manager_oauth_duration_seconds_sum{%s} %g\ncc_manager_oauth_duration_seconds_count{%s} %d\n", labels, v.count, labels, v.sum, labels, v.count)
	}
	inflight := 0
	if g := a.oauthGate.Load(); g != nil {
		inflight = len(g.slots)
	}
	fmt.Fprintf(b, "# HELP cc_manager_oauth_inflight Token exchanges currently holding a global slot.\n# TYPE cc_manager_oauth_inflight gauge\ncc_manager_oauth_inflight %d\n", inflight)
	a.writeOAuthReliabilityMetrics(b)
}

func (a *App) writeOAuthReliabilityMetrics(b *strings.Builder) {
	fmt.Fprintln(b, "# HELP cc_manager_oauth_rate_limit_total Provider 429 and coalesced reach-busy observations.")
	fmt.Fprintln(b, "# TYPE cc_manager_oauth_rate_limit_total counter")
	fmt.Fprintf(b, "cc_manager_oauth_rate_limit_total %d\n", a.oauthStatCounters.rateLimited429.Load())
	fmt.Fprintln(b, "# HELP cc_manager_oauth_reauthorization_total Linked-account reauthorize submissions.")
	fmt.Fprintln(b, "# TYPE cc_manager_oauth_reauthorization_total counter")
	fmt.Fprintf(b, "cc_manager_oauth_reauthorization_total %d\n", a.oauthStatCounters.reauthorization.Load())
}
