package app

import (
	"context"
	"sync"
	"testing"
	"time"
)

func sampleOAuthSnapshot(id string, owner byte, expires time.Time) OAuthStorageSnapshot {
	return OAuthStorageSnapshot{
		ID:                 id,
		Owner:              [32]byte{owner},
		AccountID:          "acct-" + id,
		ProxyID:            "proxy-1",
		ExpiresAt:          expires,
		TokensJSON:         []byte(`{"refresh":"tok"}`),
		OAuthProviderMetad: map[string]string{"issuer": "https://console.anthropic.com"},
	}
}

func TestOAuthStorageContractDescribesSelf(t *testing.T) {
	var _ OAuthStorageBackend = (*MemoryOAuthBackend)(nil)
	got := (&MemoryOAuthBackend{}).DescribeStorageContract()
	want := "OAuth pending sessions; TTL owned by backend; copy-on-read"
	if got != want {
		t.Fatalf("contract string drifted: got %q want %q", got, want)
	}
}

func TestOAuthStorageLifecycleAndCopies(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	b := NewMemoryOAuthBackend()

	if err := b.Save(ctx, sampleOAuthSnapshot("live", 1, now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	got, err := b.Load(ctx, "live")
	if err != nil || got == nil {
		t.Fatalf("load live: got %+v err %v", got, err)
	}
	origTokens := append([]byte(nil), got.TokensJSON...)
	got.TokensJSON[0] = 'X'
	next, _ := b.Load(ctx, "live")
	if string(next.TokensJSON) != string(origTokens) {
		t.Fatalf("backend returned aliased buffer: %q vs %q", next.TokensJSON, origTokens)
	}

	owned, err := b.Owned(ctx, [32]byte{1}, now)
	if err != nil || len(owned) != 1 || owned[0].ID != "live" {
		t.Fatalf("owned mismatch: %+v err=%v", owned, err)
	}
	strangers, _ := b.KnownByAccount(ctx, "acct-live", [32]byte{1}, now)
	if len(strangers) != 0 {
		t.Fatalf("exclude-owner violated: %+v", strangers)
	}
	others, _ := b.KnownByAccount(ctx, "acct-live", [32]byte{2}, now)
	if len(others) != 1 {
		t.Fatalf("cross-owner adoption lookup broken: %+v", others)
	}

	empty, _ := b.KnownByAccount(ctx, "", [32]byte{}, now)
	if empty != nil {
		t.Fatalf("blank account lookup must short-circuit, got %+v", empty)
	}
	if err := b.Drop(ctx, "nope"); err != nil {
		t.Fatalf("drop missing: %v", err)
	}
	count, err := b.CountLive(ctx, now)
	if err != nil || count != 1 {
		t.Fatalf("count live: got %d err %v", count, err)
	}
}

func TestOAuthStorageExpiryBehavesAsMissing(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	b := NewMemoryOAuthBackend()
	expired := sampleOAuthSnapshot("dead", 3, now.Add(-time.Minute))
	if err := b.Save(ctx, expired); err != nil {
		t.Fatal(err)
	}
	fresh := sampleOAuthSnapshot("alive", 3, now.Add(time.Hour))
	if err := b.Save(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if got, err := b.Load(ctx, "dead"); got != nil || err != nil {
		t.Fatalf("expired load must be missing: %+v err=%v", got, err)
	}
	if n, _ := b.CountLive(ctx, now); n != 1 {
		t.Fatalf("live count after expiry = %d, want 1", n)
	}
	removed, err := b.SweepAll(ctx, now)
	if err != nil || removed != 1 {
		t.Fatalf("sweep removed=%d err=%v", removed, err)
	}
	if n, _ := b.CountLive(ctx, now); n != 1 {
		t.Fatalf("post-sweep count = %d, want 1", n)
	}
}

func TestOAuthStorageConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	b := NewMemoryOAuthBackend()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "race-" + string(rune('a'+i%6)) // deliberate sharing
			_ = b.Save(ctx, sampleOAuthSnapshot(id, byte(i), time.Now().Add(time.Minute)))
			_, _ = b.Load(ctx, id)
			_, _ = b.Owned(ctx, [32]byte{byte(i)}, time.Now())
			_ = b.Drop(ctx, id)
		}(i)
	}
	wg.Wait()
}
