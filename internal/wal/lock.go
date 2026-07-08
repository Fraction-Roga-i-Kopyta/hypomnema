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
	"crypto/rand"
	"encoding/hex"
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

// Lock is an acquired lock handle. Release via Release(). token is a random
// per-acquisition id written into <dir>/owner; Release only removes the dir
// when that token still matches, so a holder that stalled past StaleAfter and
// was taken over cannot later delete the new holder's lock (review C2).
type Lock struct {
	dir   string
	token string
}

const lockOwnerFile = "owner"

func newToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Extremely unlikely; a zero token still works (Release falls back to
		// unconditional remove only when token is empty).
		return ""
	}
	return hex.EncodeToString(b[:])
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
	// Default each zero field independently: a partial config like
	// {MaxWait: X} previously left StaleAfter at 0, making time.Since(mtime)>0
	// true for any held lock — an instant steal of a fresh lock (review C3).
	if cfg.MaxWait == 0 {
		cfg.MaxWait = DefaultLockConfig.MaxWait
	}
	if cfg.StaleAfter == 0 {
		cfg.StaleAfter = DefaultLockConfig.StaleAfter
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = DefaultLockConfig.PollInterval
	}
	deadline := time.Now().Add(cfg.MaxWait)

	for {
		if err := os.Mkdir(lockDir, 0o755); err == nil {
			token := newToken()
			if token != "" {
				_ = os.WriteFile(filepath.Join(lockDir, lockOwnerFile), []byte(token), 0o644)
			}
			return &Lock{dir: lockDir, token: token}, nil
		}
		// Lock dir exists. Decide whether to wait or steal it.
		//
		// KNOWN RESIDUAL (review C1): a stale takeover clears the dir and then
		// re-mkdirs, so there is a microsecond free window in which a second
		// contender can also acquire — a brief double-*hold*. This is inherent
		// to mkdir locks (no conditional-replace primitive) and is only fully
		// closed by moving to flock(2), a separate change. The ownership token
		// below bounds the damage: neither holder's Release can delete the
		// other's live lock, and WAL appends are atomic (O_APPEND), so the
		// window cannot corrupt the WAL. The dangerous case — a stalled holder
		// deleting a live lock on Release — IS fixed here.
		if info, err := os.Stat(lockDir); err == nil {
			if time.Since(info.ModTime()) > cfg.StaleAfter {
				_ = os.RemoveAll(lockDir)
				continue
			}
		}
		if time.Now().After(deadline) {
			return nil, ErrTimeout
		}
		time.Sleep(cfg.PollInterval)
	}
}

// Release removes the lock directory, but only if this handle still owns it:
// the on-disk owner token must match. A holder taken over after stalling past
// StaleAfter therefore cannot delete the new holder's lock (review C2). Safe to
// call multiple times.
func (l *Lock) Release() {
	if l == nil {
		return
	}
	if l.token == "" {
		// No token was written (rand failure) — fall back to best-effort remove.
		_ = os.RemoveAll(l.dir)
		return
	}
	cur, err := os.ReadFile(filepath.Join(l.dir, lockOwnerFile))
	if err != nil || string(cur) != l.token {
		return // taken over (or already gone) — not ours to remove
	}
	_ = os.RemoveAll(l.dir)
}
