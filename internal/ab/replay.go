package ab

import (
	"math/rand"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/rank"
)

// Result is the mean recall@K for one budget K across all eval sessions.
type Result struct {
	K            int
	RankedRecall float64
	RandomRecall float64
	Sessions     int
}

// Replay runs the temporal-holdout replay over every eval session and returns
// one Result per K (in the order of `ks`). `seed` makes the random baseline
// reproducible.
func Replay(events []Event, ks []int, seed int64) []Result {
	pool := CandidatePool(events)
	sessions := EvalSessions(events)
	rng := rand.New(rand.NewSource(seed))

	results := make([]Result, len(ks))
	for i, k := range ks {
		results[i] = Result{K: k}
	}

	for _, s := range sessions {
		if len(s.Useful) == 0 {
			continue
		}
		sig := SignalsBefore(events, s.Date)
		// Project/Domains left empty: the WAL carries no per-slug project or
		// domain metadata, so project-boost and domain-filtering are inert in
		// this replay. Results therefore UNDERESTIMATE the ranker's lift for
		// same-project candidates — a floor, not a ceiling.
		cands := make([]rank.Candidate, 0, len(pool))
		for _, slug := range pool {
			g := sig[slug]
			cands = append(cands, rank.Candidate{
				Slug:          slug,
				Status:        "active",
				RefCount:      g.RefCount,
				Effectiveness: float64(g.Pos+1) / float64(g.Pos+g.Neg+2),
				LastInjected:  g.LastInject,
				Overlap:       0, // historical prompt keywords unavailable
			})
		}
		q := rank.Query{Today: s.Date}
		for i, k := range ks {
			rankedSlugs := topSlugs(rank.Rank(q, cands, k))
			randomSlugs := randomPick(pool, k, rng)
			results[i].RankedRecall += recall(rankedSlugs, s.Useful)
			results[i].RandomRecall += recall(randomSlugs, s.Useful)
			results[i].Sessions++
		}
	}
	for i := range results {
		if results[i].Sessions > 0 {
			n := float64(results[i].Sessions)
			results[i].RankedRecall /= n
			results[i].RandomRecall /= n
		}
	}
	return results
}

func topSlugs(scored []rank.Scored) []string {
	out := make([]string, len(scored))
	for i := range scored {
		out[i] = scored[i].Slug
	}
	return out
}

// randomPick returns up to k distinct slugs from pool using rng (Fisher-Yates
// on a copy so the source pool order is preserved across sessions).
// When k >= len(pool), the full (shuffled) pool is returned.
func randomPick(pool []string, k int, rng *rand.Rand) []string {
	cp := make([]string, len(pool))
	copy(cp, pool)
	rng.Shuffle(len(cp), func(i, j int) { cp[i], cp[j] = cp[j], cp[i] })
	if k < len(cp) {
		cp = cp[:k]
	}
	return cp
}

// recall = |selected ∩ useful| / |useful|.
func recall(selected []string, useful map[string]bool) float64 {
	if len(useful) == 0 {
		return 0
	}
	hit := 0
	for _, s := range selected {
		if useful[s] {
			hit++
		}
	}
	return float64(hit) / float64(len(useful))
}
