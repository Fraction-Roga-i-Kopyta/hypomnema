package sidecar

import (
	"fmt"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/tokenize"
)

// PopulateKeywords rebuilds keyword rows for the supplied files: name +
// description + body are tokenized; weight is term frequency. Rows are cleared
// scoped by (slug, project) so a Reproject for one project never wipes another
// project's rows for a same-basename slug (continuity.md/notes.md exist in
// every project) — that cross-project clobber zeroed the other project's
// overlap (review E5).
func (s *Store) PopulateKeywords(files []native.MemFile) error {
	return populateKeywordsIn(s.db, files)
}

func populateKeywordsIn(e dbtx, files []native.MemFile) error {
	for _, f := range files {
		if _, err := e.Exec(`DELETE FROM keyword WHERE slug=? AND project=?`, f.Slug, f.Project); err != nil {
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
			if _, err := e.Exec(`INSERT INTO keyword (slug, project, term, weight) VALUES (?,?,?,?)`,
				f.Slug, f.Project, term, float64(n)); err != nil {
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
	// Chunk the IN(...) list: SQLite caps bound variables at 32766, and a big
	// prompt + git status can tokenize past that, which errored and (in the
	// injection caller) zeroed all overlap (review E6). Terms are unique and
	// partitioned across chunks, so per-slug distinct-term counts sum exactly.
	const maxVars = 30000
	for start := 0; start < len(uniq); start += maxVars {
		end := start + maxVars
		if end > len(uniq) {
			end = len(uniq)
		}
		batch := uniq[start:end]
		placeholders := strings.TrimRight(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, len(batch))
		for i, t := range batch {
			args[i] = t
		}
		if err := s.overlapBatch(out, placeholders, args); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) overlapBatch(out map[string]int, placeholders string, args []any) error {
	rows, err := s.db.Query(
		`SELECT slug, COUNT(DISTINCT term) FROM keyword WHERE term IN (`+placeholders+`) GROUP BY slug`,
		args...)
	if err != nil {
		return fmt.Errorf("sidecar.OverlapScores: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var slug string
		var n int
		if err := rows.Scan(&slug, &n); err != nil {
			return fmt.Errorf("sidecar.OverlapScores: scan: %w", err)
		}
		out[slug] += n // accumulate: chunks partition the (disjoint) term set
	}
	return rows.Err()
}
