package rank

import "testing"

func TestRank_HiddenExcluded(t *testing.T) {
	q := Query{Today: "2026-05-29"}
	got := Rank(q, []Candidate{
		{Slug: "a.md", Status: "active", Effectiveness: 0.5, Hidden: true},
		{Slug: "b.md", Status: "active", Effectiveness: 0.5},
	}, 0)
	if len(got) != 1 || got[0].Slug != "b.md" {
		t.Fatalf("hidden must be dropped, got %+v", got)
	}
}

func TestRank_LessHalvesScore(t *testing.T) {
	q := Query{Today: "2026-05-29", Project: "p"}
	base := Candidate{Slug: "a.md", Project: "p", Status: "active", Effectiveness: 0.5, Created: "2026-05-20", Overlap: 2, RefCount: 5}
	less := base
	less.Less = true
	b := Rank(q, []Candidate{base}, 0)[0].Score
	l := Rank(q, []Candidate{less}, 0)[0].Score
	if b <= 0 || l/b < 0.4999 || l/b > 0.5001 {
		t.Fatalf("less ratio = %v (base %v, less %v), want 0.5", l/b, b, l)
	}
}
