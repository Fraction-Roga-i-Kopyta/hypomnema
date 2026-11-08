package wal

import (
	"bufio"
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
	Backup      string // path of the pre-compaction copy; "" on a dry run
}

// Compact rewrites memoryDir/.wal without LegacyEvents rows. It runs under
// the WAL lock: the kept rows go to a temp file in the same directory, the
// original is hard-linked to .wal.pre-compact-<today> (replacing an earlier
// same-day backup), and the temp file is renamed over .wal. Rows that are
// not four pipe-separated columns are kept as they are — Compact removes
// only what it positively recognises as dead.
//
// Appenders that time out on the lock write without it (append.go), so a
// row appended while Compact holds the lock past that timeout can land in
// the backup instead of the new WAL; run it when no session is mid-turn.
func Compact(memoryDir, today string, dryRun bool) (CompactReport, error) {
	rep := CompactReport{Dropped: map[string]int{}}
	walPath := filepath.Join(memoryDir, ".wal")
	if _, err := os.Stat(walPath); err != nil {
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

	in, err := os.Open(walPath)
	if err != nil {
		return rep, fmt.Errorf("wal.Compact: %w", err)
	}
	defer in.Close()
	if fi, err := in.Stat(); err == nil {
		rep.BytesBefore = fi.Size()
	}

	var out *os.File
	var w *bufio.Writer
	if !dryRun {
		out, err = os.CreateTemp(memoryDir, ".wal.compact.*.tmp")
		if err != nil {
			return rep, fmt.Errorf("wal.Compact: temp: %w", err)
		}
		defer os.Remove(out.Name()) // no-op once renamed
		defer out.Close()
		w = bufio.NewWriter(out)
	}

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if f := strings.Split(line, "|"); len(f) == 4 && LegacyEvents[f[1]] {
			rep.Dropped[f[1]]++
			continue
		}
		rep.Kept++
		rep.BytesAfter += int64(len(line)) + 1
		if w != nil {
			if _, err := w.WriteString(line + "\n"); err != nil {
				return rep, fmt.Errorf("wal.Compact: write: %w", err)
			}
		}
	}
	if err := sc.Err(); err != nil {
		// A row over the scanner limit or a read error: stop rather than
		// write a WAL with its tail cut off.
		return rep, fmt.Errorf("wal.Compact: read: %w", err)
	}
	if dryRun {
		return rep, nil
	}
	if err := w.Flush(); err != nil {
		return rep, fmt.Errorf("wal.Compact: flush: %w", err)
	}
	if err := out.Sync(); err != nil {
		return rep, fmt.Errorf("wal.Compact: sync: %w", err)
	}
	if err := out.Close(); err != nil {
		return rep, fmt.Errorf("wal.Compact: close: %w", err)
	}
	backup := filepath.Join(memoryDir, ".wal.pre-compact-"+today)
	_ = os.Remove(backup)
	if err := os.Link(walPath, backup); err != nil {
		return rep, fmt.Errorf("wal.Compact: backup: %w", err)
	}
	if err := os.Rename(out.Name(), walPath); err != nil {
		return rep, fmt.Errorf("wal.Compact: replace: %w", err)
	}
	rep.Backup = backup
	return rep, nil
}
