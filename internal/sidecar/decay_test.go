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

// Staleness must follow actual use, not just file age: a fact injected
// recently is alive regardless of when it was created.
func TestMarkStale_UsesLastInjectedOverCreated(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustUpsert(t, s, Record{Slug: "alive.md", Type: "note",
		Created: "2026-03-01", LastInjected: "2026-09-20", Status: "active"})
	mustUpsert(t, s, Record{Slug: "dead.md", Type: "note",
		Created: "2026-03-01", LastInjected: "2026-04-01", Status: "active"})

	if _, err := s.MarkStale("2026-10-01"); err != nil {
		t.Fatalf("MarkStale: %v", err)
	}
	if r, _, _ := s.Get("alive.md"); r.Status != "active" {
		t.Errorf("recently-injected fact must stay active, got %q", r.Status)
	}
	if r, _, _ := s.Get("dead.md"); r.Status != "stale" {
		t.Errorf("long-unused fact must go stale, got %q", r.Status)
	}
}

func TestSkillLearningDecayThreshold(t *testing.T) {
	if got := staleDays["skill-learning"]; got != 120 {
		t.Fatalf("want skill-learning staleDays=120, got %d", got)
	}
}

// continuity/project facts are "where we left off" markers — they must
// never rotate out by age (CLAUDE.md lifecycle contract).
func TestMarkStale_ExemptsContinuityAndProject(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustUpsert(t, s, Record{Slug: "cont.md", Type: "continuity", Created: "2020-01-01", Status: "active"})
	mustUpsert(t, s, Record{Slug: "proj.md", Type: "project", Created: "2020-01-01", Status: "active"})

	if _, err := s.MarkStale("2026-06-09"); err != nil {
		t.Fatal(err)
	}
	if r, _, _ := s.Get("cont.md"); r.Status != "active" {
		t.Errorf("continuity must never rotate, got %q", r.Status)
	}
	if r, _, _ := s.Get("proj.md"); r.Status != "active" {
		t.Errorf("project must never rotate, got %q", r.Status)
	}
}
