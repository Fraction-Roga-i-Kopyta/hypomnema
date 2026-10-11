// Package usefulness turns per-session citation observations into the
// Bayesian effectiveness the ranker reads.
//
// A cite-useful session is full positive evidence. A cite-silent session is
// only weak negative evidence: the model under-cites (it applies facts
// without tagging them), so "shown but not cited" is a noisy label. Each
// silent session therefore counts as SilentWeight of an observation.
package usefulness

// SilentWeight is how much one cite-silent session counts against a fact,
// relative to one cite-useful session counting for it.
const SilentWeight = 0.25

// Effectiveness is (pos+1)/(pos+SilentWeight·neg+2): 0.5 with no
// observations, one silent session → 0.444, ten → 0.222.
func Effectiveness(pos, neg int) float64 {
	return float64(pos+1) / (float64(pos) + SilentWeight*float64(neg) + 2)
}
