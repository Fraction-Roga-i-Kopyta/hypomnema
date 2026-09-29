package closer

import (
	"path/filepath"
	"regexp"
	"strings"
)

// citeTagRe matches an opening citation tag the way Claude Code does: tag
// name cc-memory / cc_memory / ccmemory in any case, attributes bounded
// (Claude Code allows 1 024 bytes; Go's RE2 caps a repeat at 1 000, which is
// immaterial for a filenames list). Closing tags are ignored — a citation
// counts from its opening tag (an unclosed tag at the end of a message still
// names its files).
var citeTagRe = regexp.MustCompile(`(?i)<(?:cc-memory|cc_memory|ccmemory)(?:\s[^>]{0,1000})?>`)

// citeFilenamesRe extracts the filenames attribute (case-insensitive).
var citeFilenamesRe = regexp.MustCompile(`(?i)\bfilenames="([^"]*)"`)

// maxCitations bounds how many distinct files one transcript can cite.
const maxCitations = 256

// Citations returns the memory files cited in text through
// <cc-memory filenames="a.md,b.md">…</cc-memory> tags — the citation format
// Claude Code defines for memory use. Names are reduced to their base name
// with a ".md" suffix, deduped, in first-appearance order, capped at 256.
func Citations(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, tag := range citeTagRe.FindAllString(text, -1) {
		m := citeFilenamesRe.FindStringSubmatch(tag)
		if m == nil {
			continue
		}
		for _, name := range strings.Split(m[1], ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			name = filepath.Base(name)
			if !strings.HasSuffix(name, ".md") {
				name += ".md"
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
			if len(out) >= maxCitations {
				return out
			}
		}
	}
	return out
}
