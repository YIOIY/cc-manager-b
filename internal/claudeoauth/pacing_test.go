package claudeoauth

import (
	"context"
	"math/rand/v2"
	"sync"
	"testing"
	"time"
)

func TestUniformStagePauseBounds(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	pause := UniformStagePause(50*time.Millisecond, 150*time.Millisecond, rng)
	for i := 0; i < 50; i++ {
		start := time.Now()
		if err := pause(context.Background(), "profile"); err != nil {
			t.Fatalf("pause %d: %v", i, err)
		}
		el := time.Since(start)
		if el < 50*time.Millisecond || el > 160*time.Millisecond {
			t.Fatalf("pause %d = %v, want [50ms,150ms]", i, el)
		}
	}
}

func TestStagePauseRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pause := UniformStagePause(10*time.Second, 10*time.Second, rand.New(rand.NewPCG(3, 4)))
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	err := pause(ctx, "profile")
	if err == nil {
		t.Fatal("want context error")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("cancel waited too long: %v", time.Since(start))
	}
}

func TestZeroSpanPauseImmediate(t *testing.T) {
	pause := UniformStagePause(0, 0, rand.New(rand.NewPCG(5, 6)))
	start := time.Now()
	if err := pause(context.Background(), "roles"); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatalf("zero-span paused: %v", time.Since(start))
	}
}

// CompleteProfile wiring sanity: the pause hook must fire exactly once per
// requested stage and in order. We call sleepStage directly because spinning a
// fake provider here would duplicate reliability_test.go fixtures.
func TestSleepStageNilSafe(t *testing.T) {
	var mu sync.Mutex
	stages := []string{}
	pause := func(_ context.Context, s string) error {
		mu.Lock()
		defer mu.Unlock()
		stages = append(stages, s)
		return nil
	}
	if err := sleepStage(context.Background(), pause, "profile"); err != nil {
		t.Fatal(err)
	}
	if err := sleepStage(context.Background(), pause, "roles"); err != nil {
		t.Fatal(err)
	}
	if len(stages) != 2 || stages[0] != "profile" || stages[1] != "roles" {
		t.Fatalf("stages=%v", stages)
	}
	if err := sleepStage(context.Background(), nil, "noop"); err != nil {
		t.Fatal(err)
	}
}

// Ensures the exported default is wired to the documented production range.
func TestDefaultProductionPauseWithinRange(t *testing.T) {
	start := time.Now()
	if err := DefaultProductionPause(context.Background(), "profile"); err != nil {
		t.Fatal(err)
	}
	el := time.Since(start)
	if el < 200*time.Millisecond || el > 1300*time.Millisecond {
		t.Fatalf("default pause %v outside [200ms,1.2s]+tolerance", el)
	}
}
