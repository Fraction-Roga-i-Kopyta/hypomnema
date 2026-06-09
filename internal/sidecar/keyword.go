package sidecar

import (
	"fmt"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/tokenize"
)

// PopulateKeywords rebuilds keyword rows for the supplied files: name +
// description + body are tokenized; weight is term frequency. Only the
// supplied files' rows are cleared — the table is shared across projects,
// and a scoped Reproject must not wipe other projects' relevance signal.
func (s *Store) PopulateKeywords(files []native.MemFile) error {
	for _, f := range files {
		if _, err := s.db.Exec(`DELETE FROM keyword WHERE slug=?`, f.Slug); err != nil {
			return fmt.Errorf("sidecar.PopulateKeywords: clear %s: %w", f.Slug, err)
		}
	}
	for _, f := range files {
		// Frontmatter keywords/domains are explicit relevance signal — they
		// often name concepts the body never spells out.
		text := f.Name + " " + f.Description + " " +
			strings.Join(f.Keywords, " ") + " " + strings.Join(f.Domains, " ") + " " + f.Body
		freq := map[string]int{}
		for _, tok := range tokenize.Tokenize(text, nil) {
			freq[tok]++
		}
		for term, n := range freq {
			if _, err := s.db.Exec(`INSERT INTO keyword (slug, term, weight) VALUES (?,?,?)`,
				f.Slug, term, float64(n)); err != nil {
				return fmt.Errorf("sidecar.PopulateKeywords: insert %s/%s: %w", f.Slug, term, err)
			}
		}
	}
	return nil
}

// OverlapScores returns, per slug, the count of DISTINCT query terms that
// appear in that slug's keyword set. Empty query → empty map.
func (s *Store) OverlapScores(queryTerms []string) (map[string]int, error) {
	out := map[string]int{}
	seen := map[string]bool{}
	var uniq []string
	for _, t := range queryTerms {
		if t != "" && !seen[t] {
			seen[t] = true
			uniq = append(uniq, t)
		}
	}
	if len(uniq) == 0 {
		return out, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(uniq)), ",")
	args := make([]any, len(uniq))
	for i, t := range uniq {
		args[i] = t
	}
	rows, err := s.db.Query(
		`SELECT slug, COUNT(DISTINCT term) FROM keyword WHERE term IN (`+placeholders+`) GROUP BY slug`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("sidecar.OverlapScores: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var slug string
		var n int
		if err := rows.Scan(&slug, &n); err != nil {
			return nil, fmt.Errorf("sidecar.OverlapScores: scan: %w", err)
		}
		out[slug] = n
	}
	return out, rows.Err()
}
