package wal

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The v2.5.1 ownership-token / stale-takeover tests are gone: v2.8 moved the
// lock to flock(2), which has no staleness heuristic and no takeover, so the
// C1 double-hold free-window, the C2 "Release deletes another's lock", and the
// C3 "partial config steals a fresh lock" bugs are all eliminated by
// construction. These tests assert the flock guarantees directly.

func TestAcquire_HeldLockNeverAcquirableRegardlessOfConfig(t *testing.T) { // ex-C3
	mem := t.TempDir()
	first, err := Acquire(mem, DefaultLockConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	// A partial/odd config must not let a held lock be stolen — flock has no
	// stale-takeover path at all, so a held lock always times out.
	for _, cfg := range []LockConfig{
		{MaxWait: 150 * time.Millisecond},                // StaleAfter/Poll zero
		{MaxWait: 150 * time.Millisecond, StaleAfter: 0}, // explicit zero StaleAfter (was the C3 trigger)
		{MaxWait: 150 * time.Millisecond, PollInterval: 5 * time.Millisecond},
	} {
		if _, err := Acquire(mem, cfg); err != ErrTimeout {
			t.Errorf("held lock must time out for cfg %+v, got %v", cfg, err)
		}
	}
}

func TestAcquire_HeavyContentionSingleHolder(t *testing.T) { // ex-C1: no double-hold window
	mem := t.TempDir()
	cfg := LockConfig{MaxWait: 10 * time.Second, PollInterval: time.Millisecond}
	const workers, rounds = 8, 25
	var held, maxHeld int32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				l, err := Acquire(mem, cfg)
				if err != nil {
					t.Errorf("acquire timed out under contention: %v", err)
					return
				}
				n := atomic.AddInt32(&held, 1)
				for {
					m := atomic.LoadInt32(&maxHeld)
					if n <= m || atomic.CompareAndSwapInt32(&maxHeld, m, n) {
						break
					}
				}
				time.Sleep(200 * time.Microsecond)
				atomic.AddInt32(&held, -1)
				l.Release()
			}
		}()
	}
	wg.Wait()
	if maxHeld != 1 {
		t.Errorf("flock permitted %d concurrent holders, want 1 (no free window)", maxHeld)
	}
}

func TestRelease_AfterReleaseAnotherCanAcquire(t *testing.T) { // ex-C2: clean handoff
	mem := t.TempDir()
	a, err := Acquire(mem, DefaultLockConfig)
	if err != nil {
		t.Fatal(err)
	}
	a.Release()
	b, err := Acquire(mem, LockConfig{MaxWait: 500 * time.Millisecond, PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("after Release the lock must be re-acquirable, got %v", err)
	}
	b.Release()
}
