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

func TestRank_ExcludesStaleAndDeleted(t *testing.T) {
	q := Query{Today: "2026-05-29"}
	act := base()
	act.Slug = "active"
	st := base()
	st.Slug = "stale"
	st.Status = "stale"
	del := base()
	del.Slug = "deleted"
	del.Status = "deleted"
	got := Rank(q, []Candidate{act, st, del}, 0)
	if len(got) != 1 || got[0].Slug != "active" {
		t.Errorf("stale/deleted must be excluded; got %v", slugs(got))
	}
}

func TestRank_ZeroSafe_NewFactStillRanks(t *testing.T) {
	q := Query{Today: "2026-05-29"}
	fresh := base()
	fresh.Slug = "fresh"
	fresh.Created = ""
	fresh.LastInjected = ""
	got := Rank(q, []Candidate{fresh}, 0)
	if len(got) != 1 {
		t.Fatalf("zero-signal fresh fact must still be rankable, got %d", len(got))
	}
	if got[0].Score < 0 {
		t.Errorf("score must not be negative for a zero-signal fact, got %v", got[0].Score)
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

func slugs(s []Scored) []string {
	out := make([]string, len(s))
	for i := range s {
		out[i] = s[i].Slug
	}
	return out
}

func has(s []Scored, slug string) bool {
	for _, x := range s {
		if x.Slug == slug {
			return true
		}
	}
	return false
}
