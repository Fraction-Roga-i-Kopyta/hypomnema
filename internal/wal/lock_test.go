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

	cfg := LockConfig{
		MaxWait:      80 * time.Millisecond,
		StaleAfter:   10 * time.Second,
		PollInterval: 20 * time.Millisecond,
	}
	start := time.Now()
	_, err = Acquire(mem, cfg)
	elapsed := time.Since(start)
	if err != ErrTimeout {
		t.Errorf("expected ErrTimeout, got %v", err)
	}
	if elapsed < 60*time.Millisecond {
		t.Errorf("Acquire returned too quickly (%v) — didn't actually wait", elapsed)
	}
}

func TestAcquire_StaleTakeover(t *testing.T) {
	mem := t.TempDir()
	lockDir := filepath.Join(mem, ".wal.lockd")
	// Manually create the lock dir and backdate its mtime past StaleAfter.
	if err := os.Mkdir(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * time.Second)
	if err := os.Chtimes(lockDir, old, old); err != nil {
		t.Fatal(err)
	}
	cfg := LockConfig{
		MaxWait:      500 * time.Millisecond,
		StaleAfter:   10 * time.Second, // existing mtime is older → takeover
		PollInterval: 20 * time.Millisecond,
	}
	l, err := Acquire(mem, cfg)
	if err != nil {
		t.Fatalf("expected stale takeover to succeed, got %v", err)
	}
	defer l.Release()
}

func TestAcquire_NoTakeoverWhenFresh(t *testing.T) {
	mem := t.TempDir()
	first, err := Acquire(mem, DefaultLockConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()

	cfg := LockConfig{
		MaxWait:      80 * time.Millisecond,
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
		MaxWait:      60 * time.Millisecond,
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
		MaxWait:      2 * time.Second,
		StaleAfter:   10 * time.Second,
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
