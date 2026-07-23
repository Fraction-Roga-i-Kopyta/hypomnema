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
		{Slug: "fresh.md", Type: "reference", Name: "F", ContentSHA: "sha-f"},
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if err := Reproject(s, files, walPath, nil); err != nil {
		t.Fatalf("Reproject: %v", err)
	}

	m, ok, _ := s.Get("m.md")
	if !ok {
		t.Fatal("m.md missing")
	}
	if m.RefCount != 3 {
		t.Errorf("m.md ref_count = %d, want 3", m.RefCount)
	}
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

	n, ok, _ := s.Get("n.md")
	if !ok {
		t.Fatal("n.md missing")
	}
	if n.RefCount != 1 || n.Created != "2026-04-01" || n.LastInjected != "2026-04-01" {
		t.Errorf("n.md = %+v, want ref_count 1 / created+last 2026-04-01", n)
	}
}

func TestReproject_InjectAggCount(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	// one plain inject (=1) + one aggregated inject-agg of 20 -> ref_count 21
	wal := "2026-04-01|inject|x.md|s1\n2026-04-02|inject-agg|x.md|20\n"
	if err := os.WriteFile(walPath, []byte(wal), 0o644); err != nil {
		t.Fatalf("write wal: %v", err)
	}
	files := []native.MemFile{{Slug: "x.md", ContentSHA: "x"}}
	s, err := Open(filepath.Join(dir, ".sidecar.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if err := Reproject(s, files, walPath, nil); err != nil {
		t.Fatalf("Reproject: %v", err)
	}
	r, _, _ := s.Get("x.md")
	if r.RefCount != 21 {
		t.Errorf("ref_count = %d, want 21 (1 inject + inject-agg of 20)", r.RefCount)
	}
	if r.LastInjected != "2026-04-02" {
		t.Errorf("last_injected = %q, want 2026-04-02", r.LastInjected)
	}
}

func TestReproject_IsDeterministic(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	if err := os.WriteFile(walPath, []byte("2026-04-01|inject|m.md|s1\n"), 0o644); err != nil {
		t.Fatalf("write wal: %v", err)
	}
	files := []native.MemFile{{Slug: "m.md", ContentSHA: "x"}}

	first := reprojectInto(t, filepath.Join(dir, "a.db"), files, walPath)
	second := reprojectInto(t, filepath.Join(dir, "b.db"), files, walPath)
	if first != second {
		t.Errorf("reproject not deterministic:\n a=%+v\n b=%+v", first, second)
	}
}

func TestReproject_MarksDeletedOrphans(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	if err := os.WriteFile(walPath, []byte("2026-04-01|inject|a.md|s1\n2026-04-01|inject|b.md|s1\n"), 0o644); err != nil {
		t.Fatalf("write wal: %v", err)
	}
	s, err := Open(filepath.Join(dir, ".sidecar.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// First rebuild: both a.md and b.md present.
	if err := Reproject(s, []native.MemFile{{Slug: "a.md", ContentSHA: "a"}, {Slug: "b.md", ContentSHA: "b"}}, walPath, nil); err != nil {
		t.Fatalf("reproject 1: %v", err)
	}
	// Second rebuild: b.md is gone (no longer in files).
	if err := Reproject(s, []native.MemFile{{Slug: "a.md", ContentSHA: "a"}}, walPath, nil); err != nil {
		t.Fatalf("reproject 2: %v", err)
	}

	a, _, _ := s.Get("a.md")
	if a.Status != "active" {
		t.Errorf("a.md status = %q, want active", a.Status)
	}
	b, ok, _ := s.Get("b.md")
	if !ok {
		t.Fatal("b.md row must be KEPT (history preserved), not removed")
	}
	if b.Status != "deleted" {
		t.Errorf("b.md status = %q, want deleted (orphan)", b.Status)
	}
}

func TestReproject_MatchesV1SlugsWithoutMdSuffix(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	// v1-style WAL: slug has NO .md suffix.
	os.WriteFile(walPath, []byte("2026-04-01|inject|code-approach|s1\n2026-04-02|inject|code-approach|s2\n2026-04-02|outcome-positive|code-approach|s2\n"), 0o644)
	s, _ := Open(filepath.Join(dir, ".sidecar.db"))
	defer s.Close()
	// native file slug DOES have .md.
	files := []native.MemFile{{Slug: "code-approach.md", ContentSHA: "x"}}
	if err := Reproject(s, files, walPath, nil); err != nil {
		t.Fatal(err)
	}
	r, ok, _ := s.Get("code-approach.md")
	if !ok || r.RefCount != 2 {
		t.Errorf("v1 WAL (no .md) must seed the .md native file: ref_count=%d ok=%v, want 2", r.RefCount, ok)
	}
	if r.Created != "2026-04-01" {
		t.Errorf("created should seed from v1 WAL, got %q", r.Created)
	}
}

func reprojectInto(t *testing.T, dbPath string, files []native.MemFile, walPath string) Record {
	t.Helper()
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if err := Reproject(s, files, walPath, nil); err != nil {
		t.Fatalf("Reproject: %v", err)
	}
	r, _, _ := s.Get("m.md")
	return r
}

// TestReproject_ScopedTombstone is the regression for the cross-project
// tombstoning bug: the sidecar is shared across projects, but each close
// only sees "current project + global" files. A close must therefore only
// reconcile deletions inside its own scope — never another project's rows.
func TestReproject_ScopedTombstone(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	if err := os.WriteFile(walPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, ".sidecar.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	projA, projB := "-proj-a", "-proj-b"
	g := native.MemFile{Slug: "g.md", ContentSHA: "g", Project: "global"}
	a1 := native.MemFile{Slug: "a1.md", ContentSHA: "a1", Project: projA}
	b1 := native.MemFile{Slug: "b1.md", ContentSHA: "b1", Project: projB}

	// Close from project A, then from project B.
	if err := Reproject(s, []native.MemFile{a1, g}, walPath, []string{projA, "global"}); err != nil {
		t.Fatalf("reproject A: %v", err)
	}
	if err := Reproject(s, []native.MemFile{b1, g}, walPath, []string{projB, "global"}); err != nil {
		t.Fatalf("reproject B: %v", err)
	}
	a, _, _ := s.Get("a1.md")
	if a.Status != "active" {
		t.Errorf("a1.md was tombstoned by another project's close: status=%q", a.Status)
	}
	if a.Project != projA {
		t.Errorf("a1.md project = %q, want %q", a.Project, projA)
	}

	// A's file disappears from disk: a close from A tombstones it; B stays.
	if err := Reproject(s, []native.MemFile{g}, walPath, []string{projA, "global"}); err != nil {
		t.Fatalf("reproject A2: %v", err)
	}
	if a, _, _ := s.Get("a1.md"); a.Status != "deleted" {
		t.Errorf("a1.md should be tombstoned by its own project's close, got %q", a.Status)
	}
	if b, _, _ := s.Get("b1.md"); b.Status != "active" {
		t.Errorf("b1.md must survive project A's close, got %q", b.Status)
	}
	if gr, _, _ := s.Get("g.md"); gr.Status != "active" {
		t.Errorf("g.md (global, still on disk) must stay active, got %q", gr.Status)
	}
}

// A sidecar from a previous schema generation is a stale projection: Open
// must wipe and recreate it (it rebuilds from WAL + native on next use).
func TestOpen_RecreatesOnSchemaVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".sidecar.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE meta SET v='1' WHERE k='schema_version'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(Record{Slug: "old.md", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	var v string
	if err := s2.db.QueryRow(`SELECT v FROM meta WHERE k='schema_version'`).Scan(&v); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if v != "4" {
		t.Errorf("schema_version = %q, want 4", v)
	}
	if _, ok, _ := s2.Get("old.md"); ok {
		t.Error("rows from an old schema generation must not survive (projection is rebuildable)")
	}
}

// Keywords of rows outside the populated file set must survive — otherwise a
// close from one project wipes every other project's relevance signal.
func TestPopulateKeywords_PreservesOtherSlugs(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), ".sidecar.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	a := native.MemFile{Slug: "a.md", Name: "alpha", Body: "kubernetes restart"}
	b := native.MemFile{Slug: "b.md", Name: "beta", Body: "postgres index"}
	if err := s.PopulateKeywords([]native.MemFile{a}); err != nil {
		t.Fatal(err)
	}
	if err := s.PopulateKeywords([]native.MemFile{b}); err != nil {
		t.Fatal(err)
	}
	overlap, err := s.OverlapScores([]string{"kubernetes"})
	if err != nil {
		t.Fatal(err)
	}
	if overlap["a.md"] != 1 {
		t.Errorf("a.md keyword signal wiped by a later populate of other files: %v", overlap)
	}
}

// Effectiveness must learn from the trigger events close already writes:
// one observation per (slug, session), useful wins over silent within a
// session, retro-silents count, and legacy outcome-* events still add in.
func TestReproject_EffectivenessFromTriggerEvents(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	wal := "" +
		"2026-04-01|inject|m.md|s1\n" +
		"2026-04-01|trigger-silent|m.md|s1\n" + // flips to useful below
		"2026-04-01|trigger-useful|m.md|s1\n" +
		"2026-04-02|inject|m.md|s2\n" +
		"2026-04-02|trigger-silent|m.md|s2\n" +
		"2026-04-02|outcome-positive|m.md|s2\n" + // legacy signal still counts
		"2026-04-03|inject|n.md|s1\n" +
		"2026-04-03|trigger-silent|n.md|s1\n" +
		"2026-04-04|trigger-silent|n.md|s2\n" +
		"2026-04-05|trigger-silent-retro|n.md|s3\n"
	if err := os.WriteFile(walPath, []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	files := []native.MemFile{
		{Slug: "m.md", ContentSHA: "m"},
		{Slug: "n.md", ContentSHA: "n"},
	}
	if err := Reproject(s, files, walPath, nil); err != nil {
		t.Fatal(err)
	}

	// m: useful s1 + silent s2 + legacy positive → (2+1)/(2+1+2) = 0.6
	if r, _, _ := s.Get("m.md"); r.Effectiveness != 0.6 {
		t.Errorf("m.md effectiveness = %v, want 0.6 (1 useful session + 1 legacy positive vs 1 silent session)", r.Effectiveness)
	}
	// n: three silent sessions, never useful → (0+1)/(0+3+2) = 0.2
	if r, _, _ := s.Get("n.md"); r.Effectiveness != 0.2 {
		t.Errorf("n.md effectiveness = %v, want 0.2 (3 silent sessions)", r.Effectiveness)
	}
}

// Frontmatter created/status finally drive the projection: created is the
// author's recency claim (WAL-earliest only a fallback), and pinned is
// honoured instead of being hard-reset to active on every close.
func TestReproject_FrontmatterCreatedAndPinned(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	if err := os.WriteFile(walPath, []byte("2026-04-01|inject|pin.md|s1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	files := []native.MemFile{
		{Slug: "pin.md", ContentSHA: "p", Created: "2026-06-01", Status: "pinned"},
		{Slug: "plain.md", ContentSHA: "q"},
	}
	if err := Reproject(s, files, walPath, nil); err != nil {
		t.Fatal(err)
	}
	pin, _, _ := s.Get("pin.md")
	if pin.Created != "2026-06-01" {
		t.Errorf("frontmatter created must win over WAL-earliest, got %q", pin.Created)
	}
	if pin.Status != "pinned" {
		t.Errorf("frontmatter pinned must survive reproject, got %q", pin.Status)
	}
	if plain, _, _ := s.Get("plain.md"); plain.Status != "active" {
		t.Errorf("file without status stays active, got %q", plain.Status)
	}
}

func TestReprojectRecallEvent(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	wal := filepath.Join(dir, ".wal")
	if err := os.WriteFile(wal, []byte(
		"2026-06-01|inject|fact.md|s1\n"+
			"2026-06-10|recall|fact.md|s2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := []native.MemFile{{Slug: "fact.md", Name: "fact", Project: "proj", Created: "2026-05-01"}}
	if err := Reproject(s, files, wal, []string{"proj"}); err != nil {
		t.Fatal(err)
	}

	r, ok, err := s.Get("fact.md")
	if err != nil || !ok {
		t.Fatalf("get: %v ok=%v", err, ok)
	}
	if r.RefCount != 2 {
		t.Errorf("recall must count toward ref_count: want 2, got %d", r.RefCount)
	}
	if r.LastInjected != "2026-06-10" {
		t.Errorf("recall must advance last_injected: want 2026-06-10, got %q", r.LastInjected)
	}
}

func TestRecallRevivesStale(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	wal := filepath.Join(dir, ".wal")
	if err := os.WriteFile(wal, []byte(
		"2025-01-01|inject|old.md|s1\n"+
			"2026-06-10|recall|old.md|s2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := []native.MemFile{{Slug: "old.md", Name: "old", Project: "proj", Created: "2025-01-01"}}
	if err := Reproject(s, files, wal, []string{"proj"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkStale("2026-06-10"); err != nil {
		t.Fatal(err)
	}
	r, ok, err := s.Get("old.md")
	if err != nil || !ok {
		t.Fatalf("get old.md: err=%v ok=%v", err, ok)
	}
	if r.Status != "active" {
		t.Errorf("fresh recall must keep fact out of stale, got %q", r.Status)
	}
}

func TestReproject_ErrorRollsBackOutcomeClear(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(
		`INSERT INTO outcome (slug, ts, kind) VALUES ('x','2026-07-01','positive')`); err != nil {
		t.Fatal(err)
	}
	// A directory as walPath: readWALAgg swallows its read error by design
	// (partial history is acceptable), but replayOutcomes propagates it —
	// so Reproject fails AFTER `DELETE FROM outcome` has already run.
	walDir := filepath.Join(dir, "wal-as-dir")
	if err := os.MkdirAll(walDir, 0o755); err != nil {
		t.Fatal(err)
	}
	err = Reproject(s, []native.MemFile{{Slug: "a.md", Project: "p"}}, walDir, []string{"p"})
	if err == nil {
		t.Fatal("expected Reproject to fail on an unreadable WAL path")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM outcome`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("failed Reproject lost outcome rows (have %d, want 1) — projection is not transactional", n)
	}
}

// appendLine appends one WAL line (test helper).
func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

// TestReproject_RetiredStatus: a fact with a `retire` WAL event and a missing
// native file projects as status='retired', not 'deleted'; a later `revive`
// event cancels the retirement (missing file then projects as 'deleted').
// Retirement must also survive a from-scratch rebuild (empty sidecar).
func TestReproject_RetiredStatus(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	q := native.QKey("projA", "old-fact")
	wal := "2026-07-01|inject|" + q + "|s1\n" +
		"2026-07-20|retire|" + q + ">new-owner:promoted_to_hook|cli\n"
	if err := os.WriteFile(walPath, []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Row exists from a previous projection; file now absent from `files`.
	if err := s.Upsert(Record{Slug: "old-fact.md", Project: "projA", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := Reproject(s, nil, walPath, []string{"projA"}); err != nil {
		t.Fatal(err)
	}
	r, ok, _ := s.Get("old-fact.md")
	if !ok || r.Status != "retired" {
		t.Fatalf("status = %q (ok=%v), want retired", r.Status, ok)
	}

	// From-scratch rebuild: a fresh sidecar with the same WAL and no files
	// must still know the retirement was deliberate.
	s2, err := Open(filepath.Join(dir, ".sidecar2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := Reproject(s2, nil, walPath, []string{"projA"}); err != nil {
		t.Fatal(err)
	}
	r, ok, _ = s2.Get("old-fact.md")
	if !ok || r.Status != "retired" {
		t.Fatalf("from-scratch: status = %q (ok=%v), want retired row inserted", r.Status, ok)
	}

	// Revive cancels: missing file then falls back to plain deletion.
	appendLine(t, walPath, "2026-07-21|revive|"+q+"|cli")
	if err := s.Upsert(Record{Slug: "old-fact.md", Project: "projA", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := Reproject(s, nil, walPath, []string{"projA"}); err != nil {
		t.Fatal(err)
	}
	if r, _, _ := s.Get("old-fact.md"); r.Status != "deleted" {
		t.Fatalf("after revive: status = %q, want deleted", r.Status)
	}
}
