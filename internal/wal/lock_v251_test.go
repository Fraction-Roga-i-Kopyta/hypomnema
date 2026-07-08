package wal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquire_PartialConfigDoesNotStealFresh(t *testing.T) { // review C3
	mem := t.TempDir()
	first, err := Acquire(mem, DefaultLockConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	// StaleAfter omitted — must default to 10s, NOT 0 (which would make every
	// held lock instantly "stale" and stealable).
	cfg := LockConfig{MaxWait: 200 * time.Millisecond}
	if _, err := Acquire(mem, cfg); err != ErrTimeout {
		t.Errorf("fresh lock stolen because StaleAfter defaulted to 0, want ErrTimeout, got %v", err)
	}
}

func TestRelease_OnlyRemovesOwnedLock(t *testing.T) { // review C2
	mem := t.TempDir()
	lockDir := filepath.Join(mem, ".wal.lockd")
	a, err := Acquire(mem, DefaultLockConfig)
	if err != nil {
		t.Fatal(err)
	}
	// A stalls past the stale threshold; B legitimately takes the lock over.
	old := time.Now().Add(-30 * time.Second)
	os.Chtimes(lockDir, old, old)
	b, err := Acquire(mem, LockConfig{MaxWait: 2 * time.Second, StaleAfter: 10 * time.Second, PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatalf("B should take over the stale lock: %v", err)
	}
	// A wakes up and releases — this must NOT free B's live lock.
	a.Release()
	if _, err := Acquire(mem, LockConfig{MaxWait: 200 * time.Millisecond, StaleAfter: 10 * time.Second, PollInterval: 20 * time.Millisecond}); err != ErrTimeout {
		t.Errorf("A.Release() removed B's lock — a third acquire succeeded, want ErrTimeout (got %v)", err)
	}
	b.Release()
}

func TestAcquire_ReleaseTokenSurvivesStaleTakeoverChurn(t *testing.T) { // review C1/C2
	// The C1 double-hold free-window is a documented mkdir-lock residual; what
	// MUST hold under stale-takeover churn is that the ownership token keeps a
	// stalled holder's Release from deleting whoever holds the lock now. Here A
	// stalls and is taken over repeatedly; A.Release() at the end must never
	// remove the live lock.
	mem := t.TempDir()
	lockDir := filepath.Join(mem, ".wal.lockd")
	a, err := Acquire(mem, DefaultLockConfig)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * time.Second)
	os.Chtimes(lockDir, old, old)
	cfg := LockConfig{MaxWait: 2 * time.Second, StaleAfter: 10 * time.Second, PollInterval: 10 * time.Millisecond}
	b, err := Acquire(mem, cfg) // B takes over A's stale lock
	if err != nil {
		t.Fatalf("B takeover: %v", err)
	}
	a.Release() // stalled A must not free B's live lock
	if _, err := Acquire(mem, LockConfig{MaxWait: 150 * time.Millisecond, StaleAfter: 10 * time.Second, PollInterval: 20 * time.Millisecond}); err != ErrTimeout {
		t.Errorf("A.Release() freed B's live lock under churn, want ErrTimeout, got %v", err)
	}
	b.Release()
}
