package inject

import (
	"sort"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/tokenize"
)

// TopTerms reduces text to its n most frequent relevance terms (ties in
// first-occurrence order), space-joined; n < 0 keeps every term. It bounds
// the ranking query built from a subagent's task prompt, so a long text
// cannot reward the longest fact bodies.
func TopTerms(text string, n int) string {
	toks := tokenize.Relevance(text)
	count := map[string]int{}
	var order []string
	for _, t := range toks {
		if count[t] == 0 {
			order = append(order, t)
		}
		count[t]++
	}
	sort.SliceStable(order, func(i, j int) bool { return count[order[i]] > count[order[j]] })
	if n >= 0 && len(order) > n {
		order = order[:n]
	}
	return strings.Join(order, " ")
}
