package worker

import (
	"context"
	"errors"
	"testing"
	"time"
)

// newTestLocker returns an in-process locker driven by the given clock.
func newTestLocker(c clock) *MemoryLocker {
	l := NewMemoryLocker()
	l.clock = c
	return l
}

func TestMemoryLockerHoldsOneName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	locker := newTestLocker(newFakeClock())

	first, err := locker.Acquire(ctx, "reconcile", time.Minute)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if _, err := locker.Acquire(ctx, "reconcile", time.Minute); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("second Acquire error = %v, want ErrLockHeld", err)
	}
	if _, err := locker.Acquire(ctx, "backfill", time.Minute); err != nil {
		t.Fatalf("Acquire of a different name: %v", err)
	}

	if err := first.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := locker.Acquire(ctx, "reconcile", time.Minute); err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
}

func TestMemoryLockerLeaseExpiryFreesTheName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clk := newFakeClock()
	locker := newTestLocker(clk)

	// A holder that stops renewing is a replica that died. The name must come
	// free on its own, or the schedule stops until someone intervenes.
	if _, err := locker.Acquire(ctx, "reconcile", 30*time.Second); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	clk.Advance(29 * time.Second)
	if _, err := locker.Acquire(ctx, "reconcile", 30*time.Second); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("Acquire inside the lease = %v, want ErrLockHeld", err)
	}

	clk.Advance(2 * time.Second)
	if _, err := locker.Acquire(ctx, "reconcile", 30*time.Second); err != nil {
		t.Fatalf("Acquire after the lease expired: %v", err)
	}
}

func TestMemoryLockerRenewExtendsTheLease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clk := newFakeClock()
	locker := newTestLocker(clk)

	held, err := locker.Acquire(ctx, "reconcile", 30*time.Second)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	clk.Advance(20 * time.Second)
	if err := held.Renew(ctx, 30*time.Second); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	clk.Advance(20 * time.Second)
	if _, err := locker.Acquire(ctx, "reconcile", 30*time.Second); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("Acquire after a renewal = %v, want ErrLockHeld", err)
	}
}

func TestMemoryLockerRenewAfterExpiryReportsLost(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clk := newFakeClock()
	locker := newTestLocker(clk)

	held, err := locker.Acquire(ctx, "reconcile", 30*time.Second)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	clk.Advance(31 * time.Second)

	if err := held.Renew(ctx, 30*time.Second); !errors.Is(err, ErrLockLost) {
		t.Fatalf("Renew after expiry = %v, want ErrLockLost", err)
	}
	if err := held.Release(ctx); err != nil {
		t.Fatalf("Release of an expired lease: %v", err)
	}
}

func TestMemoryLockerReleaseDoesNotFreeAnotherHolder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clk := newFakeClock()
	locker := newTestLocker(clk)

	first, err := locker.Acquire(ctx, "reconcile", 30*time.Second)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	clk.Advance(31 * time.Second)

	second, err := locker.Acquire(ctx, "reconcile", 30*time.Second)
	if err != nil {
		t.Fatalf("Acquire after expiry: %v", err)
	}
	// The first holder releases late. The name now belongs to the second
	// holder, and a stale release must not hand it to a third.
	if err := first.Release(ctx); !errors.Is(err, ErrLockLost) {
		t.Fatalf("stale Release = %v, want ErrLockLost", err)
	}
	if _, err := locker.Acquire(ctx, "reconcile", 30*time.Second); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("Acquire after a stale release = %v, want ErrLockHeld", err)
	}
	if err := second.Release(ctx); err != nil {
		t.Fatalf("Release by the current holder: %v", err)
	}
}
