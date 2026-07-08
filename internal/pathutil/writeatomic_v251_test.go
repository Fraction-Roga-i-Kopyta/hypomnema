package pathutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.list")
	if err := WriteFileAtomic(p, []byte("a\nb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "a\nb\n" {
		t.Errorf("content=%q", got)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("perm=%v", fi.Mode().Perm())
	}
	// No .tmp residue left behind.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected only the target file, got %d entries", len(entries))
	}
	// Overwrite works.
	if err := WriteFileAtomic(p, []byte("c\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(p)
	if string(got) != "c\n" {
		t.Errorf("overwrite content=%q", got)
	}
}
