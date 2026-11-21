package app

import (
	"context"
	"sync"
)

// accountLocks serialises the two writers that can race on one account's
// credential: an OAuth reauthorization pushing a fresh token to the supplier,
// and the submit worker pushing the stored token. Two overlapping pushes leave
// the supplier holding whichever arrived last, which is how a just-completed
// reauthorization gets silently replaced by a stale snapshot.
type accountLocks struct {
	mu    sync.Mutex
	items map[string]*accountLockEntry
}

type accountLockEntry struct {
	mu   sync.Mutex
	refs int
}

// lock waits for the account and returns the release func. The entry is
// reference counted so the map does not grow with every account ever touched.
func (l *accountLocks) lock(ctx context.Context, id string) (func(), error) {
	e := l.ref(id)
	if err := lockWithContext(ctx, &e.mu); err != nil {
		l.drop(id)
		return nil, err
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		e.mu.Unlock()
		l.drop(id)
	}, nil
}

// tryLock never waits. A busy account means another turn is mid-flight.
func (l *accountLocks) tryLock(id string) (func(), bool) {
	l.mu.Lock()
	if l.items == nil {
		l.items = map[string]*accountLockEntry{}
	}
	e, ok := l.items[id]
	if !ok {
		e = &accountLockEntry{}
		l.items[id] = e
	}
	e.refs++
	l.mu.Unlock()
	if !e.mu.TryLock() {
		l.drop(id)
		return nil, false
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		e.mu.Unlock()
		l.drop(id)
	}, true
}

func (l *accountLocks) ref(id string) *accountLockEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.items == nil {
		l.items = map[string]*accountLockEntry{}
	}
	e, ok := l.items[id]
	if !ok {
		e = &accountLockEntry{}
		l.items[id] = e
	}
	e.refs++
	return e
}

func (l *accountLocks) drop(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.items[id]
	if !ok {
		return
	}
	e.refs--
	if e.refs <= 0 {
		delete(l.items, id)
	}
}
