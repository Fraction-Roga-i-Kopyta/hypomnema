package wal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const compactFixture = "# hypomnema-wal v2\n" +
	"2026-09-01|inject|p\x1fa.md|s1\n" +
	"2026-09-01|trigger-silent|p\x1fa.md|s1\n" +
	"2026-09-01|session-metrics|domains:unknown,error_count:0|s1\n" +
	"broken row without columns\n" +
	"2026-09-02|trigger-useful|p\x1fa.md|s2\n" +
	"2026-09-02|cite-useful|p\x1fa.md|s2\n" +
	"2026-09-03|trigger-silent-retro|p\x1fb.md|s3\n" +
	"2026-09-03|outcome-positive|b.md|s3\n"

const compactWant = "# hypomnema-wal v2\n" +
	"2026-09-01|inject|p\x1fa.md|s1\n" +
	"broken row without columns\n" +
	"2026-09-02|cite-useful|p\x1fa.md|s2\n" +
	"2026-09-03|outcome-positive|b.md|s3\n"

// Compact drops only the event types no reader consumes any more; every
// other row — including a malformed one it does not understand — survives
// byte-for-byte and in order, and the original is kept as a backup.
func TestCompact_DropsLegacyRowsKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	if err := os.WriteFile(walPath, []byte(compactFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := Compact(dir, "2026-11-09", false)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	got, _ := os.ReadFile(walPath)
	if string(got) != compactWant {
		t.Errorf("compacted WAL:\n%q\nwant:\n%q", got, compactWant)
	}
	if rep.Kept != 5 || rep.Dropped["trigger-silent"] != 1 || rep.Dropped["trigger-useful"] != 1 ||
		rep.Dropped["trigger-silent-retro"] != 1 || rep.Dropped["session-metrics"] != 1 {
		t.Errorf("report = %+v", rep)
	}
	if rep.BytesBefore != int64(len(compactFixture)) || rep.BytesAfter != int64(len(compactWant)) {
		t.Errorf("bytes before/after = %d/%d", rep.BytesBefore, rep.BytesAfter)
	}
	backup, err := os.ReadFile(filepath.Join(dir, ".wal.pre-compact-2026-11-09"))
	if err != nil || string(backup) != compactFixture {
		t.Errorf("backup must hold the original bytes: err=%v", err)
	}
	if rep.Backup != filepath.Join(dir, ".wal.pre-compact-2026-11-09") {
		t.Errorf("report backup = %q", rep.Backup)
	}
	if v, err := Validate(walPath); err != nil || len(v.Failures) != 1 { // the kept broken row
		t.Errorf("compacted WAL validate: err=%v failures=%d", err, len(v.Failures))
	}
}

func TestCompact_DryRunChangesNothing(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	os.WriteFile(walPath, []byte(compactFixture), 0o644)

	rep, err := Compact(dir, "2026-11-09", true)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if got, _ := os.ReadFile(walPath); string(got) != compactFixture {
		t.Errorf("dry run must not touch the WAL")
	}
	if _, err := os.Stat(filepath.Join(dir, ".wal.pre-compact-2026-11-09")); err == nil {
		t.Errorf("dry run must not write a backup")
	}
	if rep.Kept != 5 || rep.BytesAfter != int64(len(compactWant)) || rep.Backup != "" {
		t.Errorf("dry-run report = %+v", rep)
	}
}

func TestCompact_MissingWAL(t *testing.T) {
	if _, err := Compact(t.TempDir(), "2026-11-09", false); !errors.Is(err, ErrWALMissing) {
		t.Errorf("want ErrWALMissing, got %v", err)
	}
}

// A second compaction the same day replaces that day's backup with the
// WAL as it stood before the second run, never fails on the existing file.
func TestCompact_SameDayRerunReplacesBackup(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	os.WriteFile(walPath, []byte(compactFixture), 0o644)
	if _, err := Compact(dir, "2026-11-09", false); err != nil {
		t.Fatal(err)
	}
	if _, err := Compact(dir, "2026-11-09", false); err != nil {
		t.Fatalf("second Compact: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".wal.pre-compact-2026-11-09")); string(b) != compactWant {
		t.Errorf("same-day backup must be the pre-second-run WAL, got %q", b)
	}
}
