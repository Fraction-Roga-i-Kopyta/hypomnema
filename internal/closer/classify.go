// Package closer implements the Stop-hook close path: it classifies injected
// memories as cited (trigger-useful) or not (trigger-silent) and emits the
// closing WAL events, refreshes the sidecar, and decays stale entries.
package closer

import "strings"

// Classify splits injected slugs into useful (applied in the assistant text)
// and silent. A slug counts as useful when any of its frontmatter `evidence:`
// phrases appears in the text, OR its base name (slug minus ".md") or
// frontmatter name does — all case-insensitive substring matches. Evidence
// widens the signal (it catches a rule being applied without being named);
// it does not replace citation.
func Classify(injected []string, names map[string]string, evidence map[string][]string, text string) (useful, silent []string) {
	lower := strings.ToLower(text)
	for _, slug := range injected {
		cited := false
		for _, phrase := range evidence[slug] {
			if p := strings.ToLower(strings.TrimSpace(phrase)); p != "" && strings.Contains(lower, p) {
				cited = true
				break
			}
		}
		if !cited {
			base := strings.ToLower(strings.TrimSuffix(slug, ".md"))
			cited = base != "" && strings.Contains(lower, base)
		}
		if !cited {
			if n := strings.ToLower(strings.TrimSpace(names[slug])); n != "" && strings.Contains(lower, n) {
				cited = true
			}
		}
		if cited {
			useful = append(useful, slug)
		} else {
			silent = append(silent, slug)
		}
	}
	return useful, silent
}
