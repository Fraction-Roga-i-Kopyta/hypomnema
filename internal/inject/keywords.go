// Package inject is the v2 injection orchestrator: it turns a hook's
// (cwd, prompt) into a ranked Markdown context block over native memory.
package inject

import (
	"os/exec"
	"path/filepath"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/tokenize"
)

// Keywords derives the ranking query terms from the prompt (the reactive
// signal), the cwd basename (a weak project hint), and — at SessionStart when
// the prompt is empty — the git context of cwd (branch, changed files, recent
// commit subjects). Deduped, lowercased, Unicode-tokenized.
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
	add(gitSignal(cwd))
	return out
}

// gitSignal returns tokenized "what am I working on" context from cwd's git
// repo: current branch, working-tree changes (modified + untracked), and the
// last few commit subjects. Best-effort — git absent or cwd not a repo yields
// nil, never an error. This is the SessionStart relevance signal (the prompt is
// empty then); UserPromptSubmit re-ranks on prompt tokens.
func gitSignal(cwd string) []string {
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", cwd}, args...)...).Output()
		if err != nil {
			return ""
		}
		return string(out)
	}
	var toks []string
	toks = append(toks, tokenize.Tokenize(git("branch", "--show-current"), nil)...)
	toks = append(toks, tokenize.Tokenize(git("status", "--porcelain"), nil)...)
	toks = append(toks, tokenize.Tokenize(git("log", "-3", "--format=%s"), nil)...)
	return toks
}
