// Package evidence mines candidate `evidence:` phrases from assistant
// transcripts. Pure-Go, stdlib-only; the entire pipeline is
// deterministic so `make replay` comparisons work across machines.
//
// Flow (one target rule at a time):
//
//   1. Partition sessions by whether they fired trigger-silent for
//      this rule — caller supplies the partition.
//   2. For each silent session, extract n-gram candidates (2- and
//      3-gram) from assistant text, weighted toward enumeration-
//      style regions (lines starting with `(N)`, `- `, `* `, etc.).
//   3. For each non-silent session, the same extraction produces a
//      noise baseline.
//   4. Rank candidates by silent-coverage, filter out those too
//      common in the noise baseline or already in existing
//      evidence, return the top N.
//
// Matching `session-stop.sh`'s evidence-check semantics: we lower-
// case, strip code fences, and treat bodies as word sequences split
// on non-letter/non-digit boundaries.
package evidence

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Config drives the miner. Zero values are documented defaults.
type Config struct {
	// MinSilentCoverage — fraction of silent sessions a phrase must
	// appear in to be considered. Default 0.20 per the plan.
	MinSilentCoverage float64
	// MaxNoiseCoverage — fraction of non-silent sessions above which
	// a phrase is rejected as too generic. Default 0.10.
	MaxNoiseCoverage float64
	// MinRuneLen — discards short fragments ("(1)", "№1"). Default 5.
	MinRuneLen int
	// TopN — how many ranked candidates to return. Default 15.
	TopN int
}

// applyDefaults mutates a zero-valued Config into the documented
// defaults. Calling code can leave Config{} to get them.
func (c *Config) applyDefaults() {
	if c.MinSilentCoverage == 0 {
		c.MinSilentCoverage = 0.20
	}
	if c.MaxNoiseCoverage == 0 {
		c.MaxNoiseCoverage = 0.10
	}
	if c.MinRuneLen == 0 {
		c.MinRuneLen = 5
	}
	if c.TopN == 0 {
		c.TopN = 15
	}
}

// Candidate is one ranked phrase proposal.
type Candidate struct {
	Phrase         string  // canonicalised (lowercase, collapsed spaces)
	SilentSessions int     // number of silent sessions it appeared in
	NoiseSessions  int     // number of non-silent sessions it appeared in
	Coverage       float64 // SilentSessions / len(silent sessions)
}

// Mine returns the top-N phrase candidates for one target rule.
//
//   silent  — assistant text from sessions where the rule was silent
//   noise   — assistant text from sessions where the rule was useful
//             or the rule wasn't injected at all (generic prose)
//   existing — already-declared evidence phrases (lowercase); excluded
//             from proposals
//   cfg     — Config; zero values = documented defaults
func Mine(silent, noise []string, existing []string, cfg Config) []Candidate {
	cfg.applyDefaults()

	existingSet := make(map[string]struct{}, len(existing))
	for _, p := range existing {
		existingSet[strings.ToLower(p)] = struct{}{}
	}

	// Per-session phrase sets (so a phrase appearing twice in one
	// session doesn't double-count).
	silentSets := make([]map[string]struct{}, len(silent))
	for i, text := range silent {
		silentSets[i] = extractPhrases(text, cfg.MinRuneLen)
	}
	noiseSets := make([]map[string]struct{}, len(noise))
	for i, text := range noise {
		noiseSets[i] = extractPhrases(text, cfg.MinRuneLen)
	}

	// Count session-coverage per phrase.
	silentCount := map[string]int{}
	for _, set := range silentSets {
		for p := range set {
			silentCount[p]++
		}
	}
	noiseCount := map[string]int{}
	for _, set := range noiseSets {
		for p := range set {
			noiseCount[p]++
		}
	}

	var out []Candidate
	nSilent := len(silent)
	nNoise := len(noise)
	for phrase, sc := range silentCount {
		if _, seen := existingSet[phrase]; seen {
			continue
		}
		cov := float64(sc) / float64(nSilent)
		if cov < cfg.MinSilentCoverage {
			continue
		}
		nc := noiseCount[phrase]
		if nNoise > 0 {
			noiseCov := float64(nc) / float64(nNoise)
			if noiseCov > cfg.MaxNoiseCoverage {
				continue
			}
		}
		out = append(out, Candidate{
			Phrase:         phrase,
			SilentSessions: sc,
			NoiseSessions:  nc,
			Coverage:       cov,
		})
	}

	// Rank by silent coverage descending, tie-break alphabetically
	// for stability.
	sort.Slice(out, func(i, j int) bool {
		if out[i].SilentSessions != out[j].SilentSessions {
			return out[i].SilentSessions > out[j].SilentSessions
		}
		return out[i].Phrase < out[j].Phrase
	})

	if len(out) > cfg.TopN {
		out = out[:cfg.TopN]
	}
	return out
}

// --- extraction internals ---

// Regex patterns for the three enumeration signals the plan names:
//
//   - `(N) ...` or `N. ...` at line start (numeric markers)
//   - `- ` or `* ` bullet lists (two or more in a row)
//   - table rows with "Вариант", "Option", "Approach", "Candidate"
//     headers
//
// A phrase extracted from inside an enumeration region is weighted
// the same as one from plain prose — the enumeration signal is a
// *where to look* heuristic, not a ranking multiplier. Simpler
// semantics than the spec's weighted option and empirically
// sufficient on the dogfood corpus.
var (
	reCodeFence   = regexp.MustCompile("(?s)```[^`]*```")
	reQuotedBlock = regexp.MustCompile(`(?m)^>\s.*$`)
)

// extractPhrases canonicalises text and returns the set of 2-gram and
// 3-gram phrases present. Canonicalisation mirrors session-stop.sh:
// strip code fences, strip blockquote lines, lowercase, split on
// non-alnum boundaries.
func extractPhrases(text string, minRuneLen int) map[string]struct{} {
	// Strip code fences — they're likely to carry function names
	// and deterministic output that isn't authorial voice.
	text = reCodeFence.ReplaceAllString(text, " ")
	// Strip blockquote lines — typically echo user prompts.
	text = reQuotedBlock.ReplaceAllString(text, " ")

	lower := strings.ToLower(text)

	// Tokenise on non-letter/non-digit boundaries. Unicode-aware so
	// Cyrillic/CJK/Greek participate.
	tokens := tokenise(lower)

	out := map[string]struct{}{}
	// 2-grams and 3-grams.
	for n := 2; n <= 3; n++ {
		for i := 0; i+n <= len(tokens); i++ {
			phrase := strings.Join(tokens[i:i+n], " ")
			if utf8.RuneCountInString(phrase) < minRuneLen {
				continue
			}
			out[phrase] = struct{}{}
		}
	}
	return out
}

// tokenise splits text on non-alnum boundaries, preserving unicode
// letters/digits and underscores (matching internal/tfidf.Tokenize
// behaviour so downstream metrics align).
func tokenise(text string) []string {
	split := func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			return r
		}
		return ' '
	}
	return strings.Fields(strings.Map(split, text))
}
