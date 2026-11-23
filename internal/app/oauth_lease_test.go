package app

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// A replacement may only inherit the account lease while the previous session
// still holds it. Once a cancel has released it (the entry is gone or marked
// cancelled, or the lease pointer changed), the replacement must take its own
// lock instead of running without one.
func TestAdoptLeaseLockedRequiresLivePrevious(t *testing.T) {
	owner := [32]byte{7}
	lease := &oauthLease{unlock: func() {}}
	store := &oauthPendingStore{items: map[string]oauthPending{
		"live":      {owner: owner, accountID: "acc", expires: time.Now().Add(time.Minute), lease: lease},
		"cancelled": {owner: owner, accountID: "acc", expires: time.Now().Add(time.Minute), lease: lease, cancelled: true},
		"swapped":   {owner: owner, accountID: "acc", expires: time.Now().Add(time.Minute), lease: &oauthLease{unlock: func() {}}},
	}}
	if store.adoptLeaseLocked("gone", lease) {
		t.Fatal("adopted a lease from a session that no longer exists")
	}
	if store.adoptLeaseLocked("cancelled", lease) {
		t.Fatal("adopted a lease from a cancelled session")
	}
	if store.adoptLeaseLocked("swapped", lease) {
		t.Fatal("adopted a lease that is no longer the observed one")
	}
	if store.adoptLeaseLocked("live", nil) {
		t.Fatal("adopted without an observed lease")
	}
	if !store.adoptLeaseLocked("live", lease) {
		t.Fatal("live session's lease was not transferred")
	}
	if store.items["live"].lease != nil {
		t.Fatal("previous entry kept the lease it handed over")
	}
	// Dropping the retired entry must not release the transferred lease.
	released := false
	lease.unlock = func() { released = true }
	store.dropLocked("live")
	if released {
		t.Fatal("drop of the retired session released the adopted lease")
	}
}

// Refreshing the link of an existing account keeps the account write lock
// across the replacement: the new session inherits the live lease, the old
// entry is retired without releasing it, and a competing ordinary write still
// sees the account as busy.
func TestOAuthRefreshForAccountKeepsLeaseAcrossReplacement(t *testing.T) {
	a, _, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	retryOAuthProxy(t, a, fixtureProvider("one@example.test"))
	first := startSession(t, admin, `{"proxy_id":"review-proxy","account_id":"`+id+`"}`)
	if first.status != http.StatusCreated {
		t.Fatalf("first start: %d %v", first.status, first.data)
	}
	firstID := first.data["id"].(string)
	a.oauth.mu.Lock()
	lease := a.oauth.items[firstID].lease
	a.oauth.mu.Unlock()
	if lease == nil {
		t.Fatal("account session started without a lease")
	}
	refreshed := startSession(t, admin, `{"proxy_id":"review-proxy","account_id":"`+id+`","refresh":true}`)
	if refreshed.status != http.StatusCreated || refreshed.data["id"] == firstID {
		t.Fatalf("refresh: %d %v", refreshed.status, refreshed.data)
	}
	a.oauth.mu.Lock()
	_, oldAlive := a.oauth.items[firstID]
	current := a.oauth.items[refreshed.data["id"].(string)]
	a.oauth.mu.Unlock()
	if oldAlive || current.lease != lease {
		t.Fatalf("lease not carried over: oldAlive=%v same=%v", oldAlive, current.lease == lease)
	}
	if unlock, ok := a.accountLocks.tryLock(id); ok {
		unlock()
		t.Fatal("account lock was released by the replacement")
	}
	res := admin.do("PATCH", "/api/v1/accounts/"+id, `{"remark":"busy"}`)
	if body := readBody(res); res.StatusCode != http.StatusConflict || !strings.Contains(body, "ACCOUNT_IN_PROGRESS") {
		t.Fatalf("ordinary write during live session: %d %s", res.StatusCode, body)
	}
}
