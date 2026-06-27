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
	wRef         = 1.0
	wRecency     = 2.0
	wEffective   = 2.0
	projectBoost = 1.0
)

// Rank scores every eligible candidate and returns the top k (k<=0 = all),
// sorted by score descending, ties broken by slug ascending for
// determinism. Candidates not in the status allowlist, or failing the domain
// filter, are excluded.
func Rank(q Query, cands []Candidate, k int) []Scored {
	today := parseDay(q.Today)
	out := make([]Scored, 0, len(cands))
	for _, c := range cands {
		// Allowlist: active/pinned are always injectable; stale joins only
		// for the pull path (Query.IncludeStale). Everything else (deleted,
		// archived, superseded, unknown) is excluded.
		allowed := c.Status == "active" || c.Status == "pinned" ||
			(q.IncludeStale && c.Status == "stale")
		if !allowed {
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
	s := wOverlap*float64(c.Overlap) +
		wRef*math.Log10(1+float64(c.RefCount))*effGate(c.Effectiveness) +
		wRecency*recency(c, today) +
		wEffective*c.Effectiveness
	if q.Project != "" && c.Project == q.Project {
		s += projectBoost
	}
	return s
}

// effGate makes popularity *earned*. The ref_count term rewards
// heavily-injected facts, but a fact injected hundreds of times that rarely
// proved useful (low effectiveness) must not coast on volume — that was the
// failure mode where eff≈0.05 notes with ref in the hundreds out-ranked
// proven facts purely on injection count. The gate scales the ref reward by
// effectiveness, normalised so the Bayesian prior (0.5) is neutral (1.0) and
// capped at 1.0 so it only *damps* unearned popularity, never amplifies it
// (a single proven fact must not snowball on volume).
//
// Zero-safe by construction: a new or low-evidence fact sits near 0.5 — the
// (pos+1)/(pos+neg+2) prior pulls it there until many outcomes accrue, so an
// extreme low eff *requires* substantial negative evidence — hence its ref
// reward is untouched; only well-evidenced low-effectiveness facts are
// suppressed. Overlap, recency and the standalone effectiveness term are
// unaffected, so no candidate is annihilated (spec §3.3).
func effGate(effectiveness float64) float64 {
	g := 2.0 * effectiveness
	if g > 1.0 {
		return 1.0
	}
	if g < 0 {
		return 0
	}
	return g
}

// recency decays from 1.0 (today) toward 0 with a 30-day scale, using
// LastInjected (fallback Created). Unknown/zero date → 0 contribution.
// LastInjected reflects actual use; Created is the novelty fallback (a calibration choice Phase 2b may revisit).
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
