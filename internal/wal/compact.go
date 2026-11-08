package wal

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LegacyEvents are WAL event types that no reader consumes any more, so
// Compact drops them:
//   - trigger-useful / trigger-silent / trigger-silent-retro — the pre-v2.14
//     usefulness signal; v2.14 restarted usefulness on explicit <cc-memory>
//     citations and nothing has read or written these since.
//   - session-metrics — written for self-profile, removed with it.
//
// Everything else stays, including rows Compact does not understand.
var LegacyEvents = map[string]bool{
	"trigger-useful":       true,
	"trigger-silent":       true,
	"trigger-silent-retro": true,
	"session-metrics":      true,
}

// CompactReport describes one compaction (or, with dryRun, what it would do).
type CompactReport struct {
	Kept        int
	Dropped     map[string]int
	BytesBefore int64
	BytesAfter  int64
	Backup      string // path of the pre-compaction copy; "" on a dry run or when nothing was dropped
}

// Compact rewrites memoryDir/.wal without LegacyEvents rows. It runs under
// the WAL lock: kept rows are copied as raw bytes (line endings included) to
// a temp file in the same directory with the WAL's mode, the original is
// hard-linked to .wal.pre-compact-<today> (or -2, -3… — a backup is never
// replaced), and the temp file is renamed over .wal. With nothing to drop it
// changes nothing. Rows that are not four pipe-separated columns are kept —
// Compact removes only what it positively recognises as dead. A temp file
// left by a killed run is removed first.
//
// Appenders that time out on the lock (1 s) write without it (append.go), so
// a row appended while Compact holds the lock longer than that could land in
// the backup instead of the new WAL; compaction of a large WAL takes tens of
// milliseconds, and it is meant to run when no session is mid-turn.
func Compact(memoryDir, today string, dryRun bool) (CompactReport, error) {
	rep := CompactReport{Dropped: map[string]int{}}
	walPath := filepath.Join(memoryDir, ".wal")
	fi, err := os.Stat(walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return rep, ErrWALMissing
		}
		return rep, fmt.Errorf("wal.Compact: %w", err)
	}
	lock, err := Acquire(memoryDir, DefaultLockConfig)
	if err != nil {
		return rep, fmt.Errorf("wal.Compact: lock: %w", err)
	}
	defer lock.Release()
	if !dryRun {
		if stale, _ := filepath.Glob(filepath.Join(memoryDir, ".wal.compact.*.tmp")); len(stale) > 0 {
			for _, f := range stale {
				_ = os.Remove(f)
			}
		}
	}

	raw, err := os.ReadFile(walPath)
	if err != nil {
		return rep, fmt.Errorf("wal.Compact: %w", err)
	}
	rep.BytesBefore = int64(len(raw))
	kept := make([]byte, 0, len(raw))
	for rest := raw; len(rest) > 0; {
		n := bytes.IndexByte(rest, '\n') + 1
		if n == 0 {
			n = len(rest)
		}
		line := rest[:n]
		rest = rest[n:]
		f := strings.Split(strings.TrimRight(string(line), "\r\n"), "|")
		if len(f) == 4 && LegacyEvents[f[1]] {
			rep.Dropped[f[1]]++
			continue
		}
		rep.Kept++
		kept = append(kept, line...)
	}
	rep.BytesAfter = int64(len(kept))
	if dryRun || len(rep.Dropped) == 0 {
		return rep, nil
	}

	out, err := os.CreateTemp(memoryDir, ".wal.compact.*.tmp")
	if err != nil {
		return rep, fmt.Errorf("wal.Compact: temp: %w", err)
	}
	defer os.Remove(out.Name()) // no-op once renamed
	defer out.Close()
	if _, err := out.Write(kept); err != nil {
		return rep, fmt.Errorf("wal.Compact: write: %w", err)
	}
	if err := out.Chmod(fi.Mode().Perm()); err != nil {
		return rep, fmt.Errorf("wal.Compact: chmod: %w", err)
	}
	if err := out.Sync(); err != nil {
		return rep, fmt.Errorf("wal.Compact: sync: %w", err)
	}
	if err := out.Close(); err != nil {
		return rep, fmt.Errorf("wal.Compact: close: %w", err)
	}
	backup := filepath.Join(memoryDir, ".wal.pre-compact-"+today)
	for i := 2; ; i++ {
		err = os.Link(walPath, backup)
		if !os.IsExist(err) {
			break
		}
		backup = filepath.Join(memoryDir, fmt.Sprintf(".wal.pre-compact-%s-%d", today, i))
	}
	if err != nil {
		return rep, fmt.Errorf("wal.Compact: backup: %w", err)
	}
	if err := os.Rename(out.Name(), walPath); err != nil {
		return rep, fmt.Errorf("wal.Compact: replace: %w", err)
	}
	if d, err := os.Open(memoryDir); err == nil { // persist the rename
		_ = d.Sync()
		d.Close()
	}
	rep.Backup = backup
	return rep, nil
}
