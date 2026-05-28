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

	n, ok, _ := s.Get("n.md")
	if !ok {
		t.Fatal("n.md missing")
	}
	if n.RefCount != 1 || n.Created != "2026-04-01" || n.LastInjected != "2026-04-01" {
		t.Errorf("n.md = %+v, want ref_count 1 / created+last 2026-04-01", n)
	}
}

func TestReproject_InjectAggCount(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	// one plain inject (=1) + one aggregated inject-agg of 20 -> ref_count 21
	wal := "2026-04-01|inject|x.md|s1\n2026-04-02|inject-agg|x.md|20\n"
	if err := os.WriteFile(walPath, []byte(wal), 0o644); err != nil {
		t.Fatalf("write wal: %v", err)
	}
	files := []native.MemFile{{Slug: "x.md", ContentSHA: "x"}}
	s, err := Open(filepath.Join(dir, ".sidecar.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if err := Reproject(s, files, walPath); err != nil {
		t.Fatalf("Reproject: %v", err)
	}
	r, _, _ := s.Get("x.md")
	if r.RefCount != 21 {
		t.Errorf("ref_count = %d, want 21 (1 inject + inject-agg of 20)", r.RefCount)
	}
	if r.LastInjected != "2026-04-02" {
		t.Errorf("last_injected = %q, want 2026-04-02", r.LastInjected)
	}
}

func TestReproject_IsDeterministic(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	if err := os.WriteFile(walPath, []byte("2026-04-01|inject|m.md|s1\n"), 0o644); err != nil {
		t.Fatalf("write wal: %v", err)
	}
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
