package sidecar

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

func TestPopulateKeywordsAndOverlap(t *testing.T) {
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

	files := []native.MemFile{
		{Slug: "a.md", Name: "Docker cache", Description: "buildkit layer", Body: "docker docker layer", ContentSHA: "a"},
		{Slug: "b.md", Name: "SQL tuning", Description: "index plan", Body: "postgres index", ContentSHA: "b"},
	}
	if err := Reproject(s, files, walPath, nil); err != nil {
		t.Fatalf("Reproject: %v", err)
	}

	scores, err := s.OverlapScores([]string{"docker", "index", "missing"})
	if err != nil {
		t.Fatalf("OverlapScores: %v", err)
	}
	if scores[native.QKey("", "a.md")] != 1 {
		t.Errorf("a.md overlap = %d, want 1 (docker)", scores[native.QKey("", "a.md")])
	}
	if scores[native.QKey("", "b.md")] != 1 {
		t.Errorf("b.md overlap = %d, want 1 (index)", scores[native.QKey("", "b.md")])
	}

	scores2, err := s.OverlapScores([]string{"layer", "docker"})
	if err != nil {
		t.Fatalf("OverlapScores 2: %v", err)
	}
	if scores2[native.QKey("", "a.md")] != 2 {
		t.Errorf("a.md overlap = %d, want 2 (layer+docker)", scores2[native.QKey("", "a.md")])
	}

	empty, err := s.OverlapScores(nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("empty query: len=%d err=%v", len(empty), err)
	}
}

func TestPopulateKeywordsIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	os.WriteFile(walPath, []byte(""), 0o644)
	s, _ := Open(filepath.Join(dir, ".sidecar.db"))
	defer s.Close()
	files := []native.MemFile{{Slug: "a.md", Body: "docker layer cache", ContentSHA: "a"}}
	if err := Reproject(s, files, walPath, nil); err != nil {
		t.Fatal(err)
	}
	if err := Reproject(s, files, walPath, nil); err != nil {
		t.Fatal(err)
	}
	scores, _ := s.OverlapScores([]string{"docker"})
	if scores[native.QKey("", "a.md")] != 1 {
		t.Errorf("after 2 rebuilds, a.md overlap for 'docker' = %d, want 1 (no dup rows)", scores[native.QKey("", "a.md")])
	}
}

func TestOverlapScores_PerProject(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), ".sidecar.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if err := s.PopulateKeywords([]native.MemFile{
		{Slug: "continuity.md", Project: "A", Name: "a", Keywords: []string{"kubernetes", "helm"}},
		{Slug: "continuity.md", Project: "B", Name: "b", Keywords: []string{"postgres", "sql"}},
	}); err != nil {
		t.Fatal(err)
	}
	ov, err := s.OverlapScores([]string{"kubernetes", "helm"})
	if err != nil {
		t.Fatal(err)
	}
	if got := ov[native.QKey("A", "continuity.md")]; got != 2 {
		t.Errorf("A overlap = %d, want 2", got)
	}
	if got := ov[native.QKey("B", "continuity.md")]; got != 0 {
		t.Errorf("B overlap = %d, want 0 (same slug, other project)", got)
	}
}
