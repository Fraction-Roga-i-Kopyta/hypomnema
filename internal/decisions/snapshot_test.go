package decisions

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildSnapshot_SelfProfileMetricsParsed(t *testing.T) {
	mem := t.TempDir()
	// Mirror the actual self-profile generator output verbatim
	// (internal/profile/profile.go and bin/memory-self-profile.sh).
	// Markdown table format — earlier inline form was a fixture-encodes-
	// the-bug case that masked the real regex never matched production.
	profile := `# Self-profile

## Meta-signals
| signal | count |
|---|---|
| total sessions (logged) | 10 |
| **measurable precision** (useful + applied) / (useful + silent) | **47%** |
| silent-applied measurable (silent + outcome-positive) | 4 |
| **ambient_fraction** (ambient activations / all reactive fires) | **22%** |
| **corpus_fraction_with_active_bayesian** (slugs with ≥min outcomes / total) | **18%** (4/22) |

## Weaknesses
- top recurrence: 8 (wrong-root-cause-diagnosis)
`
	if err := os.WriteFile(filepath.Join(mem, "self-profile.md"),
		[]byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := BuildSnapshot(mem, mustTime("2026-04-23"))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Metrics["measurable_precision"] != 0.47 {
		t.Errorf("measurable_precision: got %v, want 0.47", snap.Metrics["measurable_precision"])
	}
	if snap.Metrics["ambient_fraction"] != 0.22 {
		t.Errorf("ambient_fraction: got %v, want 0.22", snap.Metrics["ambient_fraction"])
	}
	if snap.Metrics["corpus_fraction_with_active_bayesian"] != 0.18 {
		t.Errorf("corpus_fraction_with_active_bayesian: got %v, want 0.18", snap.Metrics["corpus_fraction_with_active_bayesian"])
	}
	if snap.Metrics["recurrence_top_mistake"] != 8 {
		t.Errorf("recurrence_top_mistake: got %v, want 8", snap.Metrics["recurrence_top_mistake"])
	}
}

func TestBuildSnapshot_MissingSelfProfileIsOK(t *testing.T) {
	mem := t.TempDir()
	snap, err := BuildSnapshot(mem, mustTime("2026-04-23"))
	if err != nil {
		t.Fatal(err)
	}
	// Neither metric should be present.
	if _, ok := snap.Metrics["measurable_precision"]; ok {
		t.Error("measurable_precision must be absent when self-profile missing")
	}
	if _, ok := snap.Metrics["shadow_miss_ratio"]; ok {
		t.Error("shadow_miss_ratio must be absent when WAL missing")
	}
	if snap.Today != "2026-04-23" {
		t.Errorf("Today: got %q, want 2026-04-23", snap.Today)
	}
}

func TestBuildSnapshot_ShadowMissRatioComputed(t *testing.T) {
	mem := t.TempDir()
	now := mustTime("2026-04-23")
	// 6 shadow-miss + 2 trigger-match inside window ⇒ ratio 0.75.
	// Plus 10 shadow-miss OUTSIDE the 14-day window (must be excluded).
	recent := "2026-04-20"
	old := "2026-04-01"
	var wal string
	for i := 0; i < 6; i++ {
		wal += recent + "|shadow-miss|slug|sess\n"
	}
	for i := 0; i < 2; i++ {
		wal += recent + "|trigger-match|slug|sess\n"
	}
	for i := 0; i < 10; i++ {
		wal += old + "|shadow-miss|slug|sess\n"
	}
	if err := os.WriteFile(filepath.Join(mem, ".wal"), []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := BuildSnapshot(mem, now)
	if err != nil {
		t.Fatal(err)
	}
	got := snap.Metrics["shadow_miss_ratio"]
	want := 0.75
	if got != want {
		t.Errorf("shadow_miss_ratio: got %v, want %v", got, want)
	}
}

func TestBuildSnapshot_ZeroPromptsLeavesRatioAbsent(t *testing.T) {
	mem := t.TempDir()
	// WAL with no trigger-match / shadow-miss events at all — no
	// denominator, no metric.
	wal := "2026-04-20|inject|slug|sess\n"
	if err := os.WriteFile(filepath.Join(mem, ".wal"), []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, _ := BuildSnapshot(mem, mustTime("2026-04-23"))
	if _, ok := snap.Metrics["shadow_miss_ratio"]; ok {
		t.Error("shadow_miss_ratio must be absent when denominator is 0")
	}
}

func mustTime(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}
