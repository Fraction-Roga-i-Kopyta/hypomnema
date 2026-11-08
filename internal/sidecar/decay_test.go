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

// Claude Code's own memory types (user, reference) get explicit thresholds:
// a user-identity fact is durable (180d); a reference decays like a note (90d).
func TestMarkStale_HarnessTypes(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustUpsert(t, s, Record{Slug: "who.md", Type: "user", Created: "2026-01-01", Status: "active"})       // 100d old
	mustUpsert(t, s, Record{Slug: "ref.md", Type: "reference", Created: "2026-01-01", Status: "active"})  // 100d old
	if _, err := s.MarkStale("2026-04-11"); err != nil {
		t.Fatalf("MarkStale: %v", err)
	}
	if r, _, _ := s.Get("who.md"); r.Status != "active" {
		t.Errorf("user fact at 100d must stay active (threshold 180), got %q", r.Status)
	}
	if r, _, _ := s.Get("ref.md"); r.Status != "stale" {
		t.Errorf("reference at 100d must go stale (threshold 90), got %q", r.Status)
	}
}

// A project's freshest continuity is its "where we left off" marker — it
// must never rotate out by age, however old the file.
func TestMarkStale_SoleContinuityNeverAges(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustUpsert(t, s, Record{Slug: "cont.md", Type: "continuity", Created: "2020-01-01", Status: "active"})

	if _, err := s.MarkStale("2026-06-09"); err != nil {
		t.Fatal(err)
	}
	if r, _, _ := s.Get("cont.md"); r.Status != "active" {
		t.Errorf("a project's only (hence freshest) continuity must never rotate, got %q", r.Status)
	}
}

// Continuity files pile up (one per session); only the freshest per project
// is the live "where we left off" marker and stays exempt. Older ones age
// like notes. Claude Code's own `type: project` means "ongoing work/goals",
// not a project card, so it ages like knowledge (90 days) — a card that
// must never age is pinned. Staleness is per (slug, project): a same-named
// file in another project is a different fact.
func TestMarkStale_ContinuityAndProjectTypes(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, r := range []Record{
		{Slug: "continuity.md", Project: "p", Type: "continuity", Created: "2026-01-01", Modified: "2026-03-01", Status: "active"},
		{Slug: "continuity-2026-02-01.md", Project: "p", Type: "continuity", Created: "2026-02-01", Status: "active"},
		{Slug: "continuity.md", Project: "q", Type: "continuity", Created: "2026-01-01", Status: "active"},
		{Slug: "continuity-2026-05-01.md", Project: "q", Type: "continuity", Created: "2026-05-01", Status: "active"},
		{Slug: "plan.md", Project: "p", Type: "project", Created: "2026-01-01", Status: "active"},
		{Slug: "card.md", Project: "p", Type: "project", Created: "2026-01-01", Status: "pinned"},
	} {
		mustUpsert(t, s, r)
	}

	if _, err := s.MarkStale("2026-06-15"); err != nil {
		t.Fatalf("MarkStale: %v", err)
	}
	want := map[[2]string]string{
		{"continuity.md", "p"}:            "active", // freshest in p (modified 03-01)
		{"continuity-2026-02-01.md", "p"}: "stale",
		{"continuity.md", "q"}:            "stale", // older in q — must not touch p's row
		{"continuity-2026-05-01.md", "q"}: "active",
		{"plan.md", "p"}:                  "stale",
		{"card.md", "p"}:                  "pinned",
	}
	recs, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if w, ok := want[[2]string{r.Slug, r.Project}]; ok && r.Status != w {
			t.Errorf("%s@%s status = %q, want %q", r.Slug, r.Project, r.Status, w)
		}
	}
}
