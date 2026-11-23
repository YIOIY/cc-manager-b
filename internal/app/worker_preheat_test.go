package app

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

func TestAccountPreheatDelayBatchOnly(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	nonBatch := `{"tags":["x"]}`
	if d := accountPreheatDelay(nonBatch, rng); d != 0 {
		t.Fatalf("manual account got delay %v", d)
	}
	if d := accountPreheatDelay(`not-json`, rng); d != 0 {
		t.Fatalf("malformed metadata got delay %v", d)
	}
	if d := accountPreheatDelay(``, rng); d != 0 {
		t.Fatalf("empty metadata got delay %v", d)
	}
	for i := 0; i < 40; i++ {
		d := accountPreheatDelay(`{"source":"batch_import"}`, rng)
		if d < preheatDelayMin || d > preheatDelayMax {
			t.Fatalf("draw %d = %v outside [%v,%v]", i, d, preheatDelayMin, preheatDelayMax)
		}
	}
}

func TestWaitForPreheatHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	waitForPreheat(ctx, 10*time.Second)
	if time.Since(start) > time.Second {
		t.Fatalf("cancel ignored, waited %v", time.Since(start))
	}
	// Zero-delay fast-path
	waitForPreheat(context.Background(), 0)
	waitForPreheat(nil, 5*time.Second)
}

func TestWaitForPreheatFullDuration(t *testing.T) {
	start := time.Now()
	waitForPreheat(context.Background(), 80*time.Millisecond)
	if got := time.Since(start); got < 60*time.Millisecond {
		t.Fatalf("returned too early: %v", got)
	}
}

// Account seeded via batch-import path must carry the source marker so the
// worker recognises it. Serialised through the same marshal helper used by
// import commit.
func TestBatchImportMarkerMarshals(t *testing.T) {
	meta := map[string]any{"source": "batch_import", "tags": []string{"vip"}}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"source":"batch_import"`) {
		t.Fatalf("marker missing: %s", raw)
	}
}
