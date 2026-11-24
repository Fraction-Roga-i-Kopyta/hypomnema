package inject

import (
	"path/filepath"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/rank"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/tokenize"
)

// wordSet is a lookup set of relevance tokens.
type wordSet map[string]bool

func newWordSet(toks []string) wordSet {
	s := make(wordSet, len(toks))
	for _, t := range toks {
		s[t] = true
	}
	return s
}

// foundIn counts how many distinct members of s occur anywhere in f: its
// head (name, description, keywords, domains) or its body. It reads the
// fact's own file, never the sidecar's keyword rows — those are grouped by
// file name across projects, so another project's continuity.md would lend
// this one its words.
func (s wordSet) foundIn(f native.MemFile) int {
	if len(s) == 0 {
		return 0
	}
	found := wordSet{}
	for _, toks := range [][]string{headTokens(f), tokenize.Relevance(f.Body)} {
		for _, t := range toks {
			if s[t] {
				found[t] = true
			}
		}
	}
	return len(found)
}

// promptBodyMin is how many distinct prompt terms a fact must share with the
// prompt when none of them is in its head. One is not enough: in a
// conversational prompt a lone body hit is nearly always a filler word that
// happens to occur somewhere in a long body.
const promptBodyMin = 2

// promptGate is the UserPromptSubmit filter. Ranking alone is zero-safe, so
// once the facts the prompt matched were placed, recency filled the rest of
// the top-K with facts unrelated to the turn. Only the prompt's own terms
// count here — the cwd and git terms still rank, but the user did not ask
// for them — and a prompt with no relevance terms admits nothing.
type promptGate struct{ prompt wordSet }

func newPromptGate(prompt string) promptGate {
	return promptGate{prompt: newWordSet(tokenize.Relevance(prompt))}
}

// admits reports whether f may inject: the prompt names what f is about (a
// prompt term in its head), or f shares at least promptBodyMin prompt terms
// anywhere.
func (g promptGate) admits(f native.MemFile) bool {
	if len(g.prompt) == 0 {
		return false
	}
	for _, t := range headTokens(f) {
		if g.prompt[t] {
			return true
		}
	}
	return g.prompt.foundIn(f) >= promptBodyMin
}

// admission returns the pre-rank filter of in.Event: the head gate on the
// Agent call's description for SubagentStart, the prompt gate for
// UserPromptSubmit, nil (no filter) otherwise — SessionStart injects the
// fixed core and is not gated.
func admission(in Input, files []native.MemFile) func(native.MemFile) bool {
	switch in.Event {
	case "SubagentStart":
		home := filepath.Dir(in.ClaudeHome)
		return newGate(in.Brief, gateExclusions(in.ProjectRoot, home, in.AgentType, files)).pass
	case "UserPromptSubmit":
		return newPromptGate(in.Prompt).admits
	}
	return nil
}

// keepFiles filters cands in place to those whose native file passes ok; a
// nil ok keeps every candidate. Under a filter, a candidate without a native
// file (a sidecar row that outlived its file) could not render anyway and is
// dropped.
func keepFiles(cands []rank.Candidate, byKey map[string]native.MemFile, ok func(native.MemFile) bool) []rank.Candidate {
	if ok == nil {
		return cands
	}
	kept := cands[:0]
	for _, c := range cands {
		if f, found := byKey[native.QKey(c.Project, c.Slug)]; found && ok(f) {
			kept = append(kept, c)
		}
	}
	return kept
}
