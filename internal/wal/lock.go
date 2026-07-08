// Package wal provides a cross-process advisory lock for serialising WAL
// (and other memory-metadata) writes.
//
// v2.8 replaced the v1/v2 mkdir-based lock with flock(2). The mkdir lock
// needed a staleness heuristic to recover an orphaned lock after a crash, and
// that heuristic had an irreducible free window during takeover (a brief
// double-acquire) plus a Release that could delete a lock another process had
// legitimately taken over. flock has none of those problems: the OS drops the
// lock automatically when the holding process exits, so there is no staleness
// to guess at and no takeover to race. The lock is held for the lifetime of an
// open file descriptor; Release unlocks and closes it.
//
// Platform: flock(2) is available on Linux and macOS (the supported targets;
// Windows is WSL-only). It is advisory and reliable on local filesystems —
// which is where ~/.claude lives.
package wal

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// LockConfig controls lock acquisition behaviour.
type LockConfig struct {
	// MaxWait bounds how long Acquire blocks before returning ErrTimeout.
	MaxWait time.Duration
	// StaleAfter is retained for API compatibility but is now IGNORED: flock
	// releases automatically when the holding process exits, so there is no
	// orphaned-lock staleness to time out. (Pre-v2.8 mkdir lock used it.)
	StaleAfter time.Duration
	// PollInterval is the sleep between non-blocking flock retries.
	PollInterval time.Duration
}

// DefaultLockConfig is the standard acquisition budget.
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
	file *os.File
}

// Acquire blocks until the WAL lock at memoryDir/.wal.lockd is held, or the
// configured deadline elapses. Thin wrapper around AcquirePath.
func Acquire(memoryDir string, cfg LockConfig) (*Lock, error) {
	return AcquirePath(filepath.Join(memoryDir, ".wal.lockd"), cfg)
}

// AcquirePath flock-locks the file at lockPath, creating it if needed, retrying
// non-blockingly until held or the deadline elapses (ErrTimeout). Exposed so
// non-WAL resources (e.g. self-profile.md writes) reuse the same primitive.
func AcquirePath(lockPath string, cfg LockConfig) (*Lock, error) {
	if cfg.MaxWait == 0 {
		cfg.MaxWait = DefaultLockConfig.MaxWait
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = DefaultLockConfig.PollInterval
	}

	// Migrate a legacy mkdir lock: pre-v2.8 used a DIRECTORY at this path.
	// flock needs a regular file, so remove a directory left there. (Post-
	// upgrade there are no old-version holders; the dir is an orphan.)
	if info, err := os.Stat(lockPath); err == nil && info.IsDir() {
		_ = os.RemoveAll(lockPath)
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(cfg.MaxWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &Lock{file: f}, nil
		}
		if err != syscall.EWOULDBLOCK {
			// A real error (not "held by someone else") — don't leak the fd.
			_ = f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, ErrTimeout
		}
		time.Sleep(cfg.PollInterval)
	}
}

// Release drops the lock (flock LOCK_UN) and closes the descriptor. The lock
// file itself is intentionally left on disk — unlinking it under flock would
// race with a concurrent acquirer. Safe to call multiple times.
func (l *Lock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}
