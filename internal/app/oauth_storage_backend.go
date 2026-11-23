// Package app — storage abstraction for OAuth pending sessions. Today the
// pending store lives in memory (see oauthPendingStore) which is correct for
// the single-process deployment. The interface below lets us swap in a
// shared backend (SQLite checkpoint now, Redis later) without rewriting
// handlers, keeping the exact same ownership/TTL/cancel semantics.
//
// Contract:
//   - Values are opaque snapshots, serialised by the caller.
//   - TTL handling is the backend's responsibility; Expired entries behave
//     as Missing to callers.
//   - Load/Owned/KnownByAccount return-by-copy semantics protect the
//     caller's in-memory mutations from cross-talk.
package app

import (
	"context"
	"errors"
	"sync"
	"time"
)

// OAuthStorageSnapshot is a backend-independent view of one pending session.
// Fields mirror what a given owner/session needs to reconstruct behaviour
// across a restart. Internal pointers (network, lease) are intentionally not
// exposed here — they stay in-memory.
type OAuthStorageSnapshot struct {
	ID                 string
	Owner              [32]byte
	ProxyID            string
	AccountID          string
	ExpiresAt          time.Time
	Cancelled          bool
	Ready              bool
	SubmittingFlag     bool
	ExchangingFlag     bool
	TokensJSON         []byte // serialised claudeoauth.Result
	CodeHash           [32]byte
	CreatedAt          time.Time
	LastTouch          time.Time
	ProxyBindingFP     string
	RawProxyURL        string
	OAuthProviderMetad map[string]string
}

// OAuthStorageBackend abstracts persistence of OAuth pending sessions.
// Implementations must be safe for concurrent use. Lifecycle:
//
//	Save → Load/KnownByAccount/Owned → MarkSubmitted/Drop
//
// Cancel/touch race with Save; implementations should linearise on the
// latest LastTouch value.
type OAuthStorageBackend interface {
	// Save inserts or updates a pending session snapshot.
	Save(ctx context.Context, snap OAuthStorageSnapshot) error
	// Load retrieves by session id, returning (nil, nil) if expired or gone.
	Load(ctx context.Context, id string) (*OAuthStorageSnapshot, error)
	// Drop removes a session permanently.
	Drop(ctx context.Context, id string) error
	// Owned lists live session ids belonging to one owner fingerprint.
	// Used by the single-flight check in reusableOAuthPending.
	Owned(ctx context.Context, owner [32]byte, now time.Time) ([]OAuthStorageSnapshot, error)
	// KnownByAccount lists live sessions tied to a specific accountID (excluding
	// ones owned by owner, so an operator on a fresh browser can still adopt it).
	KnownByAccount(ctx context.Context, accountID string, excludeOwner [32]byte, now time.Time) ([]OAuthStorageSnapshot, error)
	// CountLive reports the current size of the pending set (for janitor gauge).
	CountLive(ctx context.Context, now time.Time) (int, error)
	// SweepAll evicts everything expired; runs periodically from janitor.
	SweepAll(ctx context.Context, now time.Time) (removed int, err error)
}

// Compile-time contract documentation. Future adapters must preserve this
// wording only as far as it describes ownership and copy semantics.
var _ interface{ DescribeStorageContract() string } = (*MemoryOAuthBackend)(nil)

// DescribeStorageContract is a small self-documenting hook proving the
// reference backend participates in the storage contract.
func (*MemoryOAuthBackend) DescribeStorageContract() string {
	return "OAuth pending sessions; TTL owned by backend; copy-on-read"
}

// memoryOAuthEntries backs the reference adapter. It deliberately stores only
// the public snapshot: swapping this map for SQLite/Redis must not invent
// semantics that the live oauthPendingStore does not have.
type memoryOAuthEntries struct {
	mu    sync.RWMutex
	items map[string]OAuthStorageSnapshot
}

// MemoryOAuthBackend implements OAuthStorageBackend with copy-on-read maps.
// Expiry is evaluated lazily by every reader, matching the contract that
// Expired behaves as Missing without requiring a sweeper for correctness.
type MemoryOAuthBackend struct {
	entries memoryOAuthEntries
}

func NewMemoryOAuthBackend() *MemoryOAuthBackend {
	return &MemoryOAuthBackend{entries: memoryOAuthEntries{items: map[string]OAuthStorageSnapshot{}}}
}

func (b *MemoryOAuthBackend) clone(v OAuthStorageSnapshot) OAuthStorageSnapshot {
	out := v
	if len(v.TokensJSON) > 0 {
		out.TokensJSON = append([]byte(nil), v.TokensJSON...)
	}
	if len(v.OAuthProviderMetad) > 0 {
		out.OAuthProviderMetad = make(map[string]string, len(v.OAuthProviderMetad))
		for k, val := range v.OAuthProviderMetad {
			out.OAuthProviderMetad[k] = val
		}
	}
	return out
}

func (b *MemoryOAuthBackend) Save(_ context.Context, snap OAuthStorageSnapshot) error {
	b.entries.mu.Lock()
	defer b.entries.mu.Unlock()
	if snap.ID == "" {
		return errors.New("oauth storage: snapshot id required")
	}
	if b.entries.items == nil {
		b.entries.items = map[string]OAuthStorageSnapshot{}
	}
	b.entries.items[snap.ID] = b.clone(snap)
	return nil
}

func (b *MemoryOAuthBackend) Load(_ context.Context, id string) (*OAuthStorageSnapshot, error) {
	b.entries.mu.RLock()
	v, ok := b.entries.items[id]
	b.entries.mu.RUnlock()
	if !ok || !v.ExpiresAt.IsZero() && !time.Now().Before(v.ExpiresAt) {
		return nil, nil
	}
	out := b.clone(v)
	return &out, nil
}

func (b *MemoryOAuthBackend) Drop(_ context.Context, id string) error {
	b.entries.mu.Lock()
	defer b.entries.mu.Unlock()
	delete(b.entries.items, id)
	return nil
}

func (b *MemoryOAuthBackend) live(now time.Time, v OAuthStorageSnapshot) bool {
	return v.ExpiresAt.IsZero() || now.Before(v.ExpiresAt)
}

func (b *MemoryOAuthBackend) Owned(_ context.Context, owner [32]byte, now time.Time) ([]OAuthStorageSnapshot, error) {
	b.entries.mu.RLock()
	defer b.entries.mu.RUnlock()
	out := make([]OAuthStorageSnapshot, 0)
	for _, v := range b.entries.items {
		if v.Owner == owner && b.live(now, v) {
			out = append(out, b.clone(v))
		}
	}
	return out, nil
}

func (b *MemoryOAuthBackend) KnownByAccount(_ context.Context, accountID string, excludeOwner [32]byte, now time.Time) ([]OAuthStorageSnapshot, error) {
	if accountID == "" {
		return nil, nil
	}
	b.entries.mu.RLock()
	defer b.entries.mu.RUnlock()
	out := make([]OAuthStorageSnapshot, 0)
	for _, v := range b.entries.items {
		if v.AccountID == accountID && v.Owner != excludeOwner && b.live(now, v) {
			out = append(out, b.clone(v))
		}
	}
	return out, nil
}

func (b *MemoryOAuthBackend) CountLive(_ context.Context, now time.Time) (int, error) {
	b.entries.mu.RLock()
	defer b.entries.mu.RUnlock()
	n := 0
	for _, v := range b.entries.items {
		if b.live(now, v) {
			n++
		}
	}
	return n, nil
}

func (b *MemoryOAuthBackend) SweepAll(_ context.Context, now time.Time) (int, error) {
	b.entries.mu.Lock()
	defer b.entries.mu.Unlock()
	removed := 0
	for id, v := range b.entries.items {
		if !b.live(now, v) {
			delete(b.entries.items, id)
			removed++
		}
	}
	return removed, nil
}
