package sidecar

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// last_useful is the latest cite-useful date — the model's own use
// signal, distinct from last_injected (the ranker's output). recall does not
// count: skill-inject writes the same event as a push, and a recalled fact
// that is actually used shows up as cite-useful at close.
func TestReproject_LastUseful(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	wal := "2026-04-01|inject|x.md|s1\n" +
		"2026-04-02|cite-silent|x.md|s1\n" +
		"2026-04-03|inject|x.md|s2\n" +
		"2026-04-03|cite-useful|x.md|s2\n" +
		"2026-04-05|recall|x.md|s3\n" + // delivery, not use — bumps last_injected only
		"2026-04-06|holdout-hit|x.md|s4\n" + // never counts
		"2026-04-07|inject|x.md|s5\n" + // injection alone does not move last_useful
		"2026-04-07|cite-silent|x.md|s5\n"
	if err := os.WriteFile(walPath, []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	files := []native.MemFile{{Slug: "x.md", ContentSHA: "x"}}
	s, err := Open(filepath.Join(dir, ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := Reproject(s, files, walPath, nil); err != nil {
		t.Fatalf("Reproject: %v", err)
	}
	r, _, _ := s.Get("x.md")
	if r.LastUseful != "2026-04-03" {
		t.Errorf("last_useful = %q, want 2026-04-03 (only cite-useful counts; recall/holdout/silent/inject do not)", r.LastUseful)
	}
	if r.LastInjected != "2026-04-07" {
		t.Errorf("last_injected = %q, want 2026-04-07 (unchanged semantics)", r.LastInjected)
	}
}

// A fact with history but no useful citation projects an empty last_useful.
func TestReproject_LastUsefulEmptyWhenNeverUseful(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	os.WriteFile(walPath, []byte("2026-04-01|inject|y.md|s1\n2026-04-01|cite-silent|y.md|s1\n"), 0o644)
	s, _ := Open(filepath.Join(dir, ".sidecar.db"))
	defer s.Close()
	if err := Reproject(s, []native.MemFile{{Slug: "y.md", ContentSHA: "y"}}, walPath, nil); err != nil {
		t.Fatal(err)
	}
	r, _, _ := s.Get("y.md")
	if r.LastUseful != "" {
		t.Errorf("last_useful = %q, want empty", r.LastUseful)
	}
}

// Qualified (project\x1fslug) and legacy bare-slug history merge with max-date.
func TestReproject_LastUsefulMergesLegacyAndQualified(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	os.WriteFile(walPath, []byte(
		"2026-04-01|cite-useful|z.md|s1\n"+
			"2026-04-09|cite-useful|-p\x1fz.md|s2\n"), 0o644)
	s, _ := Open(filepath.Join(dir, ".sidecar.db"))
	defer s.Close()
	if err := Reproject(s, []native.MemFile{{Slug: "z.md", ContentSHA: "z", Project: "-p"}}, walPath, []string{"-p"}); err != nil {
		t.Fatal(err)
	}
	r, _, _ := s.Get("z.md")
	if r.LastUseful != "2026-04-09" {
		t.Errorf("last_useful = %q, want 2026-04-09", r.LastUseful)
	}
}

// Round-trip through Upsert/Get/All keeps the new column.
func TestRecord_LastUsefulRoundTrip(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), ".sidecar.db"))
	defer s.Close()
	if err := s.Upsert(Record{Slug: "r.md", Project: "-p", LastUseful: "2026-05-01"}); err != nil {
		t.Fatal(err)
	}
	r, ok, _ := s.Get("r.md")
	if !ok || r.LastUseful != "2026-05-01" {
		t.Errorf("Get: %+v", r)
	}
	all, _ := s.All()
	if len(all) != 1 || all[0].LastUseful != "2026-05-01" {
		t.Errorf("All: %+v", all)
	}
}
