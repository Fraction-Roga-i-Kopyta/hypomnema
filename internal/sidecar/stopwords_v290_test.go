package sidecar

import (
	"path/filepath"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

func TestOverlap_StopwordsNotCounted(t *testing.T) { // P3a stopwords
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, ".sidecar.db"))
	defer s.Close()
	// A file whose body is mostly English/Russian function words plus one real
	// signal term. Keyword stuffing must not let the function words score.
	if err := s.PopulateKeywords([]native.MemFile{{
		Slug: "stuffed.md", Project: "p", Name: "stuffed",
		Body: "the and that with this from which when where would could sqlite что как это для когда",
	}}); err != nil {
		t.Fatal(err)
	}
	// Query is function-word heavy + the one real term.
	overlap, err := s.OverlapScores([]string{
		"the", "and", "that", "with", "this", "which", "when", "sqlite",
		"что", "как", "это", "для", "когда",
	})
	if err != nil {
		t.Fatal(err)
	}
	if overlap["stuffed.md"] != 1 {
		t.Errorf("only the real term 'sqlite' should count, got overlap=%d (function words leaked)", overlap["stuffed.md"])
	}
}
