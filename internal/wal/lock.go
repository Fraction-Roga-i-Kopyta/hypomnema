// Package wal mirrors the semantics of hooks/lib/wal-lock.sh for Go callers.
// The lock is a directory — mkdir(2) is atomic on every POSIX filesystem, so
// acquire is just "try to create the dir, retry with backoff until the caller's
// deadline, force-release anything older than the stale threshold."
//
// The bash implementation's fallback-to-unlocked-run is intentionally not
// ported: Go callers should decide explicitly whether to proceed without a
// lock. Acquire returns an error; the caller picks the policy.
package wal

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// LockConfig controls lock acquisition behaviour. Zero values fall back to the
// same defaults as the bash implementation (see wal-lock.sh).
type LockConfig struct {
	// MaxWait bounds how long Acquire will block before returning ErrTimeout.
	MaxWait time.Duration
	// StaleAfter is the age past which an existing lock directory is assumed
	// orphaned and forcibly removed. Must be larger than any legitimate lock
	// hold time.
	StaleAfter time.Duration
	// PollInterval is the sleep between acquire retries.
	PollInterval time.Duration
}

// DefaultLockConfig matches the bash hook defaults exactly.
var DefaultLockConfig = LockConfig{
	MaxWait:      1000 * time.Millisecond,
	StaleAfter:   10 * time.Second,
	PollInterval: 50 * time.Millisecond,
}

// ErrTimeout is returned when Acquire could not obtain the lock before the
// configured deadline.
var ErrTimeout = errors.New("wal lock: acquire timed out")

// Lock is an acquired lock handle. Release via Release().
type Lock struct {
	dir string
}

// Acquire blocks until the WAL lock at memoryDir/.wal.lockd is held, or the
// configured deadline elapses. Thin wrapper around AcquirePath for the
// canonical WAL lock location.
func Acquire(memoryDir string, cfg LockConfig) (*Lock, error) {
	return AcquirePath(filepath.Join(memoryDir, ".wal.lockd"), cfg)
}

// AcquirePath blocks until the lock directory at lockDir is held, or the
// configured deadline elapses. Same mkdir-atomicity semantics as Acquire;
// exposed so non-WAL resources (e.g. self-profile.md writes) can reuse the
// same stale-takeover logic without duplicating it per-package.
func AcquirePath(lockDir string, cfg LockConfig) (*Lock, error) {
	if cfg.MaxWait == 0 {
		cfg = DefaultLockConfig
	}
	deadline := time.Now().Add(cfg.MaxWait)

	for {
		if err := os.Mkdir(lockDir, 0o755); err == nil {
			return &Lock{dir: lockDir}, nil
		}
		// Lock dir exists. Decide whether to wait or steal it.
		if info, err := os.Stat(lockDir); err == nil {
			if time.Since(info.ModTime()) > cfg.StaleAfter {
				// Orphaned — take it over. rmdir may race with another
				// Acquire; either winner proceeds to the mkdir retry.
				_ = os.Remove(lockDir)
				continue
			}
		}
		if time.Now().After(deadline) {
			return nil, ErrTimeout
		}
		time.Sleep(cfg.PollInterval)
	}
}

// Release removes the lock directory. Safe to call multiple times.
func (l *Lock) Release() {
	if l == nil {
		return
	}
	_ = os.Remove(l.dir)
}
