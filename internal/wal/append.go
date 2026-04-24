package wal

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Append writes a single line to the WAL file under lock. If dedupKey is
// non-empty and already appears in the current WAL, the write is skipped —
// mirrors the substring-dedup semantics of the bash `wal_append`.
//
// Failures are deliberately swallowed: hooks must be fail-safe, and a broken
// WAL write is a worse degradation than a silent missed event. Callers that
// need to know about failures should use AppendStrict.
func Append(memoryDir, line, dedupKey string) {
	_ = AppendStrict(memoryDir, line, dedupKey)
}

// AppendStrict writes to the WAL and returns any error encountered. Unlike
// Append it does not swallow errors; useful in tests and for
// acceptance-comparison with the bash implementation.
func AppendStrict(memoryDir, line, dedupKey string) error {
	if line == "" {
		return nil
	}
	lock, err := Acquire(memoryDir, DefaultLockConfig)
	if err != nil {
		// Bash fallback: run anyway (see wal-lock.sh:53). Choose the same
		// policy here — better to race on append than to drop the event.
		return writeLine(memoryDir, line, dedupKey)
	}
	defer lock.Release()
	return writeLine(memoryDir, line, dedupKey)
}

func writeLine(memoryDir, line, dedupKey string) error {
	walPath := filepath.Join(memoryDir, ".wal")

	if dedupKey != "" {
		if seen, _ := containsLine(walPath, dedupKey); seen {
			return nil
		}
	}

	f, err := os.OpenFile(walPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	_, err = f.WriteString(line)
	return err
}

// dedupTailBytes is how many trailing bytes of the WAL containsLine
// scans when looking for a recent dedup key. Dedup events we care about
// are always recently appended — older duplicates are natural load
// history and the scan was only ever walking them to confirm absence.
// 256 KiB covers ~2000 canonical 4-column events and keeps the dedup
// check at O(1) instead of O(WAL-size). Tunable for tests.
const dedupTailBytes int64 = 256 * 1024

// containsLine is the Go equivalent of `grep -qF $dedupKey $(tail -c 256K $wal)`
// — substring match against the trailing slice of the WAL only. Dedup
// events arrive in append order, so the last few kilobytes of events
// are the only region a fresh duplicate could land in. On a small WAL
// (< dedupTailBytes) we read the whole file, matching the previous
// behaviour; on a 50 MB live WAL we read 256 KiB instead of 50 MB,
// removing the quadratic blow-up the older implementation had when
// WAL growth outpaced compaction.
func containsLine(walPath, key string) (bool, error) {
	f, err := os.Open(walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return false, err
	}
	size := fi.Size()
	offset := int64(0)
	if size > dedupTailBytes {
		offset = size - dedupTailBytes
		if _, err := f.Seek(offset, 0); err != nil {
			return false, err
		}
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	// When we seeked into the middle of a line, the first Scan() returns a
	// fragment. Skip it — any real duplicate in that fragment would also
	// exist in the next full line(s) since dedup events are rewritten, not
	// partially embedded.
	if offset > 0 && scanner.Scan() {
		// Discard the first (partial) line.
		_ = scanner.Text()
	}
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), key) {
			return true, nil
		}
	}
	return false, scanner.Err()
}
