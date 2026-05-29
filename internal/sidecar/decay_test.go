package sidecar

import (
	"path/filepath"
	"testing"
)

func TestMarkStale(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustUpsert(t, s, Record{Slug: "old-fb.md", Type: "feedback", Created: "2026-01-01", Status: "active"})
	mustUpsert(t, s, Record{Slug: "fresh-mi.md", Type: "mistake", Created: "2026-05-20", Status: "active"})
	mustUpsert(t, s, Record{Slug: "pinned.md", Type: "note", Created: "2020-01-01", Status: "pinned"})

	n, err := s.MarkStale("2026-05-29")
	if err != nil {
		t.Fatalf("MarkStale: %v", err)
	}
	if n != 1 {
		t.Errorf("marked %d stale, want 1 (old feedback only)", n)
	}
	if r, _, _ := s.Get("old-fb.md"); r.Status != "stale" {
		t.Errorf("old-fb status = %q, want stale", r.Status)
	}
	if r, _, _ := s.Get("fresh-mi.md"); r.Status != "active" {
		t.Errorf("fresh-mi status = %q, want active", r.Status)
	}
	if r, _, _ := s.Get("pinned.md"); r.Status != "pinned" {
		t.Errorf("pinned status = %q, want pinned (never decays)", r.Status)
	}
}

func mustUpsert(t *testing.T, s *Store, r Record) {
	t.Helper()
	if err := s.Upsert(r); err != nil {
		t.Fatal(err)
	}
}
