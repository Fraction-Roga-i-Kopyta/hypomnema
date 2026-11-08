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

// The pre-compaction backup is the only way back, so it is never replaced:
// a rerun with nothing to drop changes nothing at all, and a later run on
// the same day keeps the first backup and writes its own beside it.
func TestCompact_RerunNeverReplacesBackup(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	first := filepath.Join(dir, ".wal.pre-compact-2026-11-09")
	os.WriteFile(walPath, []byte(compactFixture), 0o644)
	if _, err := Compact(dir, "2026-11-09", false); err != nil {
		t.Fatal(err)
	}

	rep, err := Compact(dir, "2026-11-09", false)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if rep.Backup != "" || len(rep.Dropped) != 0 {
		t.Errorf("nothing to drop must write nothing, report %+v", rep)
	}
	if b, _ := os.ReadFile(first); string(b) != compactFixture {
		t.Errorf("first backup must still hold the original WAL")
	}

	f, _ := os.OpenFile(walPath, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("2026-11-09|trigger-silent|x.md|s9\n")
	f.Close()
	rep, err = Compact(dir, "2026-11-09", false)
	if err != nil {
		t.Fatalf("third run: %v", err)
	}
	if rep.Backup != first+"-2" {
		t.Errorf("same-day backup must get a suffix, got %q", rep.Backup)
	}
	if b, _ := os.ReadFile(first); string(b) != compactFixture {
		t.Errorf("first backup was overwritten")
	}
}

// Kept rows are copied as raw bytes (a CRLF row stays CRLF, a last row
// without a newline stays without one) and the WAL keeps its file mode.
func TestCompact_PreservesBytesAndMode(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	in := "2026-09-01|inject|a.md|s1\r\n2026-09-01|trigger-silent|a.md|s1\r\n2026-09-02|inject|b.md|s2"
	os.WriteFile(walPath, []byte(in), 0o640)
	os.Chmod(walPath, 0o640)
	if _, err := Compact(dir, "2026-11-09", false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(walPath)
	if want := "2026-09-01|inject|a.md|s1\r\n2026-09-02|inject|b.md|s2"; string(got) != want {
		t.Errorf("compacted = %q, want %q", got, want)
	}
	if fi, _ := os.Stat(walPath); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", fi.Mode().Perm())
	}
}

// A compaction killed mid-way leaves a WAL-sized temp file; the next run
// removes it.
func TestCompact_RemovesLeftoverTemp(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".wal"), []byte(compactFixture), 0o644)
	stale := filepath.Join(dir, ".wal.compact.123.tmp")
	os.WriteFile(stale, []byte("half"), 0o600)
	if _, err := Compact(dir, "2026-11-09", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Errorf("leftover temp file must be removed")
	}
}
