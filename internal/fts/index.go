package fts

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Schema is the FTS5 virtual-table DDL the bash sync script creates.
// Contract surface per docs/FORMAT.md §6: columns `path UNINDEXED, content,
// mtime UNINDEXED`, trigram tokenizer. Any drift here and bash/Go implementations
// would produce different query results on the same DB.
const Schema = `CREATE VIRTUAL TABLE IF NOT EXISTS mem USING fts5(
  path UNINDEXED,
  content,
  mtime UNINDEXED,
  tokenize='trigram'
);`

// Index wraps an open SQLite connection to ~/.claude/memory/index.db.
type Index struct {
	db   *sql.DB
	path string
}

// Open creates the parent directory if necessary, opens the SQLite file,
// and applies the schema. Uses `modernc.org/sqlite` (pure Go) so the binary
// is CGO_ENABLED=0 cross-compilable from macOS to Linux.
func Open(dbPath string) (*Index, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("fts.Open: mkdir parent: %w", err)
	}
	// _pragma=busy_timeout=5000 matches the bash sync script's retry budget.
	dsn := dbPath + "?_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("fts.Open: sql.Open: %w", err)
	}
	if _, err := db.Exec(Schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("fts.Open: schema: %w", err)
	}
	return &Index{db: db, path: dbPath}, nil
}

// Close releases the underlying handle.
func (ix *Index) Close() error {
	return ix.db.Close()
}

// Hit is a single FTS5 match: file path and BM25 score (lower is better).
type Hit struct {
	Path  string
	Score float64
}

// Query returns up to limit hits for the given prompt, BM25-sorted (best first).
// Returns an empty slice — not an error — when the tokenized prompt is empty
// (mirrors bash behaviour: nothing to search for is not a failure).
func (ix *Index) Query(ctx context.Context, prompt string, limit int) ([]Hit, error) {
	expr := tokenize(prompt)
	if expr == "" {
		return nil, nil
	}

	// Parameter binding doesn't interpolate into MATCH expressions the way
	// the bash SQL-string version does; FTS5 accepts the full expression as
	// a single bound string. `ORDER BY bm25(mem)` + LIMIT is the same as
	// memory-fts-query.sh:47-51.
	rows, err := ix.db.QueryContext(ctx, `
		SELECT path, ROUND(bm25(mem), 3)
		FROM mem
		WHERE mem MATCH ?
		ORDER BY bm25(mem)
		LIMIT ?`, expr, limit)
	if err != nil {
		// Malformed MATCH expressions raise query-time errors; in bash they
		// ended up as a silent no-match via `2>/dev/null || true`. Same
		// policy here — don't break the shadow pass for a bad prompt.
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, nil
	}
	defer rows.Close()

	var hits []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.Path, &h.Score); err != nil {
			return nil, fmt.Errorf("fts.Query: scan: %w", err)
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}
