// Package fts mirrors bin/memory-fts-shadow.sh and bin/memory-fts-query.sh.
// The on-disk schema (CREATE VIRTUAL TABLE mem USING fts5(..., tokenize='trigram'))
// is fixed by docs/FORMAT.md §6 and must match the bash implementation byte-for-byte
// — any consumer of ~/.claude/memory/index.db relies on trigram+BM25 behaviour.
package fts

import (
	"strings"
	"unicode"
)

// tokenize turns a free-form user prompt into an FTS5 MATCH expression, joining
// tokens with " OR " so the trigram tokenizer fires on substrings. Non-alnum
// characters (including Cyrillic punctuation) split tokens; FTS5 reserved
// keywords are dropped to keep the query parser honest.
//
// Matches bin/memory-fts-query.sh:23-40 behaviour 1:1. Kept deliberately
// simple — anything fancier diverges from the bash version and breaks the
// drop-in replacement contract.
func tokenize(prompt string) string {
	// Replace anything that's not a letter, digit, or underscore with a
	// space. unicode.IsLetter covers Cyrillic / CJK / etc. under UTF-8.
	split := func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			return r
		}
		return ' '
	}
	cleaned := strings.Map(split, prompt)

	var toks []string
	for _, t := range strings.Fields(cleaned) {
		switch strings.ToUpper(t) {
		case "AND", "OR", "NOT", "NEAR":
			// FTS5 reserved words — dropping them matches the bash guard.
			continue
		}
		toks = append(toks, t)
	}
	if len(toks) == 0 {
		return ""
	}
	return strings.Join(toks, " OR ")
}
