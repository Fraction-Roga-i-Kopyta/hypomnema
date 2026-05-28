# v2.0 Phase 1 — Foundations (native adapter + sidecar) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the read-side foundation of hypomnema v2 — a `native` adapter that reads Claude Code's native memory files, and a SQLite `sidecar` projection that holds hypomnema's metadata (ref_count, effectiveness, status) rebuildable from the WAL + native frontmatter.

**Architecture:** Two new Go packages (`internal/native`, `internal/sidecar`) plus a diagnostic `memoryctl sidecar` verb. Purely additive — no existing hook behaviour changes. WAL stays the append-only source of truth; the sidecar is a disposable, rebuildable index. Phase 2 (ranker) consumes these.

**Tech Stack:** Go 1.25, `modernc.org/sqlite` (pure-Go, CGO_ENABLED=0), stdlib `testing` (table-driven, `t.TempDir()`). Module path `github.com/Fraction-Roga-i-Kopyta/hypomnema`.

**Scope boundary (this phase):** Reproject populates the `memory` and `outcome` tables. The `keyword` table is created but left empty — population belongs to Phase 2 (ranker), which owns tokenization. `inject-agg` events count as 1 toward ref_count (exact count-reconstruction is a Phase 6 / migration concern).

---

### Task 1: `internal/native` — directory resolution

**Files:**
- Create: `internal/native/dirs.go`
- Test: `internal/native/dirs_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/native/dirs_test.go
package native

import (
	"path/filepath"
	"testing"
)

func TestSlugFromCWD(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Verified real example: this repo's own native memory dir.
		{"repo path", "/Users/akamash/Development/hypomnema", "-Users-akamash-Development-hypomnema"},
		{"tmp path", "/tmp/cap-test", "-tmp-cap-test"},
		{"root", "/", "-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SlugFromCWD(tc.in); got != tc.want {
				t.Errorf("SlugFromCWD(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestProjectMemoryDir(t *testing.T) {
	got := ProjectMemoryDir("/home/u", "/tmp/proj")
	want := filepath.Join("/home/u", ".claude", "projects", "-tmp-proj", "memory")
	if got != want {
		t.Errorf("ProjectMemoryDir = %q, want %q", got, want)
	}
}

func TestGlobalMemoryDir_Default(t *testing.T) {
	t.Setenv("HYPOMNEMA_GLOBAL_DIR", "")
	got := GlobalMemoryDir("/home/u")
	want := filepath.Join("/home/u", ".claude", "memory-global")
	if got != want {
		t.Errorf("GlobalMemoryDir = %q, want %q", got, want)
	}
}

func TestGlobalMemoryDir_Override(t *testing.T) {
	t.Setenv("HYPOMNEMA_GLOBAL_DIR", "/custom/global")
	if got := GlobalMemoryDir("/home/u"); got != "/custom/global" {
		t.Errorf("GlobalMemoryDir override = %q, want /custom/global", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/native/ -run TestSlugFromCWD -v`
Expected: FAIL — `undefined: SlugFromCWD` (package doesn't compile yet).

- [ ] **Step 3: Write minimal implementation**

```go
// internal/native/dirs.go
// Package native is the read-only adapter over Claude Code's native memory
// store. It is the single place that knows the native on-disk layout and
// frontmatter format; everything else in v2 depends on this package rather
// than touching native files directly.
package native

import (
	"os"
	"path/filepath"
	"strings"
)

// SlugFromCWD encodes an absolute working-directory path into the project
// slug Claude Code uses for its per-project memory dir: every path separator
// becomes '-'. /Users/akamash/Development/hypomnema ->
// -Users-akamash-Development-hypomnema (verified against a real install).
//
// NOTE: this matches observed behaviour for ordinary paths. If a future
// Claude Code version encodes other characters (dots, spaces), extend here —
// this is the single chokepoint.
func SlugFromCWD(cwd string) string {
	return strings.ReplaceAll(cwd, string(os.PathSeparator), "-")
}

// ProjectMemoryDir returns the native per-project memory directory for cwd:
// <home>/.claude/projects/<slug>/memory.
func ProjectMemoryDir(home, cwd string) string {
	return filepath.Join(home, ".claude", "projects", SlugFromCWD(cwd), "memory")
}

// GlobalMemoryDir returns the hypomnema-owned global store (native format),
// which native memory itself lacks (native is per-project only). Honours the
// HYPOMNEMA_GLOBAL_DIR override; defaults to <home>/.claude/memory-global.
func GlobalMemoryDir(home string) string {
	if d := os.Getenv("HYPOMNEMA_GLOBAL_DIR"); d != "" {
		return d
	}
	return filepath.Join(home, ".claude", "memory-global")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/native/ -v`
Expected: PASS (all four tests).

- [ ] **Step 5: Commit**

```bash
git add internal/native/dirs.go internal/native/dirs_test.go
git commit -m "feat(native): project/global memory dir resolution"
```

---

### Task 2: `internal/native` — frontmatter parse + List

**Files:**
- Create: `internal/native/native.go`
- Test: `internal/native/native_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/native/native_test.go
package native

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestList_ParsesFrontmatterAndSkipsIndex(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "feedback_x.md", "---\nname: Rule X\ndescription: do X\ntype: feedback\noriginSessionId: abc\n---\nBody text here.\n")
	writeFile(t, dir, "MEMORY.md", "# Memory Index\n- index, must be skipped\n")
	writeFile(t, dir, "notes.txt", "not markdown, skipped\n")

	files, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file (index + non-md skipped), got %d", len(files))
	}
	f := files[0]
	if f.Slug != "feedback_x.md" {
		t.Errorf("Slug = %q, want feedback_x.md", f.Slug)
	}
	if f.Name != "Rule X" || f.Description != "do X" || f.Type != "feedback" {
		t.Errorf("frontmatter = %+v", f)
	}
	if f.Body != "Body text here." {
		t.Errorf("Body = %q", f.Body)
	}
	if len(f.ContentSHA) != 64 {
		t.Errorf("ContentSHA len = %d, want 64 (hex sha256)", len(f.ContentSHA))
	}
}

func TestList_MissingDirIsEmpty(t *testing.T) {
	files, err := List(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("missing dir should be nil error, got %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestList_ToleratesBOMAndCRLFAndQuotes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.md", "﻿---\r\nname: \"Quoted\"\r\ntype: reference\r\n---\r\nbody\r\n")
	files, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 1 || files[0].Name != "Quoted" || files[0].Type != "reference" {
		t.Fatalf("BOM/CRLF/quote tolerance failed: %+v", files)
	}
}

func TestList_UnterminatedFrontmatterIsSkippedNotFatal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "good.md", "---\nname: G\ntype: reference\n---\nok\n")
	writeFile(t, dir, "bad.md", "---\nname: never closed\nbody with no end fence\n")
	files, err := List(dir)
	if err != nil {
		t.Fatalf("List should not error on a bad file: %v", err)
	}
	// The unterminated file parses with empty frontmatter (whole content as
	// body) — it is not fatal and is still listed; assert the good one exists.
	var haveGood bool
	for _, f := range files {
		if f.Slug == "good.md" && f.Name == "G" {
			haveGood = true
		}
	}
	if !haveGood {
		t.Fatalf("good.md missing from %+v", files)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/native/ -run TestList -v`
Expected: FAIL — `undefined: List`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/native/native.go
package native

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MemFile is one native memory file: minimal frontmatter + body + identity.
type MemFile struct {
	Slug        string // path relative to its memory dir, e.g. "feedback_x.md"
	Path        string // absolute path
	Name        string // frontmatter: name
	Description string // frontmatter: description
	Type        string // frontmatter: type (user|feedback|project|reference)
	ContentSHA  string // sha256 of full file bytes — rename/edit detection
	Body        string // markdown body, frontmatter stripped
}

// List enumerates *.md files directly under dir (non-recursive) and parses
// each. A missing dir yields (nil, nil) — a fresh install simply has no
// memory. MEMORY.md (the harness index) and non-.md files are skipped.
// Individual unreadable files are skipped, never fatal.
func List(dir string) ([]MemFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("native.List: read dir: %w", err)
	}
	var out []MemFile
	for _, e := range entries {
		if e.IsDir() || e.Name() == "MEMORY.md" || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		mf, err := parseFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // skip unreadable, never fatal
		}
		out = append(out, mf)
	}
	return out, nil
}

func parseFile(path string) (MemFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return MemFile{}, fmt.Errorf("native.parseFile: %w", err)
	}
	fm, body := splitFrontmatter(string(raw))
	sum := sha256.Sum256(raw)
	return MemFile{
		Slug:        filepath.Base(path),
		Path:        path,
		Name:        fm["name"],
		Description: fm["description"],
		Type:        fm["type"],
		ContentSHA:  hex.EncodeToString(sum[:]),
		Body:        body,
	}, nil
}

// splitFrontmatter parses a leading `---`-fenced YAML-ish block into a flat
// key->value map and returns the trimmed body. Tolerates a UTF-8 BOM, CRLF
// line endings, and quoted scalars — matching the existing bash parser's
// tolerances. An unterminated fence yields an empty map + the whole content
// as body.
func splitFrontmatter(s string) (map[string]string, string) {
	fm := map[string]string{}
	s = strings.TrimPrefix(s, "﻿")
	if !strings.HasPrefix(s, "---") {
		return fm, strings.TrimSpace(s)
	}
	lines := strings.Split(s, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return fm, strings.TrimSpace(s)
	}
	for _, ln := range lines[1:end] {
		ln = strings.TrimRight(ln, "\r")
		idx := strings.IndexByte(ln, ':')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(ln[:idx])
		val := strings.Trim(strings.TrimSpace(ln[idx+1:]), `"'`)
		fm[key] = val
	}
	return fm, strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/native/ -v`
Expected: PASS (all tests, Task 1 + Task 2).

- [ ] **Step 5: Commit**

```bash
git add internal/native/native.go internal/native/native_test.go
git commit -m "feat(native): MemFile parse + List with frontmatter tolerances"
```

---

### Task 3: `internal/sidecar` — schema + Open/Close

**Files:**
- Create: `internal/sidecar/schema.go`
- Create: `internal/sidecar/sidecar.go`
- Test: `internal/sidecar/sidecar_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/sidecar/sidecar_test.go
package sidecar

import (
	"path/filepath"
	"testing"
)

func TestOpenCreatesSchemaAndCloses(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sub", ".sidecar.db")
	s, err := Open(dbPath) // also creates the parent "sub" dir
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Schema present: querying an empty table must succeed, not error.
	if _, _, err := s.Get("nonexistent.md"); err != nil {
		t.Fatalf("Get on empty table: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), ".sidecar.db")
	s1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1.Close()
	s2, err := Open(dbPath) // re-open existing file: IF NOT EXISTS schema
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	s2.Close()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sidecar/ -v`
Expected: FAIL — `undefined: Open` / `undefined: Get`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/sidecar/schema.go
package sidecar

// Schema is the sidecar's table set. The sidecar is a DERIVED projection:
// it can be deleted and rebuilt from the WAL + native frontmatter (see
// Reproject). effectiveness defaults to the neutral Bayesian prior
// (0+1)/(0+0+2)=0.5 so a brand-new fact is never zeroed out of ranking.
const Schema = `
CREATE TABLE IF NOT EXISTS memory (
  slug          TEXT PRIMARY KEY,
  content_sha   TEXT,
  type          TEXT,
  name          TEXT,
  description   TEXT,
  project       TEXT,
  domains       TEXT,
  created       TEXT,
  last_injected TEXT,
  ref_count     INTEGER NOT NULL DEFAULT 0,
  status        TEXT NOT NULL DEFAULT 'active',
  effectiveness REAL NOT NULL DEFAULT 0.5
);
CREATE TABLE IF NOT EXISTS keyword (slug TEXT, term TEXT, weight REAL);
CREATE TABLE IF NOT EXISTS outcome (slug TEXT, ts TEXT, kind TEXT);
CREATE TABLE IF NOT EXISTS meta    (k TEXT PRIMARY KEY, v TEXT);
CREATE INDEX IF NOT EXISTS idx_keyword_term ON keyword(term);
CREATE INDEX IF NOT EXISTS idx_outcome_slug ON outcome(slug);
`
```

```go
// internal/sidecar/sidecar.go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sidecar/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sidecar/schema.go internal/sidecar/sidecar.go internal/sidecar/sidecar_test.go
git commit -m "feat(sidecar): SQLite schema + Open/Get/Close"
```

---

### Task 4: `internal/sidecar` — Upsert + List

**Files:**
- Modify: `internal/sidecar/sidecar.go` (append `Upsert` and `All`)
- Test: `internal/sidecar/sidecar_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
// append to internal/sidecar/sidecar_test.go
func TestUpsertInsertsAndUpdates(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), ".sidecar.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	rec := Record{Slug: "m.md", Type: "mistake", Name: "M", RefCount: 3,
		Status: "active", Effectiveness: 0.8, ContentSHA: "sha1"}
	if err := s.Upsert(rec); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, ok, err := s.Get("m.md")
	if err != nil || !ok {
		t.Fatalf("Get after insert: ok=%v err=%v", ok, err)
	}
	if got.RefCount != 3 || got.Type != "mistake" || got.Effectiveness != 0.8 {
		t.Errorf("inserted record wrong: %+v", got)
	}

	rec.RefCount = 5
	rec.ContentSHA = "sha2"
	if err := s.Upsert(rec); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _, _ = s.Get("m.md")
	if got.RefCount != 5 || got.ContentSHA != "sha2" {
		t.Errorf("updated record wrong: %+v", got)
	}

	all, err := s.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("All len = %d, want 1 (upsert must not duplicate)", len(all))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sidecar/ -run TestUpsert -v`
Expected: FAIL — `s.Upsert undefined` / `s.All undefined`.

- [ ] **Step 3: Write minimal implementation**

```go
// append to internal/sidecar/sidecar.go

// Upsert inserts or replaces the memory row keyed by slug.
func (s *Store) Upsert(r Record) error {
	if r.Status == "" {
		r.Status = "active"
	}
	_, err := s.db.Exec(`
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
func (s *Store) All() ([]Record, error) {
	rows, err := s.db.Query(`SELECT slug, content_sha, type, name, description,
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sidecar/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sidecar/sidecar.go internal/sidecar/sidecar_test.go
git commit -m "feat(sidecar): Upsert + All"
```

---

### Task 5: `internal/sidecar` — Reproject from native files + WAL

**Files:**
- Create: `internal/sidecar/reproject.go`
- Test: `internal/sidecar/reproject_test.go`

**Context:** Reproject is the determinism guarantee — given the native files and the WAL, it rebuilds the `memory` and `outcome` tables. ref_count = count of `inject`/`inject-agg` events for that slug; effectiveness = `(pos+1)/(pos+neg+2)` from `outcome-positive`/`outcome-negative`; created = earliest WAL date for the slug; last_injected = latest inject date. Project/Type are taken from arguments the caller supplies per file (Phase 1 leaves them empty for the diagnostic verb; the migration tool in Phase 6 supplies fine-grained type/project). The `keyword` table is intentionally left empty here (Phase 2 owns tokenization).

- [ ] **Step 1: Write the failing test**

```go
// internal/sidecar/reproject_test.go
package sidecar

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

func TestReproject(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".sidecar.db")
	walPath := filepath.Join(dir, ".wal")

	wal := "" +
		"2026-04-01|inject|m.md|s1\n" +
		"2026-04-02|inject|m.md|s2\n" +
		"2026-04-02|outcome-positive|m.md|s2\n" +
		"2026-04-03|inject|m.md|s3\n" +
		"2026-04-03|outcome-negative|m.md|s3\n" +
		"2026-04-01|inject|n.md|s1\n"
	if err := os.WriteFile(walPath, []byte(wal), 0o644); err != nil {
		t.Fatalf("write wal: %v", err)
	}

	files := []native.MemFile{
		{Slug: "m.md", Type: "feedback", Name: "M", ContentSHA: "sha-m"},
		{Slug: "n.md", Type: "reference", Name: "N", ContentSHA: "sha-n"},
		// "orphan" native file with no WAL history: must still be inserted
		// with ref_count 0 and neutral effectiveness (injectable from day 1).
		{Slug: "fresh.md", Type: "reference", Name: "F", ContentSHA: "sha-f"},
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if err := Reproject(s, files, walPath); err != nil {
		t.Fatalf("Reproject: %v", err)
	}

	m, ok, _ := s.Get("m.md")
	if !ok {
		t.Fatal("m.md missing")
	}
	if m.RefCount != 3 {
		t.Errorf("m.md ref_count = %d, want 3", m.RefCount)
	}
	// 1 positive, 1 negative -> (1+1)/(1+1+2) = 0.5
	if m.Effectiveness != 0.5 {
		t.Errorf("m.md effectiveness = %v, want 0.5", m.Effectiveness)
	}
	if m.Created != "2026-04-01" || m.LastInjected != "2026-04-03" {
		t.Errorf("m.md dates: created=%q last=%q", m.Created, m.LastInjected)
	}

	fresh, ok, _ := s.Get("fresh.md")
	if !ok {
		t.Fatal("fresh.md missing — orphan native files must be inserted")
	}
	if fresh.RefCount != 0 || fresh.Effectiveness != 0.5 {
		t.Errorf("fresh.md = %+v, want ref_count 0 / effectiveness 0.5", fresh)
	}
}

func TestReproject_IsDeterministic(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	os.WriteFile(walPath, []byte("2026-04-01|inject|m.md|s1\n"), 0o644)
	files := []native.MemFile{{Slug: "m.md", ContentSHA: "x"}}

	first := reprojectInto(t, filepath.Join(dir, "a.db"), files, walPath)
	second := reprojectInto(t, filepath.Join(dir, "b.db"), files, walPath)
	if first != second {
		t.Errorf("reproject not deterministic:\n a=%+v\n b=%+v", first, second)
	}
}

func reprojectInto(t *testing.T, dbPath string, files []native.MemFile, walPath string) Record {
	t.Helper()
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if err := Reproject(s, files, walPath); err != nil {
		t.Fatalf("Reproject: %v", err)
	}
	r, _, _ := s.Get("m.md")
	return r
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sidecar/ -run TestReproject -v`
Expected: FAIL — `undefined: Reproject`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/sidecar/reproject.go
package sidecar

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

type agg struct {
	injects    int
	pos        int
	neg        int
	created    string // earliest WAL date for the slug
	lastInject string // latest inject date
}

// Reproject rebuilds the memory + outcome tables from the supplied native
// files and the WAL at walPath. Existing rows are upserted (idempotent).
// Native files with no WAL history are inserted with ref_count 0 and the
// neutral effectiveness prior, so freshly-written facts are injectable
// immediately. The keyword table is left untouched (Phase 2 owns it).
func Reproject(s *Store, files []native.MemFile, walPath string) error {
	aggs := readWALAgg(walPath)

	if _, err := s.db.Exec(`DELETE FROM outcome`); err != nil {
		return fmt.Errorf("sidecar.Reproject: clear outcome: %w", err)
	}

	for _, f := range files {
		a := aggs[f.Slug]
		if a == nil {
			a = &agg{}
		}
		eff := float64(a.pos+1) / float64(a.pos+a.neg+2)
		rec := Record{
			Slug:          f.Slug,
			ContentSHA:    f.ContentSHA,
			Type:          f.Type,
			Name:          f.Name,
			Description:   f.Description,
			Created:       a.created,
			LastInjected:  a.lastInject,
			RefCount:      a.injects,
			Status:        "active",
			Effectiveness: eff,
		}
		if err := s.Upsert(rec); err != nil {
			return fmt.Errorf("sidecar.Reproject: upsert %s: %w", f.Slug, err)
		}
	}

	// Replay outcome events into the detail table (positive/negative only;
	// kind is the raw WAL event name).
	if err := replayOutcomes(s, walPath); err != nil {
		return fmt.Errorf("sidecar.Reproject: outcomes: %w", err)
	}
	return nil
}

func readWALAgg(walPath string) map[string]*agg {
	out := map[string]*agg{}
	f, err := os.Open(walPath)
	if err != nil {
		return out // missing WAL -> no history, not fatal
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		date, event, slug, ok := parseWALLine(sc.Text())
		if !ok {
			continue
		}
		a := out[slug]
		if a == nil {
			a = &agg{}
			out[slug] = a
		}
		switch event {
		case "inject", "inject-agg":
			a.injects++
			if date > a.lastInject {
				a.lastInject = date
			}
		case "outcome-positive":
			a.pos++
		case "outcome-negative":
			a.neg++
		}
		if a.created == "" || date < a.created {
			a.created = date
		}
	}
	return out
}

func replayOutcomes(s *Store, walPath string) error {
	f, err := os.Open(walPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		date, event, slug, ok := parseWALLine(sc.Text())
		if !ok {
			continue
		}
		if event == "outcome-positive" || event == "outcome-negative" {
			if _, err := s.db.Exec(`INSERT INTO outcome (slug, ts, kind) VALUES (?,?,?)`,
				slug, date, strings.TrimPrefix(event, "outcome-")); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseWALLine splits a 4-column WAL line "DATE|EVENT|SLUG|SESSION". The bool
// is false for blank lines, comments, or malformed rows.
func parseWALLine(line string) (date, event, slug string, ok bool) {
	line = strings.TrimRight(line, "\r")
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", "", false
	}
	parts := strings.SplitN(line, "|", 4)
	if len(parts) != 4 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sidecar/ -v`
Expected: PASS (Reproject + determinism + earlier tasks).

- [ ] **Step 5: Commit**

```bash
git add internal/sidecar/reproject.go internal/sidecar/reproject_test.go
git commit -m "feat(sidecar): Reproject memory+outcome from native files + WAL"
```

---

### Task 6: `memoryctl sidecar` verb (rebuild + show)

**Files:**
- Create: `cmd/memoryctl/sidecar.go`
- Modify: `cmd/memoryctl/main.go` (add `case "sidecar":` to the top-level switch)
- Test: `cmd/memoryctl/sidecar_test.go`

**Context:** This wires the two packages into a runnable, diagnostic verb so Phase 1 produces working software. `rebuild` lists native files from the current project dir + the global dir, reprojects them into `<memoryDir>/.sidecar.db` against `<memoryDir>/.wal`, and prints a one-line summary. `show` prints every memory row. The CWD used for the per-project dir is read from `$CLAUDE_PROJECT_CWD` (set by the hook contract), falling back to `os.Getwd()`. The Claude home is `claudeDir()` (already in main.go, honours `$CLAUDE_HOME`).

- [ ] **Step 1: Write the failing test**

```go
// cmd/memoryctl/sidecar_test.go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSidecarRebuildAndShow(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("mkdir mem: %v", err)
	}
	// One native fact in the project memory dir.
	os.WriteFile(filepath.Join(projDir, "feedback_x.md"),
		[]byte("---\nname: Rule X\ndescription: do X\ntype: feedback\n---\nbody\n"), 0o644)
	// A WAL with two injects + one positive outcome for it.
	os.WriteFile(filepath.Join(memDir, ".wal"),
		[]byte("2026-04-01|inject|feedback_x.md|s1\n2026-04-02|inject|feedback_x.md|s2\n2026-04-02|outcome-positive|feedback_x.md|s2\n"), 0o644)

	env := map[string]string{
		"CLAUDE_HOME":        filepath.Join(home, ".claude"),
		"CLAUDE_MEMORY_DIR":  memDir,
		"CLAUDE_PROJECT_CWD": "/tmp/proj",
	}

	out, errOut, code := run(t, env, "sidecar", "rebuild")
	if code != 0 {
		t.Fatalf("rebuild exit=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "1 file") && !strings.Contains(out, "rebuilt") {
		t.Errorf("rebuild summary unexpected: %q", out)
	}

	out, errOut, code = run(t, env, "sidecar", "show")
	if code != 0 {
		t.Fatalf("show exit=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "feedback_x.md") || !strings.Contains(out, "ref=2") {
		t.Errorf("show output missing slug/ref: %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/memoryctl/ -run TestSidecarRebuildAndShow -v`
Expected: FAIL — unknown command `sidecar` (exit 2) or compile error `undefined: runSidecar`.

- [ ] **Step 3a: Add the dispatch case in main.go**

In `cmd/memoryctl/main.go`, inside the top-level `switch os.Args[1] {` block (alongside the existing `case "fts":`, `case "dedup":` …), add:

```go
	case "sidecar":
		if len(os.Args) < 3 {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		switch os.Args[2] {
		case "rebuild":
			runSidecarRebuild(os.Args[3:])
		case "show":
			runSidecarShow(os.Args[3:])
		default:
			fmt.Fprintf(os.Stderr, "memoryctl: unknown sidecar subcommand %q\n", os.Args[2])
			os.Exit(2)
		}
```

- [ ] **Step 3b: Implement the verb**

```go
// cmd/memoryctl/sidecar.go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

// sidecarPath is the derived SQLite projection, alongside the WAL + fts index.
func sidecarPath() string { return filepath.Join(memoryDir(), ".sidecar.db") }

// projectCWD resolves the working directory used for the per-project native
// memory dir. The hook contract exports $CLAUDE_PROJECT_CWD; fall back to the
// process cwd so the verb is usable standalone.
func projectCWD() string {
	if d := os.Getenv("CLAUDE_PROJECT_CWD"); d != "" {
		return d
	}
	cwd, _ := os.Getwd()
	return cwd
}

// collectNative lists native files from both the per-project dir and the
// hypomnema-owned global dir (native lacks a global store).
func collectNative() ([]native.MemFile, error) {
	home := claudeDir()
	homeParent := filepath.Dir(home) // claudeDir() == <home>/.claude
	projFiles, err := native.List(native.ProjectMemoryDir(homeParent, projectCWD()))
	if err != nil {
		return nil, err
	}
	globalFiles, err := native.List(native.GlobalMemoryDir(homeParent))
	if err != nil {
		return nil, err
	}
	return append(projFiles, globalFiles...), nil
}

func runSidecarRebuild(args []string) {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Print(usage)
			return
		}
		fmt.Fprintf(os.Stderr, "memoryctl sidecar rebuild: unknown flag %q\n", a)
		os.Exit(2)
	}
	files, err := collectNative()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sidecar rebuild: %v\n", err)
		os.Exit(1)
	}
	s, err := sidecar.Open(sidecarPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "sidecar rebuild: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()
	if err := sidecar.Reproject(s, files, filepath.Join(memoryDir(), ".wal")); err != nil {
		fmt.Fprintf(os.Stderr, "sidecar rebuild: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("rebuilt %s: %d file(s)\n", sidecarPath(), len(files))
}

func runSidecarShow(args []string) {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Print(usage)
			return
		}
		fmt.Fprintf(os.Stderr, "memoryctl sidecar show: unknown flag %q\n", a)
		os.Exit(2)
	}
	s, err := sidecar.Open(sidecarPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "sidecar show: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()
	recs, err := s.All()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sidecar show: %v\n", err)
		os.Exit(1)
	}
	for _, r := range recs {
		fmt.Printf("%-40s type=%-10s ref=%-3d eff=%.2f status=%s\n",
			r.Slug, r.Type, r.RefCount, r.Effectiveness, r.Status)
	}
}
```

`claudeDir()` returns `<home>/.claude`, so `filepath.Dir(claudeDir())` is `<home>` — the argument `native.ProjectMemoryDir`/`GlobalMemoryDir` expect. The test sets `CLAUDE_HOME=<tmp>/.claude`, so `homeParent` resolves to `<tmp>`, matching the dirs it created.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/memoryctl/ -run TestSidecarRebuildAndShow -v`
Expected: PASS.

Then full suite + race + build:

Run: `go build ./... && go test -race ./...`
Expected: PASS across all packages (no regressions).

- [ ] **Step 5: Commit**

```bash
git add cmd/memoryctl/sidecar.go cmd/memoryctl/main.go cmd/memoryctl/sidecar_test.go
git commit -m "feat(memoryctl): sidecar rebuild + show verb"
```

---

## Phase 1 Definition of Done

- [ ] `internal/native` reads native memory dirs (project + global), parses minimal frontmatter, computes content_sha, tolerates BOM/CRLF/quotes, skips MEMORY.md.
- [ ] `internal/sidecar` opens a SQLite projection, Upsert/Get/All work, Reproject deterministically rebuilds memory+outcome from native files + WAL.
- [ ] Orphan/fresh native files (no WAL history) project to ref_count 0 + effectiveness 0.5 (injectable from day 1) — zero-safe per spec §3.3.
- [ ] `memoryctl sidecar rebuild|show` runs end-to-end against a real dir layout.
- [ ] `go test -race ./...` green; `make build` produces the binary.
- [ ] No existing hook behaviour changed (additive only).

## Self-Review (against spec §3.3, §3.4, §4, §5)

- **Spec §3.4 schema** — all four tables (memory/keyword/outcome/meta) created; types match (slug PK, content_sha, ref_count int, status, effectiveness real). ✓ (keyword left empty — declared boundary, Phase 2 populates).
- **Spec §3.3 native adapter read-only** — `native` only reads; never writes content. ✓
- **Spec §5 graceful degradation** — missing dir → empty (not error); missing WAL → no history (not error); unparseable file → skipped. ✓
- **Spec §5 fresh native fact injectable** — orphan files projected with neutral effectiveness 0.5, ref_count 0. ✓ (tested in TestReproject).
- **Spec §3.4 WAL=truth/SQLite=projection** — Reproject is deterministic + idempotent (tested). ✓
- **Type consistency** — `Record` fields used identically across Upsert/Get/All/Reproject; `parseWALLine` signature stable; `sidecarPath()`/`collectNative()` defined once and reused. ✓
- **No placeholders** — every step has complete, compilable code + exact run commands. ✓
- **Global store (spec §4.2)** — `collectNative` unions project + global dirs; `GlobalMemoryDir` honours override. ✓
