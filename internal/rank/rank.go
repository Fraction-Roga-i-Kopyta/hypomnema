// Package rank is the v2 relevance ranker — the merged injection pipeline's
// core. It is a pure, I/O-free leaf: no imports of sidecar/native, so it is
// trivially unit-testable and A/B-able (Phase 2b). The caller maps storage
// records into Candidate. Scoring is zero-safe: no single zero signal
// annihilates a candidate (spec §3.3).
package rank

import (
	"math"
	"sort"
	"time"
)

// Candidate is one rankable memory record with its static signals plus a
// precomputed keyword Overlap (count of query terms it matches).
type Candidate struct {
	Slug          string
	Type          string
	Project       string
	Domains       []string
	RefCount      int
	Effectiveness float64
	Status        string
	Created       string // YYYY-MM-DD, may be ""
	LastInjected  string // YYYY-MM-DD, may be ""
	Overlap       int
}

// Query carries the relevance signal for one ranking call.
type Query struct {
	Terms   []string
	Project string
	Domains []string
	Today   string // YYYY-MM-DD; drives recency decay
}

// Scored is a Candidate with its computed score.
type Scored struct {
	Candidate
	Score float64
}

// Scoring weights — fixed here; Phase 2b's A/B harness calibrates them.
const (
	wOverlap     = 3.0
	wRef         = 1.0
	wRecency     = 2.0
	wEffective   = 2.0
	projectBoost = 1.0
)

// Rank scores every eligible candidate and returns the top k (k<=0 = all),
// sorted by score descending, ties broken by slug ascending for
// determinism. Candidates with status stale/deleted, or failing the domain
// filter, are excluded.
func Rank(q Query, cands []Candidate, k int) []Scored {
	today := parseDay(q.Today)
	out := make([]Scored, 0, len(cands))
	for _, c := range cands {
		if c.Status == "stale" || c.Status == "deleted" {
			continue
		}
		if !domainOK(q.Domains, c.Domains) {
			continue
		}
		out = append(out, Scored{Candidate: c, Score: score(q, c, today)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Slug < out[j].Slug
	})
	if k > 0 && len(out) > k {
		out = out[:k]
	}
	return out
}

func score(q Query, c Candidate, today time.Time) float64 {
	s := wOverlap*float64(c.Overlap) +
		wRef*math.Log10(1+float64(c.RefCount)) +
		wRecency*recency(c, today) +
		wEffective*c.Effectiveness
	if q.Project != "" && c.Project == q.Project {
		s += projectBoost
	}
	return s
}

// recency decays from 1.0 (today) toward 0 with a 30-day scale, using
// LastInjected (fallback Created). Unknown/zero date → 0 contribution.
func recency(c Candidate, today time.Time) float64 {
	d := c.LastInjected
	if d == "" {
		d = c.Created
	}
	t := parseDay(d)
	if t.IsZero() || today.IsZero() {
		return 0
	}
	days := today.Sub(t).Hours() / 24.0
	if days < 0 {
		days = 0
	}
	return 1.0 / (1.0 + days/30.0)
}

func parseDay(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// domainOK: an untagged candidate (no domains) or an unfiltered query (no
// domains) is always eligible; otherwise require a non-empty intersection.
func domainOK(queryDomains, candDomains []string) bool {
	if len(candDomains) == 0 || len(queryDomains) == 0 {
		return true
	}
	for _, a := range queryDomains {
		for _, b := range candDomains {
			if a == b {
				return true
			}
		}
	}
	return false
}
