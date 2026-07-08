package sidecar

import (
	"os"
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

func TestReproject_AmbientNotPenalizedBySilence(t *testing.T) { // P3a ambient-exclusion
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, ".sidecar.db"))
	defer s.Close()
	// Five sessions, each marks BOTH facts trigger-silent (injected, not cited).
	var wal string
	for i := 0; i < 5; i++ {
		sid := "s" + string(rune('0'+i))
		wal += "2026-07-01|trigger-silent|amb.md|" + sid + "\n"
		wal += "2026-07-01|trigger-silent|reg.md|" + sid + "\n"
	}
	walPath := filepath.Join(dir, ".wal")
	if err := os.WriteFile(walPath, []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	Reproject(s, []native.MemFile{
		{Slug: "amb.md", Project: "p", Type: "feedback", Name: "amb", PrecisionClass: "ambient"},
		{Slug: "reg.md", Project: "p", Type: "feedback", Name: "reg"},
	}, walPath, []string{"p"})

	amb, _, _ := s.Get("amb.md")
	reg, _, _ := s.Get("reg.md")
	// Ambient rule: silence is not negative evidence → stays at/above the prior.
	if amb.Effectiveness < 0.5 {
		t.Errorf("ambient fact penalized by silence: eff=%.3f, want >= 0.5", amb.Effectiveness)
	}
	// Regular fact: 5 silent sessions drag it well below the prior.
	if reg.Effectiveness >= 0.5 {
		t.Errorf("regular fact should drop below prior after 5 silent sessions: eff=%.3f", reg.Effectiveness)
	}
}
