// Package sidecar is the SQLite metadata projection for hypomnema v2. It is
// the single place that touches SQLite. The WAL remains the source of truth;
// this store is rebuildable from it (see Reproject) and safe to delete.
package sidecar

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite handle.
type Store struct {
	db   *sql.DB
	path string
}

// Record is one row of the memory table — a native file plus hypomnema's
// derived metadata.
type Record struct {
	Slug          string
	ContentSHA    string
	Type          string // fine-grained hypomnema type (mistake/strategy/...)
	Name          string
	Description   string
	Project       string
	Domains       string
	Created       string
	LastInjected  string
	RefCount      int
	Status        string
	Effectiveness float64
}

// Open opens (creating if needed) the sidecar DB at dbPath and applies the
// schema. Mirrors internal/fts: modernc.org/sqlite, busy_timeout, WAL journal.
func Open(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("sidecar.Open: mkdir parent: %w", err)
	}
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sidecar.Open: sql.Open: %w", err)
	}
	if _, err := db.Exec(Schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sidecar.Open: schema: %w", err)
	}
	return &Store{db: db, path: dbPath}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// Get returns the record for slug. The bool is false (with nil error) when no
// such row exists.
func (s *Store) Get(slug string) (Record, bool, error) {
	row := s.db.QueryRow(`SELECT slug, content_sha, type, name, description,
		project, domains, created, last_injected, ref_count, status, effectiveness
		FROM memory WHERE slug = ?`, slug)
	var r Record
	err := row.Scan(&r.Slug, &r.ContentSHA, &r.Type, &r.Name, &r.Description,
		&r.Project, &r.Domains, &r.Created, &r.LastInjected, &r.RefCount,
		&r.Status, &r.Effectiveness)
	if err == sql.ErrNoRows {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("sidecar.Get: %w", err)
	}
	return r, true, nil
}
