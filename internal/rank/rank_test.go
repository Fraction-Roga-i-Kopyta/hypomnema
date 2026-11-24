package rank

import (
	"testing"
)

func base() Candidate {
	return Candidate{Slug: "x", Status: "active", RefCount: 0, Effectiveness: 0.5, Created: "2026-05-01"}
}

func TestRank_OverlapDominates(t *testing.T) {
	q := Query{Terms: []string{"docker"}, Today: "2026-05-29"}
	a := base()
	a.Slug = "a"
	a.Overlap = 2
	b := base()
	b.Slug = "b"
	b.Overlap = 0
	got := Rank(q, []Candidate{b, a}, 0)
	if len(got) != 2 || got[0].Slug != "a" {
		t.Fatalf("higher overlap should rank first; got %v", slugs(got))
	}
}

func TestRank_StatusAllowlist(t *testing.T) {
	q := Query{Today: "2026-05-29"}
	mk := func(slug, status string) Candidate {
		c := base()
		c.Slug = slug
		c.Status = status
		return c
	}
	got := Rank(q, []Candidate{
		mk("active", "active"),
		mk("pinned", "pinned"),
		mk("stale", "stale"),
		mk("deleted", "deleted"),
		mk("archived", "archived"),
	}, 0)
	if !has(got, "active") || !has(got, "pinned") {
		t.Error("active and pinned must be injectable")
	}
	for _, bad := range []string{"stale", "deleted", "archived"} {
		if has(got, bad) {
			t.Errorf("%s must be excluded (allowlist)", bad)
		}
	}
}

func TestRank_ZeroSafe_NewFactStillRanks(t *testing.T) {
	q := Query{Today: "2026-05-29"}
	// Fully-zero signal: overlap 0, ref 0, effectiveness 0, no dates.
	zero := base()
	zero.Slug = "zero"
	zero.Effectiveness = 0
	zero.Created = ""
	zero.LastInjected = ""
	zero.LastUseful = ""
	got := Rank(q, []Candidate{zero}, 0)
	if len(got) != 1 {
		t.Fatalf("zero-signal fact must still be rankable, got %d", len(got))
	}
	if got[0].Score < 0 {
		t.Errorf("score must be >= 0 for a zero-signal fact, got %v", got[0].Score)
	}
}

func TestRank_ProjectBoost(t *testing.T) {
	q := Query{Project: "proj", Today: "2026-05-29"}
	in := base()
	in.Slug = "in"
	in.Project = "proj"
	out := base()
	out.Slug = "out"
	out.Project = "other"
	got := Rank(q, []Candidate{out, in}, 0)
	if got[0].Slug != "in" {
		t.Errorf("project match should boost; got %v", slugs(got))
	}
}

func TestRank_DomainFilter(t *testing.T) {
	q := Query{Domains: []string{"backend"}, Today: "2026-05-29"}
	match := base()
	match.Slug = "match"
	match.Domains = []string{"backend", "db"}
	miss := base()
	miss.Slug = "miss"
	miss.Domains = []string{"frontend"}
	untagged := base()
	untagged.Slug = "untagged"
	got := Rank(q, []Candidate{match, miss, untagged}, 0)
	if has(got, "miss") {
		t.Error("domain mismatch must be filtered out")
	}
	if !has(got, "match") || !has(got, "untagged") {
		t.Error("matching + untagged must survive domain filter")
	}
}

func TestRank_CapK(t *testing.T) {
	q := Query{Today: "2026-05-29"}
	var cands []Candidate
	for i, n := range []int{1, 2, 3, 4, 5} {
		c := base()
		c.Slug = string(rune('a' + i))
		c.Overlap = n
		cands = append(cands, c)
	}
	got := Rank(q, cands, 3)
	if len(got) != 3 {
		t.Fatalf("k=3 should cap to 3, got %d", len(got))
	}
	if got[0].Slug != "e" {
		t.Errorf("expected highest-overlap first; got %v", slugs(got))
	}
}

func TestRank_DeterministicTieBreak(t *testing.T) {
	q := Query{Today: "2026-05-29"}
	a := base()
	a.Slug = "b"
	c := base()
	c.Slug = "a"
	got := Rank(q, []Candidate{a, c}, 0)
	if got[0].Slug != "a" {
		t.Errorf("tie must break by slug asc; got %v", slugs(got))
	}
}

func TestRank_RecencyOrdering(t *testing.T) {
	q := Query{Today: "2026-05-29"}
	recent := base()
	recent.Slug = "recent"
	recent.LastUseful = "2026-05-28"
	old := base()
	old.Slug = "old"
	old.LastUseful = "2026-01-01"
	got := Rank(q, []Candidate{old, recent}, 0)
	if got[0].Slug != "recent" {
		t.Errorf("more recently useful should rank first; got %v", slugs(got))
	}
}

func TestRank_IgnoresEffectivenessAndRefCount(t *testing.T) {
	base := Candidate{Status: "active", Overlap: 1, Created: "2026-11-01"}
	hi, lo := base, base
	hi.Slug, hi.Effectiveness, hi.RefCount = "a.md", 0.95, 900
	lo.Slug, lo.Effectiveness, lo.RefCount = "b.md", 0.05, 0
	got := Rank(Query{Today: "2026-11-24"}, []Candidate{lo, hi}, 0)
	if got[0].Score != got[1].Score {
		t.Fatalf("eff/ref must not change the score: %v vs %v", got[0].Score, got[1].Score)
	}
	if got[0].Slug != "a.md" {
		t.Fatalf("tie-break by slug, got %s first", got[0].Slug)
	}
}

func TestRank_ScoreIsOverlapRecencyProject(t *testing.T) {
	c := Candidate{Slug: "x.md", Status: "active", Overlap: 2, Created: "2026-11-24",
		Project: "p", Effectiveness: 0.5, RefCount: 9}
	got := Rank(Query{Today: "2026-11-24", Project: "p"}, []Candidate{c}, 0)
	if want := 3.0*2 + 2.0*1 + 1.0; got[0].Score != want { // old formula: 11
		t.Fatalf("score = %v, want %v", got[0].Score, want)
	}
}

func slugs(s []Scored) []string {
	out := make([]string, len(s))
	for i := range s {
		out[i] = s[i].Slug
	}
	return out
}

func TestRank_IncludeStale(t *testing.T) {
	cands := []Candidate{
		{Slug: "live.md", Status: "active", Overlap: 1},
		{Slug: "old.md", Status: "stale", Overlap: 5},
		{Slug: "gone.md", Status: "deleted", Overlap: 9},
		{Slug: "pin.md", Status: "pinned", Overlap: 2},
	}
	q := Query{Today: "2026-06-10"}

	got := Rank(q, cands, 0)
	if len(got) != 2 {
		t.Fatalf("default rank must exclude stale/deleted, want active+pinned (2), got %+v", got)
	}
	if has(got, "old.md") || has(got, "gone.md") {
		t.Fatalf("default rank must exclude stale/deleted, got %+v", got)
	}

	q.IncludeStale = true
	got = Rank(q, cands, 0)
	if len(got) != 3 {
		t.Fatalf("IncludeStale: want active+pinned+stale (3), got %+v", got)
	}
	for _, sc := range got {
		if sc.Slug == "gone.md" {
			t.Fatal("deleted must never rank, even with IncludeStale")
		}
	}
	if got[0].Slug != "old.md" {
		t.Errorf("IncludeStale: higher-overlap stale fact must rank first, got %+v", got)
	}
}

func has(s []Scored, slug string) bool {
	for _, x := range s {
		if x.Slug == slug {
			return true
		}
	}
	return false
}

// TestRank_CandidateInjectable: candidate facts rank like active ones —
// the soft-corroboration path keeps zero-safe injection.
func TestRank_CandidateInjectable(t *testing.T) {
	got := Rank(Query{Today: "2026-07-23"}, []Candidate{
		{Slug: "c.md", Status: "candidate", Effectiveness: 0.5},
	}, 0)
	if len(got) != 1 {
		t.Fatalf("candidate excluded from ranking: %v", got)
	}
}

// Recency follows the model's use (LastUseful), not the ranker's own output
// (LastInjected): a fact injected today but never useful must not outrank an
// equal fact last useful last week just by having been injected.
func TestRank_RecencyUsesLastUsefulNotLastInjected(t *testing.T) {
	q := Query{Today: "2026-08-16"}
	injectedToday := base()
	injectedToday.Slug = "injected-today"
	injectedToday.Created = "2026-01-01"
	injectedToday.LastInjected = "2026-08-16"
	injectedToday.LastUseful = ""

	usefulLastWeek := base()
	usefulLastWeek.Slug = "useful-last-week"
	usefulLastWeek.Created = "2026-01-01"
	usefulLastWeek.LastInjected = "2026-01-02"
	usefulLastWeek.LastUseful = "2026-08-09"

	got := Rank(q, []Candidate{injectedToday, usefulLastWeek}, 0)
	if got[0].Slug != "useful-last-week" {
		t.Fatalf("want useful-last-week first, got %v", []string{got[0].Slug, got[1].Slug})
	}
	// And the injected-today fact scores exactly like a fact with no dates
	// but the same Created — LastInjected contributes nothing.
	same := injectedToday
	same.Slug = "same-created-no-inject"
	same.LastInjected = ""
	r := Rank(q, []Candidate{injectedToday, same}, 0)
	if r[0].Score != r[1].Score {
		t.Errorf("LastInjected must not move the score: %v vs %v", r[0].Score, r[1].Score)
	}
}
