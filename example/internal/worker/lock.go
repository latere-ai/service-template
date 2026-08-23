package worker

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Lock errors. A caller distinguishes "another replica is running this" from
// "the lock service is unreachable", because the first is the mechanism working
// and the second is an outage.
var (
	// ErrLockHeld reports that another holder owns an unexpired lease.
	ErrLockHeld = errors.New("worker: lock held by another holder")
	// ErrLockLost reports a renew or a release against a lease that expired or
	// was taken over.
	ErrLockLost = errors.New("worker: lock lost")
)

// Locker hands out a lease on a name. It is the seam a deployment plugs a
// shared lock service into: a database row, a key with a time to live, or a
// coordination service. The runtime asks for nothing else.
//
// Acquire returns [ErrLockHeld] when an unexpired lease exists under the name.
// Any other error is an infrastructure failure and is reported as one.
type Locker interface {
	Acquire(ctx context.Context, name string, lease time.Duration) (Lock, error)
}

// Lock is one held lease.
//
// Renew extends it while the work runs, which is what lets the lease be shorter
// than the schedule interval: a replica that dies stops renewing and the lease
// expires, instead of holding the name until someone intervenes. Renew and
// Release return [ErrLockLost] once the lease is gone.
type Lock interface {
	Renew(ctx context.Context, lease time.Duration) error
	Release(ctx context.Context) error
}

// MemoryLocker is an in-process [Locker].
//
// It serialises executions inside one process only. It is the right choice for
// a single-replica deployment and for tests. Two replicas each holding their
// own MemoryLocker produce two executions per interval, which is the failure
// the lock exists to prevent, so a multi-replica deployment supplies an
// implementation backed by shared storage.
type MemoryLocker struct {
	mu    sync.Mutex
	held  map[string]*memoryLock
	next  uint64
	clock clock
}

// NewMemoryLocker returns an empty in-process locker.
func NewMemoryLocker() *MemoryLocker {
	return &MemoryLocker{held: make(map[string]*memoryLock), clock: systemClock{}}
}

// memoryLock is one lease held in memory. The token separates this holder from
// a later holder of the same name, so a release that arrives after the lease
// expired and the name changed hands does not free someone else's lease.
type memoryLock struct {
	locker  *MemoryLocker
	name    string
	token   uint64
	expires time.Time
}

// Acquire takes the name when no unexpired lease exists under it.
func (l *MemoryLocker) Acquire(_ context.Context, name string, lease time.Duration) (Lock, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock.Now()
	if cur, ok := l.held[name]; ok && cur.expires.After(now) {
		return nil, ErrLockHeld
	}

	l.next++
	held := &memoryLock{locker: l, name: name, token: l.next, expires: now.Add(lease)}
	l.held[name] = held
	return held, nil
}

// expiry reports the lease deadline recorded under name, and whether the name
// is held at all.
func (l *MemoryLocker) expiry(name string) (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cur, ok := l.held[name]
	if !ok {
		return time.Time{}, false
	}
	return cur.expires, true
}

// Renew extends the lease. A lease that already expired is not extended: the
// name may have changed hands, and the holder must learn that it lost it.
func (m *memoryLock) Renew(_ context.Context, lease time.Duration) error {
	l := m.locker
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock.Now()
	cur, ok := l.held[m.name]
	if !ok || cur.token != m.token || !cur.expires.After(now) {
		return ErrLockLost
	}
	cur.expires = now.Add(lease)
	m.expires = cur.expires
	return nil
}

// Release frees the name when this holder still owns it, and reports
// [ErrLockLost] when it does not.
func (m *memoryLock) Release(_ context.Context) error {
	l := m.locker
	l.mu.Lock()
	defer l.mu.Unlock()

	cur, ok := l.held[m.name]
	if !ok || cur.token != m.token {
		return ErrLockLost
	}
	delete(l.held, m.name)
	return nil
}
