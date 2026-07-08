package sidecar

import (
	"path/filepath"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

func TestReproject_CrossProjectKeywordNoClobber(t *testing.T) { // review E5
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, ".sidecar.db"))
	defer s.Close()
	wal := filepath.Join(dir, ".wal")

	// Project A: notes.md about react/hooks.
	Reproject(s, []native.MemFile{{Slug: "notes.md", Project: "projA", Type: "note", Name: "notes", Keywords: []string{"react", "hooks"}}}, wal, []string{"projA"})
	// Project B: a DIFFERENT notes.md (same basename) about postgres.
	Reproject(s, []native.MemFile{{Slug: "notes.md", Project: "projB", Type: "note", Name: "notes", Keywords: []string{"postgres", "sql"}}}, wal, []string{"projB"})

	// Project A's overlap must survive project B's reproject (was clobbered 2→0).
	ov, _ := s.OverlapScores([]string{"react", "hooks"})
	if ov["notes.md"] != 2 {
		t.Errorf("projA keyword rows clobbered by projB reproject: overlap(react,hooks)=%d, want 2", ov["notes.md"])
	}
	ov2, _ := s.OverlapScores([]string{"postgres", "sql"})
	if ov2["notes.md"] != 2 {
		t.Errorf("projB keyword rows missing: overlap(postgres,sql)=%d, want 2", ov2["notes.md"])
	}
}
