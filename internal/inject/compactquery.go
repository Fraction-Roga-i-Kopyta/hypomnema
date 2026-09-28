package inject

import (
	"regexp"
	"sort"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/tokenize"
)

// compactFocus lists (lower-cased substrings of) the Claude Code compaction
// summary headers that describe what the session is doing now. The other
// standard sections — key concepts, files and code, errors, all user
// messages — are inventories whose vocabulary would make raw overlap reward
// the longest bodies.
var compactFocus = []string{"primary request", "pending tasks", "current work", "next step"}

// compactQueryTerms caps the query built from a summary.
const compactQueryTerms = 40

// sectionHeaderRe matches a numbered or markdown section header line in the
// forms Claude Code actually emits (checked against 25 live summaries, 14 of
// them markdown-styled): "1. Primary Request and Intent:",
// "1. **Primary Request and Intent:**", "## 1. Primary Request and Intent",
// "## Current Work", "7. Pending Tasks (all awaiting review):".
var sectionHeaderRe = regexp.MustCompile(`^\s*(?:#{1,6}\s+(?:\d+\.\s+)?|\d+\.\s+)\**([A-Za-z][A-Za-z /&'-]{2,58}?)\s*(?:\([^)]*\))?:?\**:?\s*$`)

// CompactQuery turns a compaction summary into a bounded ranking query: the
// text of the focus sections when the summary uses the standard headers
// (else the whole summary), reduced to its compactQueryTerms most frequent
// relevance terms, ties in first-occurrence order, space-joined.
func CompactQuery(summary string) string {
	toks := tokenize.Relevance(focusText(summary))
	count := map[string]int{}
	var order []string
	for _, t := range toks {
		if count[t] == 0 {
			order = append(order, t)
		}
		count[t]++
	}
	sort.SliceStable(order, func(i, j int) bool { return count[order[i]] > count[order[j]] })
	if len(order) > compactQueryTerms {
		order = order[:compactQueryTerms]
	}
	return strings.Join(order, " ")
}

func focusText(summary string) string {
	var b strings.Builder
	in, found := false, false
	for _, ln := range strings.Split(summary, "\n") {
		if m := sectionHeaderRe.FindStringSubmatch(ln); m != nil {
			title := strings.ToLower(m[1])
			in = false
			for _, f := range compactFocus {
				if strings.Contains(title, f) {
					in, found = true, true
					break
				}
			}
			continue
		}
		if in {
			b.WriteString(ln)
			b.WriteByte('\n')
		}
	}
	if !found {
		return summary
	}
	return b.String()
}
