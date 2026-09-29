package wal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAppendWritesAndDedupes covers the two behaviours that matter:
// - a fresh event is written
// - an event whose dedup key already appears in the WAL is a no-op
func TestAppendWritesAndDedupes(t *testing.T) {
	dir := t.TempDir()

	if err := AppendStrict(dir, "2026-01-01|inject|slug-a|sess1", "inject|slug-a|sess1"); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := AppendStrict(dir, "2026-01-01|inject|slug-a|sess1", "inject|slug-a|sess1"); err != nil {
		t.Fatalf("duplicate append: %v", err)
	}
	if err := AppendStrict(dir, "2026-01-01|inject|slug-b|sess1", "inject|slug-b|sess1"); err != nil {
		t.Fatalf("second slug append: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, ".wal"))
	if err != nil {
		t.Fatalf("read wal: %v", err)
	}
	got := strings.Count(string(body), "\n")
	if got != 2 {
		t.Errorf("expected 2 lines after dedup, got %d:\n%s", got, body)
	}
}

func TestAppendNoDedupKey(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		_ = AppendStrict(dir, "2026-01-01|error-detect|npm:deprecation|s", "")
	}
	body, _ := os.ReadFile(filepath.Join(dir, ".wal"))
	if strings.Count(string(body), "\n") != 3 {
		t.Errorf("empty dedup key must NOT dedupe; got %d lines:\n%s", strings.Count(string(body), "\n"), body)
	}
}

func TestAppendSuffixUnique_EndAnchored(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	os.WriteFile(walPath, []byte("2026-09-29|cite-useful|p\x1fdocker.md|s1:a1\n"), 0o644)
	line, key := "2026-09-29|cite-useful|p\x1fdocker.md|s1", "|cite-useful|p\x1fdocker.md|s1"
	AppendSuffixUnique(dir, line, key) // not a duplicate: the existing row ends in s1:a1
	AppendSuffixUnique(dir, line, key) // exact duplicate: skipped
	b, _ := os.ReadFile(walPath)
	if n := strings.Count(string(b), "docker.md|s1\n"); n != 1 {
		t.Fatalf("want exactly one s1 row, got %d:\n%s", n, b)
	}
}
