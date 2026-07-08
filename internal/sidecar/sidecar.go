// Package sidecar is the SQLite metadata projection for hypomnema v2. It is
// the single place that touches SQLite. The WAL remains the source of truth;
// this store is rebuildable from it (see Reproject) and safe to delete.
package sidecar

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite handle.
type Store struct {
	db *sql.DB
}

// dbtx is the shared surface of *sql.DB and *sql.Tx the projection helpers
// write through, so Reproject can run every statement inside one transaction
// while the public Store methods keep operating on the raw handle.
type dbtx interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
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

// schemaVersion identifies the projection generation. v2 added project
// scoping (rows are stamped with their owning store and reconciled
// per-scope). Open wipes a sidecar written by a different generation — the
// sidecar is a derived projection, so the wipe only costs the next Reproject.
const schemaVersion = "2"

// Open opens (creating if needed) the sidecar DB at dbPath and applies the
// schema. Mirrors internal/fts: modernc.org/sqlite, busy_timeout, WAL journal.
// A schema_version mismatch recreates the file.
func Open(dbPath string) (*Store, error) { return open(dbPath, true) }

func open(dbPath string, allowRecreate bool) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("sidecar.Open: mkdir parent: %w", err)
	}
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sidecar.Open: sql.Open: %w", err)
	}
	if err := execBusyRetry(db, Schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sidecar.Open: schema: %w", err)
	}
	var v string
	err = db.QueryRow(`SELECT v FROM meta WHERE k='schema_version'`).Scan(&v)
	switch {
	case err == sql.ErrNoRows:
		// OR IGNORE: two processes creating a fresh sidecar concurrently both
		// see ErrNoRows and race to seed the version row; the loser must no-op,
		// not fail on the UNIQUE constraint (review C3).
		if ierr := execBusyRetry(db, `INSERT OR IGNORE INTO meta (k, v) VALUES ('schema_version', '`+schemaVersion+`')`); ierr != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sidecar.Open: seed meta: %w", ierr)
		}
	case err != nil:
		_ = db.Close()
		return nil, fmt.Errorf("sidecar.Open: read schema_version: %w", err)
	case v != schemaVersion:
		_ = db.Close()
		if !allowRecreate {
			return nil, fmt.Errorf("sidecar.Open: schema_version %q persists after recreate", v)
		}
		for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
			_ = os.Remove(p)
		}
		return open(dbPath, false)
	}
	return &Store{db: db}, nil
}

// execBusyRetry runs a statement, retrying on SQLITE_BUSY. The busy_timeout
// PRAGMA does not cover the first-open DDL race (two processes creating the
// schema on a fresh/recreated sidecar both fail fast with SQLITE_BUSY —
// review C3). Bounded to ~1s total, well under the 5s busy_timeout, so a
// genuinely stuck DB still surfaces its error.
func execBusyRetry(db *sql.DB, stmt string) error {
	var err error
	for i := 0; i < 20; i++ {
		if _, err = db.Exec(stmt); err == nil {
			return nil
		}
		if !strings.Contains(strings.ToLower(err.Error()), "database is locked") &&
			!strings.Contains(err.Error(), "SQLITE_BUSY") {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return err
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

// Upsert inserts or replaces the memory row keyed by slug.
func (s *Store) Upsert(r Record) error { return upsertIn(s.db, r) }

func upsertIn(e dbtx, r Record) error {
	if r.Status == "" {
		r.Status = "active"
	}
	_, err := e.Exec(`
INSERT INTO memory (slug, content_sha, type, name, description, project,
	domains, created, last_injected, ref_count, status, effectiveness)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(slug) DO UPDATE SET
	content_sha=excluded.content_sha, type=excluded.type, name=excluded.name,
	description=excluded.description, project=excluded.project,
	domains=excluded.domains, created=excluded.created,
	last_injected=excluded.last_injected, ref_count=excluded.ref_count,
	status=excluded.status, effectiveness=excluded.effectiveness`,
		r.Slug, r.ContentSHA, r.Type, r.Name, r.Description, r.Project,
		r.Domains, r.Created, r.LastInjected, r.RefCount, r.Status, r.Effectiveness)
	if err != nil {
		return fmt.Errorf("sidecar.Upsert: %w", err)
	}
	return nil
}

// All returns every memory row, ordered by slug for stable output.
func (s *Store) All() ([]Record, error) { return allIn(s.db) }

func allIn(e dbtx) ([]Record, error) {
	rows, err := e.Query(`SELECT slug, content_sha, type, name, description,
		project, domains, created, last_injected, ref_count, status, effectiveness
		FROM memory ORDER BY slug`)
	if err != nil {
		return nil, fmt.Errorf("sidecar.All: %w", err)
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.Slug, &r.ContentSHA, &r.Type, &r.Name,
			&r.Description, &r.Project, &r.Domains, &r.Created, &r.LastInjected,
			&r.RefCount, &r.Status, &r.Effectiveness); err != nil {
			return nil, fmt.Errorf("sidecar.All: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
