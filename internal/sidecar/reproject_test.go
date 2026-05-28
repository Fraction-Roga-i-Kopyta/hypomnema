package sidecar

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

func TestReproject(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".sidecar.db")
	walPath := filepath.Join(dir, ".wal")

	wal := "" +
		"2026-04-01|inject|m.md|s1\n" +
		"2026-04-02|inject|m.md|s2\n" +
		"2026-04-02|outcome-positive|m.md|s2\n" +
		"2026-04-03|inject|m.md|s3\n" +
		"2026-04-03|outcome-negative|m.md|s3\n" +
		"2026-04-01|inject|n.md|s1\n"
	if err := os.WriteFile(walPath, []byte(wal), 0o644); err != nil {
		t.Fatalf("write wal: %v", err)
	}

	files := []native.MemFile{
		{Slug: "m.md", Type: "feedback", Name: "M", ContentSHA: "sha-m"},
		{Slug: "n.md", Type: "reference", Name: "N", ContentSHA: "sha-n"},
		{Slug: "fresh.md", Type: "reference", Name: "F", ContentSHA: "sha-f"},
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if err := Reproject(s, files, walPath); err != nil {
		t.Fatalf("Reproject: %v", err)
	}

	m, ok, _ := s.Get("m.md")
	if !ok {
		t.Fatal("m.md missing")
	}
	if m.RefCount != 3 {
		t.Errorf("m.md ref_count = %d, want 3", m.RefCount)
	}
	if m.Effectiveness != 0.5 {
		t.Errorf("m.md effectiveness = %v, want 0.5", m.Effectiveness)
	}
	if m.Created != "2026-04-01" || m.LastInjected != "2026-04-03" {
		t.Errorf("m.md dates: created=%q last=%q", m.Created, m.LastInjected)
	}

	fresh, ok, _ := s.Get("fresh.md")
	if !ok {
		t.Fatal("fresh.md missing — orphan native files must be inserted")
	}
	if fresh.RefCount != 0 || fresh.Effectiveness != 0.5 {
		t.Errorf("fresh.md = %+v, want ref_count 0 / effectiveness 0.5", fresh)
	}
}

func TestReproject_IsDeterministic(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	os.WriteFile(walPath, []byte("2026-04-01|inject|m.md|s1\n"), 0o644)
	files := []native.MemFile{{Slug: "m.md", ContentSHA: "x"}}

	first := reprojectInto(t, filepath.Join(dir, "a.db"), files, walPath)
	second := reprojectInto(t, filepath.Join(dir, "b.db"), files, walPath)
	if first != second {
		t.Errorf("reproject not deterministic:\n a=%+v\n b=%+v", first, second)
	}
}

func reprojectInto(t *testing.T, dbPath string, files []native.MemFile, walPath string) Record {
	t.Helper()
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if err := Reproject(s, files, walPath); err != nil {
		t.Fatalf("Reproject: %v", err)
	}
	r, _, _ := s.Get("m.md")
	return r
}
