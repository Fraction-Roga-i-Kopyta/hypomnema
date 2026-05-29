// Package closer implements the Stop-hook close path: it classifies injected
// memories as cited (trigger-useful) or not (trigger-silent) and emits the
// closing WAL events, refreshes the sidecar, and decays stale entries.
package closer

import "strings"

// Classify splits injected slugs into useful (cited in the assistant text) and
// silent (not cited). A slug counts as cited if its base name (slug minus
// ".md") or its frontmatter name appears in the text, case-insensitively.
func Classify(injected []string, names map[string]string, text string) (useful, silent []string) {
	lower := strings.ToLower(text)
	for _, slug := range injected {
		base := strings.ToLower(strings.TrimSuffix(slug, ".md"))
		cited := base != "" && strings.Contains(lower, base)
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
