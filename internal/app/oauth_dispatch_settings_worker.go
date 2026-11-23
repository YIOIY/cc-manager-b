package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"time"
)

// oauthPreheatPayload is stored inside the existing jobs payload JSON. It is
// an additive key and never replaces the submit payload.
type oauthPreheatPayload struct {
	submitJob
	NotBefore string `json:"not_before_at,omitempty"`
}

// computeOAuthPreheat returns the preheat delay for a job. Manual single
// accounts wait by default too; a zero-value config keeps this off when an
// operator explicitly wants instant processing.
func (a *App) computeOAuthPreheat(metaRaw string, rng *rand.Rand) time.Duration {
	s, err := a.loadOAuthDispatchSettings()
	if err != nil {
		return 0
	}
	minimum, maximum := s.ManualPreheatSecondsMin, s.ManualPreheatSecondsMax
	if accountPreheatMetaSource(metaRaw) == "batch_import" {
		minimum, maximum = s.BatchPreheatSecondsMin, s.BatchPreheatSecondsMax
	}
	if minimum <= 0 || maximum < minimum {
		return 0
	}
	span := maximum - minimum
	seconds := minimum
	if span > 0 {
		seconds += rng.IntN(span + 1)
	}
	return time.Duration(seconds) * time.Second
}

// accountPreheatMetaSource extracts only the source field used to choose the
// preheat range. The payload itself is the authority for the actual wait.
func accountPreheatMetaSource(metaRaw string) string {
	var meta struct {
		Source string `json:"source"`
	}
	_ = json.Unmarshal([]byte(metaRaw), &meta)
	return meta.Source
}

// persistOAuthPreheat rewrites only the additive not_before_at key so queued
// work keeps its connection and submit version after a restart.
func persistOAuthPreheat(payload string, notBefore time.Time) (string, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return "", err
	}
	m["not_before_at"] = notBefore.UTC().Format(time.RFC3339)
	out, err := json.Marshal(m)
	return string(out), err
}

// oauthPreheatRemaining reads the stored delay. It is additive to job payload
// so existing callers and schema stay unchanged.
func oauthPreheatRemaining(payload string, now time.Time) time.Duration {
	var p map[string]any
	if json.Unmarshal([]byte(payload), &p) != nil {
		return 0
	}
	v, _ := p["not_before_at"].(string)
	if v == "" {
		return 0
	}
	at, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return 0
	}
	if !now.Before(at) {
		return 0
	}
	return at.Sub(now)
}

// waitOAuthPreheat sleeps until the saved not-before time. Context cancel
// triggers requeue by the caller so restarts do not lose work.
func (a *App) waitOAuthPreheat(ctx context.Context, accountID, jobID, payload string) {
	remaining := oauthPreheatRemaining(payload, time.Now())
	if remaining <= 0 {
		return
	}
	slog.Info("oauth preheat waiting before first submit", "account", accountID, "job", jobID, "delay", remaining.String())
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}
