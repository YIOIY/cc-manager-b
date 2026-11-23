// Package claudeoauth — pacing helpers spread dependent provider requests so a
// freshly exchanged token does not trigger a sub-second profile/roles burst,
// which reads as automation on the Anthropic side. Delays are injectable so
// tests can assert ordering without sleeping.
package claudeoauth

import (
	"context"
	"math/rand/v2"
	"time"
)

// StagePause sleeps before a provider request in CompleteProfile. Returning a
// non-nil error aborts the pause (and therefore the following request).
type StagePause func(ctx context.Context, stage string) error

// noPause is the zero-cost implementation used by tests that exercise the
// pure request logic. Production wires UniformPause.
func noPause(context.Context, string) error { return nil }

// UniformStagePause returns a StagePause that sleeps a uniformly distributed
// duration in [min,max] before each stage. Context cancellation interrupts
// the wait so shutdown or a request teardown is respected.
func UniformStagePause(min, max time.Duration, rng *rand.Rand) StagePause {
	if min < 0 {
		min = 0
	}
	if max < min {
		max = min
	}
	return func(ctx context.Context, stage string) error {
		if max <= 0 {
			return nil
		}
		span := max - min
		d := min
		if span > 0 {
			d += time.Duration(rng.Int64N(int64(span) + 1))
		}
		if d <= 0 {
			return nil
		}
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// DefaultProductionPause mirrors common UI dwell times: a user typically
// spends hundreds of milliseconds between the token grant and opening the
// profile screen, and again between loading profile and viewing roles.
var DefaultProductionPause = func() StagePause {
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(time.Now().UnixMicro())))
	return UniformStagePause(200*time.Millisecond, 1200*time.Millisecond, rng)
}()

// sleepStage invokes pause (falling back to noPause when nil), collapsing
// context cancellation into the flow's usual error propagation.
func sleepStage(ctx context.Context, pause StagePause, stage string) error {
	if pause == nil {
		return nil
	}
	return pause(ctx, stage)
}

// ProfileOption mutates the pacing configuration of CompleteProfile.
type ProfileOption func(*StagePause)

// WithStagePause overrides the default inter-stage pause. Passing nil
// disables pacing entirely.
func WithStagePause(p StagePause) ProfileOption {
	return func(dst *StagePause) { *dst = p }
}
