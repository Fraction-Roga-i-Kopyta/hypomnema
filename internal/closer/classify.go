// Package closer implements the Stop-hook close path: it classifies injected
// memories as cited (cite-useful) or not (cite-silent) based on explicit
// <cc-memory> citations in the assistant text, and emits the closing WAL
// events, refreshes the sidecar, and decays stale entries. Classify's
// substring match now serves only the holdout (ablation) observation.
package closer

import "strings"

// Classify splits slugs into useful (applied in the assistant text) and
// silent, by a substring match: a slug counts as useful when any of its
// frontmatter `evidence:` phrases appears in the text, OR its base name (slug
// minus ".md") or frontmatter name does — all case-insensitive substring
// matches. Close itself no longer calls Classify for the live cite-useful /
// cite-silent signal (that comes from explicit <cc-memory> citations via
// Citations); Classify remains in use only for the holdout (ablation)
// observation, where no citation channel is available to compare against.
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
