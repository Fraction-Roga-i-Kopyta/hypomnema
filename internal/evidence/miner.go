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
	// MinSilentSessions — absolute floor on silent-session count.
	// Without this, small N (e.g. 1 silent session out of 30) makes
	// every phrase pass the 20% coverage bar trivially. Default 2.
	MinSilentSessions int
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
	if c.MinSilentSessions == 0 {
		c.MinSilentSessions = 2
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
		if sc < cfg.MinSilentSessions {
			continue
		}
		if isNumericOnly(phrase) {
			// "0 0 0", "1 2 3" — commit hash fragments, line
			// numbers, scores. Never authorial voice.
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
	// Enumeration-line markers at start of line after trim: "(N) ",
	// "N. ", "N) ", "- ", "* ", or a leading bold header with a
	// keyword ("Варианты:", "Options:", "Possible causes:", etc.).
	reEnumMarker    = regexp.MustCompile(`^(\(\d+\)|\d+[\.\)]|[-*+])\s`)
	reEnumKeywords  = regexp.MustCompile(`(?i)^(?:\*\*)?(варианты|опции|причины|гипотезы|версии|options|causes|candidates|hypotheses|approaches|reasons)[:：]`)
)

// extractPhrases canonicalises text and returns the set of 2-gram and
// 3-gram phrases present in *enumeration regions*. An enumeration
// region is a run of ≥2 consecutive lines matching a bullet / number
// / keyword-header pattern, plus the header immediately preceding
// such a run when present.
//
// Why only enumeration regions: for rules like
// `wrong-root-cause-diagnosis` the authorial voice shows up when the
// assistant lists hypotheses — not in arbitrary prose or code
// discussion. Narrowing extraction to enumeration-style content is
// the difference between mining "варианты:" (good) and "syncengine
// apply swift" (code-discussion noise).
//
// Fallback: if no enumeration region is detected in the whole text,
// scan the whole thing. This preserves recall for narrower rules
// (e.g. language preferences) where phrases aren't list-bound.
func extractPhrases(text string, minRuneLen int) map[string]struct{} {
	text = reCodeFence.ReplaceAllString(text, " ")
	text = reQuotedBlock.ReplaceAllString(text, " ")

	enumText := extractEnumRegions(text)
	source := enumText
	if source == "" {
		source = text
	}
	lower := strings.ToLower(source)
	tokens := tokenise(lower)

	out := map[string]struct{}{}
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

// extractEnumRegions returns only the text lines that form part of
// an enumeration block (2+ consecutive marker lines). Optionally
// includes a preceding keyword header ("Варианты:", "Options:").
// Returns empty string if no block was detected.
func extractEnumRegions(text string) string {
	lines := strings.Split(text, "\n")
	markerAt := make([]bool, len(lines))
	headerAt := make([]bool, len(lines))
	for i, line := range lines {
		t := strings.TrimLeft(line, " \t")
		if reEnumMarker.MatchString(t) {
			markerAt[i] = true
		} else if reEnumKeywords.MatchString(t) {
			headerAt[i] = true
		}
	}

	var picked strings.Builder
	for i := 0; i < len(lines); i++ {
		if !markerAt[i] {
			continue
		}
		// Require at least one more marker line within the next
		// few rows (1 blank tolerated).
		foundPair := false
		for j := i + 1; j < len(lines) && j <= i+3; j++ {
			if markerAt[j] {
				foundPair = true
				break
			}
			if strings.TrimSpace(lines[j]) != "" && !markerAt[j] {
				break
			}
		}
		if !foundPair {
			continue
		}
		// Backfill: if the line immediately above is a header
		// keyword, include it.
		if i > 0 && headerAt[i-1] {
			picked.WriteString(lines[i-1])
			picked.WriteByte('\n')
			headerAt[i-1] = false // don't double-include
		}
		// Extend while markers or blanks-between-markers continue.
		j := i
		for j < len(lines) {
			if markerAt[j] {
				picked.WriteString(lines[j])
				picked.WriteByte('\n')
				j++
				continue
			}
			if strings.TrimSpace(lines[j]) == "" && j+1 < len(lines) && markerAt[j+1] {
				j++
				continue
			}
			break
		}
		i = j
	}
	return picked.String()
}

// isNumericOnly reports whether every token in the phrase is a
// pure digit/alnum-hash-looking run with no letters. Filters out
// commit fragments ("27f07f9"), line-count trios ("0 0 0"), and
// similar machine noise that accidentally survives the generic
// extraction.
func isNumericOnly(phrase string) bool {
	hasLetter := false
	for _, r := range phrase {
		if unicode.IsLetter(r) {
			hasLetter = true
			break
		}
	}
	return !hasLetter
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
