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

// containsLine is the Go equivalent of `grep -qF $dedupKey $wal` — substring
// match (not regex), line-by-line so huge WALs stream through a buffer rather
// than loading whole-file.
func containsLine(walPath, key string) (bool, error) {
	f, err := os.Open(walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Memory files can be long; bump the line buffer from the 64 KB default.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), key) {
			return true, nil
		}
	}
	return false, scanner.Err()
}
