// Package inject is the v2 injection orchestrator: it turns a hook's
// (cwd, prompt) into a ranked Markdown context block over native memory.
package inject

import (
	"path/filepath"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/tokenize"
)

// Keywords derives the ranking query terms from the prompt (the reactive
// signal) and the cwd basename (a weak project hint). Deduped, lowercased,
// Unicode-tokenized. Git-signal enrichment is a future additive refinement.
func Keywords(cwd, prompt string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(toks []string) {
		for _, t := range toks {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	add(tokenize.Tokenize(prompt, nil))
	add(tokenize.Tokenize(filepath.Base(cwd), nil))
	return out
}
