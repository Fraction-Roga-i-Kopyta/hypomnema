package rank

import "testing"

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

func TestRank_MonotonicInRefCountAndEffectiveness(t *testing.T) {
	q := Query{Today: "2026-05-29"}
	lo := base()
	lo.Slug = "lo"
	hi := base()
	hi.Slug = "hi"
	hi.RefCount = 100
	got := Rank(q, []Candidate{lo, hi}, 0)
	if got[0].Slug != "hi" {
		t.Errorf("higher ref_count should rank first; got %v", slugs(got))
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
		c.RefCount = n
		cands = append(cands, c)
	}
	got := Rank(q, cands, 3)
	if len(got) != 3 {
		t.Fatalf("k=3 should cap to 3, got %d", len(got))
	}
	if got[0].Slug != "e" {
		t.Errorf("expected highest-ref first; got %v", slugs(got))
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

func TestRank_MonotonicInEffectiveness(t *testing.T) {
	q := Query{Today: "2026-05-29"}
	lo := base()
	lo.Slug = "lo"
	lo.Effectiveness = 0.2
	hi := base()
	hi.Slug = "hi"
	hi.Effectiveness = 0.9
	got := Rank(q, []Candidate{lo, hi}, 0)
	if got[0].Slug != "hi" {
		t.Errorf("higher effectiveness should rank first; got %v", slugs(got))
	}
}

func TestRank_RecencyOrdering(t *testing.T) {
	q := Query{Today: "2026-05-29"}
	recent := base()
	recent.Slug = "recent"
	recent.LastInjected = "2026-05-28"
	old := base()
	old.Slug = "old"
	old.LastInjected = "2026-01-01"
	got := Rank(q, []Candidate{old, recent}, 0)
	if got[0].Slug != "recent" {
		t.Errorf("more recently injected should rank first; got %v", slugs(got))
	}
}

func slugs(s []Scored) []string {
	out := make([]string, len(s))
	for i := range s {
		out[i] = s[i].Slug
	}
	return out
}

func TestRankIncludeStale(t *testing.T) {
	cands := []Candidate{
		{Slug: "live.md", Status: "active", Overlap: 1},
		{Slug: "old.md", Status: "stale", Overlap: 5},
		{Slug: "gone.md", Status: "deleted", Overlap: 9},
	}
	q := Query{Today: "2026-06-10"}

	got := Rank(q, cands, 0)
	if len(got) != 1 || got[0].Slug != "live.md" {
		t.Fatalf("default rank must exclude stale/deleted, got %+v", got)
	}

	q.IncludeStale = true
	got = Rank(q, cands, 0)
	if len(got) != 2 {
		t.Fatalf("IncludeStale: want active+stale (2), got %+v", got)
	}
	for _, sc := range got {
		if sc.Slug == "gone.md" {
			t.Fatal("deleted must never rank, even with IncludeStale")
		}
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
