package inject

import (
	"math"
	"strings"
	"unicode"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/tokenize"
)

// headGateMin is how many distinct query terms a fact's head must contain
// to pass the head gate. Two, not one: compaction summaries and subagent
// briefs are long, so a single shared word is coincidence.
const headGateMin = 2

// commonHeadFloor and commonHeadShare set when a head term is too common in
// the corpus to count toward the gate: df ≥ max(commonHeadFloor,
// ceil(commonHeadShare × N)). The floor keeps a small store from excluding
// every term.
const (
	commonHeadFloor = 5
	commonHeadShare = 0.10
)

// gate is the head gate shared by compact SessionStart and SubagentStart: a
// fact passes when the query names what it is about — at least headGateMin
// distinct query terms in its name, description, keywords or domains. Body
// words never count: a long query shares two words with almost any long
// body.
type gate struct{ want map[string]bool }

// newGate builds the gate's term set from free text: relevance tokens minus
// the exclusions and minus digit-only tokens (dates, counts, PR numbers).
func newGate(text string, exclude map[string]bool) gate {
	want := map[string]bool{}
	for _, tok := range tokenize.Relevance(text) {
		if exclude[tok] || digitsOnly(tok) {
			continue
		}
		want[tok] = true
	}
	return gate{want: want}
}

// pass reports whether f's head carries at least headGateMin distinct
// wanted terms.
func (g gate) pass(f native.MemFile) bool {
	if len(g.want) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, tok := range headTokens(f) {
		if g.want[tok] && !seen[tok] {
			seen[tok] = true
			if len(seen) >= headGateMin {
				return true
			}
		}
	}
	return false
}

// headTokens tokenizes a fact's head: name, description, keywords, domains.
func headTokens(f native.MemFile) []string {
	return tokenize.Relevance(f.Name + " " + f.Description + " " +
		strings.Join(f.Keywords, " ") + " " + strings.Join(f.Domains, " "))
}

// gateExclusions returns the terms that do not count toward the gate (they
// still feed ranking): the path components of the project root and of
// $HOME — queries quote absolute paths, so the login and repo name are in
// almost every one — but not components below the root (a worktree or a
// subdirectory names the task itself); the agent type's words; and head
// terms common to the corpus. An empty projectRoot or home excludes
// nothing for that path.
func gateExclusions(projectRoot, home, agentType string, files []native.MemFile) map[string]bool {
	ex := map[string]bool{}
	for _, s := range []string{projectRoot, home, agentType} {
		for _, tok := range tokenize.Relevance(s) {
			ex[tok] = true
		}
	}
	minDF := int(math.Ceil(commonHeadShare * float64(len(files))))
	if minDF < commonHeadFloor {
		minDF = commonHeadFloor
	}
	df := map[string]int{}
	for _, f := range files {
		seen := map[string]bool{}
		for _, tok := range headTokens(f) {
			if !seen[tok] {
				seen[tok] = true
				df[tok]++
			}
		}
	}
	for tok, n := range df {
		if n >= minDF {
			ex[tok] = true
		}
	}
	return ex
}

func digitsOnly(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}
