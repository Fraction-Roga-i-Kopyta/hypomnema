// Package rank is the v2 relevance ranker — the merged injection pipeline's
// core. It is a pure, I/O-free leaf: no imports of sidecar/native, so it is
// trivially unit-testable and A/B-able (Phase 2b). The caller maps storage
// records into Candidate. Scoring is overlap + recency + project boost only:
// RefCount and Effectiveness stay on Candidate as diagnostics but never enter
// the score. Zero-safe: no single zero signal annihilates a candidate.
package rank

import (
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
	RefCount      int     // diagnostic only; not read by the score
	Effectiveness float64 // diagnostic only; not read by the score
	Status        string
	Created       string // YYYY-MM-DD, may be ""
	LastInjected  string // YYYY-MM-DD, may be ""
	LastUseful    string // YYYY-MM-DD, may be "" — latest cite-useful citation (v2.12; formerly trigger-useful)
	Overlap       int
	// Hidden drops the candidate (a /memboard hide for this project); Less
	// halves its score. Set by push injection only — pull paths leave both
	// false.
	Hidden bool
	Less   bool
}

// Query carries the relevance signal for one ranking call.
type Query struct {
	Terms   []string // caller uses these to populate each Candidate.Overlap; Rank itself does not read Terms
	Project string
	Domains []string
	Today   string // YYYY-MM-DD; drives recency decay
	// IncludeStale widens the status allowlist to stale rows — used by the
	// pull path (memoryctl recall), where an explicit query has no noise
	// cost and recalling a stale fact revives it. Deleted stays excluded.
	IncludeStale bool
}

// Scored is a Candidate with its computed score.
type Scored struct {
	Candidate
	Score float64
}

// Scoring weights — fixed here; Phase 2b's A/B harness calibrates them.
const (
	wOverlap     = 3.0
	wRecency     = 2.0
	projectBoost = 1.0
	// lessFactor scales the final score of a fact marked "less".
	lessFactor = 0.5
)

// Rank scores every eligible candidate and returns the top k (k<=0 = all),
// sorted by score descending, ties broken by slug ascending for
// determinism. Candidates not in the status allowlist, or failing the domain
// filter, are excluded.
func Rank(q Query, cands []Candidate, k int) []Scored {
	today := parseDay(q.Today)
	out := make([]Scored, 0, len(cands))
	for _, c := range cands {
		// Allowlist: active/pinned are always injectable; candidate (an
		// agent-written fact pending corroboration) injects normally —
		// zero-safe — and graduates via the close hook; stale joins only
		// for the pull path (Query.IncludeStale). Everything else (deleted,
		// retired, unknown) is excluded.
		allowed := c.Status == "active" || c.Status == "pinned" || c.Status == "candidate" ||
			(q.IncludeStale && c.Status == "stale")
		if !allowed || c.Hidden {
			continue
		}
		if !domainOK(q.Domains, c.Domains) {
			continue
		}
		out = append(out, Scored{Candidate: c, Score: score(q, c, today)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		// scores for identical inputs are bit-identical; a NaN score (caller error) sinks to the bottom
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
	s := wOverlap*float64(c.Overlap) + wRecency*recency(c, today)
	if q.Project != "" && c.Project == q.Project {
		s += projectBoost
	}
	if c.Less {
		s *= lessFactor
	}
	return s
}

// recency decays from 1.0 (today) toward 0 with a 30-day scale, using
// LastUseful (fallback Created). Unknown/zero date → 0 contribution.
//
// LastUseful is the model's own signal (a useful citation classified at
// close). LastInjected is deliberately NOT used: injection is the ranker's
// output, so keying recency on it made every pick refresh itself — a fact
// injected hundreds of times with near-zero effectiveness never aged
// (v2.11 live audit: ref 619 / eff 0.04 still in rotation). Created is the
// novelty fallback so a brand-new fact is injectable from day one.
func recency(c Candidate, today time.Time) float64 {
	d := c.LastUseful
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
