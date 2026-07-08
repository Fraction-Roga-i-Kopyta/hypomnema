package wal

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAcquire_EmptyDirSucceeds(t *testing.T) {
	mem := t.TempDir()
	l, err := Acquire(mem, DefaultLockConfig)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer l.Release()
	if _, err := os.Stat(filepath.Join(mem, ".wal.lockd")); err != nil {
		t.Errorf("lock dir should exist after Acquire: %v", err)
	}
}

func TestAcquire_ReleaseIsIdempotent(t *testing.T) {
	mem := t.TempDir()
	l, err := Acquire(mem, DefaultLockConfig)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	l.Release()
	l.Release() // must not panic or error.
}

func TestAcquire_TimesOutWhenHeld(t *testing.T) {
	mem := t.TempDir()
	first, err := Acquire(mem, DefaultLockConfig)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer first.Release()

	// 500ms budget: the original 60–80ms budgets flaked on loaded CI
	// runners (2026-07-08 review P2) — scheduler jitter alone can eat 80ms.
	cfg := LockConfig{
		MaxWait:      500 * time.Millisecond,
		StaleAfter:   10 * time.Second,
		PollInterval: 20 * time.Millisecond,
	}
	start := time.Now()
	_, err = Acquire(mem, cfg)
	elapsed := time.Since(start)
	if err != ErrTimeout {
		t.Errorf("expected ErrTimeout, got %v", err)
	}
	if elapsed < 400*time.Millisecond {
		t.Errorf("Acquire returned too quickly (%v) — didn't actually wait", elapsed)
	}
}

// Under flock there is no staleness/takeover: an unheld lock (even one left
// over from a crashed holder) is simply free to acquire — the OS dropped it
// when that process died. A stale *file* at the path is acquired immediately.
func TestAcquire_UnheldLockIsFree(t *testing.T) {
	mem := t.TempDir()
	// A pre-existing (unlocked) lock file from a prior run must not block.
	if err := os.WriteFile(filepath.Join(mem, ".wal.lockd"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := Acquire(mem, LockConfig{MaxWait: 500 * time.Millisecond, PollInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatalf("an unheld lock file must acquire immediately, got %v", err)
	}
	l.Release()
}

func TestAcquire_NoTakeoverWhenFresh(t *testing.T) {
	mem := t.TempDir()
	first, err := Acquire(mem, DefaultLockConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()

	cfg := LockConfig{
		MaxWait:      500 * time.Millisecond,
		StaleAfter:   10 * time.Second, // held lock is 0s old
		PollInterval: 20 * time.Millisecond,
	}
	if _, err := Acquire(mem, cfg); err != ErrTimeout {
		t.Errorf("expected timeout on fresh lock, got %v", err)
	}
}

func TestAcquirePath_AllowsNonWALResources(t *testing.T) {
	// Regression guard for v0.15 P2.12: AcquirePath must lock an
	// arbitrary directory, not only .wal.lockd. Profile regen uses
	// .self-profile.lockd via this entrypoint.
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, ".self-profile.lockd")
	l1, err := AcquirePath(lockPath, DefaultLockConfig)
	if err != nil {
		t.Fatalf("first AcquirePath: %v", err)
	}
	defer l1.Release()

	// Second Acquire on same path must time out while l1 is held.
	cfg := LockConfig{
		MaxWait:      500 * time.Millisecond,
		StaleAfter:   10 * time.Second,
		PollInterval: 20 * time.Millisecond,
	}
	if _, err := AcquirePath(lockPath, cfg); err != ErrTimeout {
		t.Errorf("expected ErrTimeout on held custom lock, got %v", err)
	}
}

func TestAcquire_ConcurrentCallersSerialize(t *testing.T) {
	// Exercises the mkdir-atomicity guarantee under real contention.
	// Spawn N goroutines that each try to acquire, hold briefly, release;
	// no two should hold the lock simultaneously.
	mem := t.TempDir()
	const workers = 8
	var wg sync.WaitGroup
	var held int32
	var maxHeld int32
	var mu sync.Mutex

	cfg := LockConfig{
		MaxWait:      10 * time.Second, // generous: 8 contending workers under -race on a 2-core runner
		StaleAfter:   30 * time.Second,
		PollInterval: 5 * time.Millisecond,
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := Acquire(mem, cfg)
			if err != nil {
				t.Errorf("Acquire timed out under contention: %v", err)
				return
			}
			mu.Lock()
			held++
			if held > maxHeld {
				maxHeld = held
			}
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			held--
			mu.Unlock()
			l.Release()
		}()
	}
	wg.Wait()
	if maxHeld != 1 {
		t.Errorf("lock permitted %d concurrent holders, want 1", maxHeld)
	}
}

func TestAcquire_MigratesLegacyMkdirLock(t *testing.T) { // C1 flock migration
	mem := t.TempDir()
	// Pre-v2.8 versions used a DIRECTORY as the lock; flock needs a file.
	// A leftover legacy dir at the path must be migrated, not treated as a
	// permanently-held lock.
	if err := os.Mkdir(filepath.Join(mem, ".wal.lockd"), 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := Acquire(mem, LockConfig{MaxWait: 500 * time.Millisecond, PollInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatalf("Acquire must migrate a legacy lock dir and succeed, got: %v", err)
	}
	l.Release()
}
