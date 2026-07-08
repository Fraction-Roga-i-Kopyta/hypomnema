package sidecar

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

func TestOverlapScores_HugeQueryChunks(t *testing.T) { // review E6
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, ".sidecar.db"))
	defer s.Close()
	// One slug whose keyword set includes "needle".
	if err := s.PopulateKeywords([]native.MemFile{
		{Slug: "x.md", Project: "p", Name: "x", Keywords: []string{"needle"}},
	}); err != nil {
		t.Fatal(err)
	}
	// A query with >32766 unique terms (past SQLite's variable limit) that
	// includes the needle. This errored ("too many SQL variables") and the
	// caller swallowed it → overlap silently zeroed.
	terms := make([]string, 0, 40000)
	terms = append(terms, "needle")
	for i := 0; i < 40000; i++ {
		terms = append(terms, fmt.Sprintf("filler%d", i))
	}
	ov, err := s.OverlapScores(terms)
	if err != nil {
		t.Fatalf("OverlapScores must chunk past the variable limit, got error: %v", err)
	}
	if ov["x.md"] != 1 {
		t.Errorf("overlap should find the needle across chunks: got %d, want 1", ov["x.md"])
	}
}

func TestReproject_PopulatesDomains(t *testing.T) { // review E7 (data half only)
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, ".sidecar.db"))
	defer s.Close()
	Reproject(s, []native.MemFile{
		{Slug: "a.md", Project: "p", Type: "mistake", Name: "a", Domains: []string{"backend", "integration"}},
	}, filepath.Join(dir, ".wal"), []string{"p"})
	r, ok, _ := s.Get("a.md")
	if !ok {
		t.Fatal("row missing")
	}
	if r.Domains != "backend,integration" {
		t.Errorf("Domains column not populated: got %q, want backend,integration", r.Domains)
	}
}
