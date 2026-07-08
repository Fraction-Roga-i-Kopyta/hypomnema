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

func TestReproject_PerProjectEffectivenessNoMerge(t *testing.T) { // E5-deep
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, ".sidecar.db"))
	defer s.Close()
	q := native.QKey
	var wal string
	for i := 0; i < 3; i++ {
		sid := "s" + string(rune('0'+i))
		wal += "2026-07-01|trigger-useful|" + q("projA", "notes.md") + "|" + sid + "\n"
		wal += "2026-07-01|trigger-silent|" + q("projB", "notes.md") + "|" + sid + "\n"
	}
	walPath := filepath.Join(dir, ".wal")
	if err := os.WriteFile(walPath, []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	// Both projects reproject-visible (nil scope derives from files' projects).
	Reproject(s, []native.MemFile{
		{Slug: "notes.md", Project: "projA", Type: "note", Name: "notes"},
		{Slug: "notes.md", Project: "projB", Type: "note", Name: "notes"},
	}, walPath, nil)

	recs, _ := s.All()
	var effA, effB float64
	found := 0
	for _, r := range recs {
		if r.Slug == "notes.md" && r.Project == "projA" {
			effA = r.Effectiveness
			found++
		}
		if r.Slug == "notes.md" && r.Project == "projB" {
			effB = r.Effectiveness
			found++
		}
	}
	if found != 2 {
		t.Fatalf("expected TWO distinct notes.md rows (projA, projB), got %d (%d total rows)", found, len(recs))
	}
	if !(effA > 0.5 && effB < 0.5) {
		t.Errorf("per-project eff should not merge: projA(useful)=%.3f want>0.5, projB(silent)=%.3f want<0.5", effA, effB)
	}
}

func TestReproject_LegacyBareSlugGrandfathered(t *testing.T) { // E5-deep M2
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, ".sidecar.db"))
	defer s.Close()
	// A legacy (pre-v2.10) bare-slug useful event, no project attribution.
	wal := "2026-06-01|trigger-useful|solo.md|sX\n"
	walPath := filepath.Join(dir, ".wal")
	os.WriteFile(walPath, []byte(wal), 0o644)
	Reproject(s, []native.MemFile{{Slug: "solo.md", Project: "projA", Type: "note", Name: "solo"}}, walPath, []string{"projA"})
	r, ok, _ := s.Get("solo.md")
	if !ok {
		t.Fatal("row missing")
	}
	if r.Effectiveness <= 0.5 {
		t.Errorf("legacy useful event should be grandfathered onto the owner: eff=%.3f want>0.5", r.Effectiveness)
	}
}
