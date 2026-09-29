package promote

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

func TestAnalyze(t *testing.T) {
	q := func(slug string) string { return native.QKey("p", slug) }
	walLines := []string{
		// always: 5 distinct useful sessions — claudemd signal. One session
		// (s3) was silent FIRST and useful later: useful must win.
		"2026-07-01|cite-useful|" + q("always") + "|s1",
		"2026-07-02|cite-useful|" + q("always") + "|s2",
		"2026-07-03|cite-silent|" + q("always") + "|s3",
		"2026-07-03|cite-useful|" + q("always") + "|s3",
		"2026-07-04|cite-useful|" + q("always") + "|s4",
		"2026-07-05|cite-useful|" + q("always") + "|s5",
		// done: completed ablation, 3/3 hits — retire signal.
		"2026-07-01|ablate-start|" + q("done") + ":3|cli",
		"2026-07-02|holdout-skip|" + q("done") + "|t1",
		"2026-07-02|holdout-hit|" + q("done") + "|t1",
		"2026-07-03|holdout-skip|" + q("done") + "|t2",
		"2026-07-03|holdout-hit|" + q("done") + "|t2",
		"2026-07-04|holdout-skip|" + q("done") + "|t3",
		"2026-07-04|holdout-hit|" + q("done") + "|t3",
		"2026-07-04|ablate-stop|" + q("done") + ":expired|t3",
		// dud: candidate, 5 silent, never useful — retire signal.
		"2026-07-01|cite-silent|" + q("dud") + "|u1",
		"2026-07-02|cite-silent|" + q("dud") + "|u2",
		"2026-07-03|cite-silent|" + q("dud") + "|u3",
		"2026-07-04|cite-silent|" + q("dud") + "|u4",
		"2026-07-05|cite-silent|" + q("dud") + "|u5",
	}
	walPath := filepath.Join(t.TempDir(), ".wal")
	if err := os.WriteFile(walPath, []byte(strings.Join(walLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := []native.MemFile{
		{Slug: "repeat.md", Project: "p", Type: "mistake", Recurrence: 3, Status: "active"},
		{Slug: "always.md", Project: "p", Type: "feedback", Status: "active"},
		{Slug: "done.md", Project: "p", Type: "strategy", Status: "active"},
		{Slug: "dud.md", Project: "p", Type: "mistake", Status: "candidate"},
		{Slug: "quiet.md", Project: "p", Type: "knowledge", Status: "active"}, // no signal
	}
	got := Analyze(files, walPath, Defaults())
	want := map[string]string{"repeat": "mechanical", "always": "claudemd", "done": "retire", "dud": "retire"}
	if len(got) != len(want) {
		t.Fatalf("got %d suggestions %+v, want %d", len(got), got, len(want))
	}
	for _, s := range got {
		if want[s.Slug] != s.Kind {
			t.Fatalf("%s: kind %q, want %q", s.Slug, s.Kind, want[s.Slug])
		}
		if s.Action == "" || s.Evidence == "" {
			t.Fatalf("%s: empty action/evidence: %+v", s.Slug, s)
		}
	}
	// Ordering: mechanical first, retire last.
	if got[0].Kind != "mechanical" || got[len(got)-1].Kind != "retire" {
		t.Fatalf("ordering wrong: %+v", got)
	}
}

// TestAnalyze_OneSuggestionPerSlug: a fact matching several signals prints
// once with the highest-consequence suggestion (mechanical wins).
func TestAnalyze_OneSuggestionPerSlug(t *testing.T) {
	q := native.QKey("p", "both")
	walLines := []string{
		"2026-07-01|cite-useful|" + q + "|s1",
		"2026-07-02|cite-useful|" + q + "|s2",
		"2026-07-03|cite-useful|" + q + "|s3",
		"2026-07-04|cite-useful|" + q + "|s4",
		"2026-07-05|cite-useful|" + q + "|s5",
	}
	walPath := filepath.Join(t.TempDir(), ".wal")
	if err := os.WriteFile(walPath, []byte(strings.Join(walLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := []native.MemFile{
		{Slug: "both.md", Project: "p", Type: "mistake", Recurrence: 4, Status: "active"},
	}
	got := Analyze(files, walPath, Defaults())
	if len(got) != 1 || got[0].Kind != "mechanical" {
		t.Fatalf("want single mechanical suggestion, got %+v", got)
	}
}

// TestAnalyze_LegacyTriggerEventsHaveNoEffect: trigger-useful used to drive
// the useful-streak signal (pre-v2.14); the fresh start reads only
// cite-useful, so a fact with 5 recent cite-useful sessions is suggested for
// CLAUDE.md while one with 5 trigger-useful sessions is not.
func TestAnalyze_LegacyTriggerEventsHaveNoEffect(t *testing.T) {
	q := func(slug string) string { return native.QKey("p", slug) }
	walLines := []string{
		"2026-07-01|cite-useful|" + q("live") + "|s1",
		"2026-07-02|cite-useful|" + q("live") + "|s2",
		"2026-07-03|cite-useful|" + q("live") + "|s3",
		"2026-07-04|cite-useful|" + q("live") + "|s4",
		"2026-07-05|cite-useful|" + q("live") + "|s5",
		"2026-07-01|trigger-useful|" + q("legacy") + "|t1",
		"2026-07-02|trigger-useful|" + q("legacy") + "|t2",
		"2026-07-03|trigger-useful|" + q("legacy") + "|t3",
		"2026-07-04|trigger-useful|" + q("legacy") + "|t4",
		"2026-07-05|trigger-useful|" + q("legacy") + "|t5",
	}
	walPath := filepath.Join(t.TempDir(), ".wal")
	if err := os.WriteFile(walPath, []byte(strings.Join(walLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := []native.MemFile{
		{Slug: "live.md", Project: "p", Type: "feedback", Status: "active"},
		{Slug: "legacy.md", Project: "p", Type: "feedback", Status: "active"},
	}
	got := Analyze(files, walPath, Defaults())
	want := map[string]string{"live": "claudemd"}
	if len(got) != len(want) {
		t.Fatalf("got %d suggestions %+v, want %d (legacy.md must not be suggested — trigger-useful is ignored)", len(got), got, len(want))
	}
	for _, s := range got {
		if want[s.Slug] != s.Kind {
			t.Fatalf("%s: kind %q, want %q", s.Slug, s.Kind, want[s.Slug])
		}
	}
}

// Five subagents of one orchestrating session are one observation: they
// must not by themselves reach the useful-streak or never-corroborated
// thresholds.
func TestAnalyze_SubagentKeysCountAsOneSession(t *testing.T) {
	q := func(slug string) string { return native.QKey("p", slug) }
	var lines []string
	for i := 1; i <= 5; i++ {
		lines = append(lines,
			fmt.Sprintf("2026-07-01|cite-useful|%s|s1:a%d", q("always"), i),
			fmt.Sprintf("2026-07-01|cite-silent|%s|s1:a%d", q("dud"), i))
	}
	walPath := filepath.Join(t.TempDir(), ".wal")
	if err := os.WriteFile(walPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := []native.MemFile{
		{Slug: "always.md", Project: "p", Type: "feedback", Status: "active"},
		{Slug: "dud.md", Project: "p", Type: "mistake", Status: "candidate"},
	}
	if got := Analyze(files, walPath, Defaults()); len(got) != 0 {
		t.Fatalf("five subagents of one session must count once; got %+v", got)
	}
}
