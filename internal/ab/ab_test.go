package ab

import (
	"os"
	"path/filepath"
	"testing"
)

func writeWAL(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".wal")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseWALAndEvalSessions(t *testing.T) {
	p := writeWAL(t, ""+
		"2026-04-01|inject|a.md|s1\n"+
		"# comment skipped\n"+
		"2026-04-02|cite-useful|a.md|s1\n"+
		"2026-04-02|cite-useful|b.md|s1\n"+
		"2026-04-05|inject|a.md|s2\n"+
		"2026-04-05|cite-useful|a.md|s2\n")
	events, err := ParseWAL(p)
	if err != nil {
		t.Fatalf("ParseWAL: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("expected 5 events (comment skipped), got %d", len(events))
	}
	sessions := EvalSessions(events)
	if len(sessions) != 2 {
		t.Fatalf("expected 2 eval sessions, got %d", len(sessions))
	}
	if sessions[0].Date != "2026-04-02" {
		t.Errorf("s1 date = %q, want 2026-04-02 (earliest cite-useful)", sessions[0].Date)
	}
	if len(sessions[0].Useful) != 2 || !sessions[0].Useful["a.md"] || !sessions[0].Useful["b.md"] {
		t.Errorf("s1 useful set wrong: %v", sessions[0].Useful)
	}
}

// TestEvalSessions_LegacyTriggerUsefulIgnored: trigger-useful used to be the
// ground-truth signal (pre-v2.14); the fresh start reads only cite-useful,
// so a WAL carrying nothing but trigger-useful rows yields zero eval
// sessions.
func TestEvalSessions_LegacyTriggerUsefulIgnored(t *testing.T) {
	events, _ := ParseWAL(writeWAL(t, ""+
		"2026-04-01|inject|a.md|s1\n"+
		"2026-04-02|trigger-useful|a.md|s1\n"))
	sessions := EvalSessions(events)
	if len(sessions) != 0 {
		t.Errorf("sessions = %+v, want 0 (legacy trigger-useful must not count)", sessions)
	}
}

func TestSignalsBefore_TemporalHoldout(t *testing.T) {
	events, _ := ParseWAL(writeWAL(t, ""+
		"2026-04-01|inject|a.md|s1\n"+
		"2026-04-02|inject-agg|a.md|10\n"+
		"2026-04-02|cite-useful|a.md|s1\n"+
		"2026-04-10|inject|a.md|s2\n"+
		"2026-04-10|cite-silent|a.md|s2\n"))
	sig := SignalsBefore(events, "2026-04-10")
	a := sig["a.md"]
	if a.RefCount != 11 {
		t.Errorf("RefCount = %d, want 11 (1 inject + agg 10; the 2026-04-10 inject excluded)", a.RefCount)
	}
	if a.Pos != 1 || a.Neg != 0 {
		t.Errorf("Pos/Neg = %d/%d, want 1/0 (the 2026-04-10 cite-silent excluded by the holdout)", a.Pos, a.Neg)
	}
	if a.LastInject != "2026-04-02" {
		t.Errorf("LastInject = %q, want 2026-04-02 (latest inject before cutoff)", a.LastInject)
	}
}

// TestSignalsBefore_UsefulWinsPerSession is the exact fixture from the
// design brief: one verdict per (slug, session), useful wins over silent
// even when silent arrives first within the same session.
func TestSignalsBefore_UsefulWinsPerSession(t *testing.T) {
	events, _ := ParseWAL(writeWAL(t, ""+
		"2026-04-01|cite-useful|a.md|s1\n"+
		"2026-04-02|cite-silent|a.md|s2\n"+
		"2026-04-03|cite-silent|a.md|s3\n"+
		"2026-04-04|cite-useful|a.md|s3\n"))
	sig := SignalsBefore(events, "2026-04-10")
	a := sig["a.md"]
	if a.Pos != 2 || a.Neg != 1 {
		t.Errorf("Pos/Neg = %d/%d, want 2/1 (s1 useful, s2 silent, s3 silent-then-useful — useful wins)", a.Pos, a.Neg)
	}
}

// TestSignalsBefore_LegacyOutcomeEventsIgnored: outcome-positive/outcome-
// negative used to drive Pos/Neg directly (pre-v2.14); the fresh start
// ignores them — only cite-useful/cite-silent count now.
func TestSignalsBefore_LegacyOutcomeEventsIgnored(t *testing.T) {
	events, _ := ParseWAL(writeWAL(t, ""+
		"2026-04-01|outcome-positive|a.md|s1\n"+
		"2026-04-02|outcome-positive|a.md|s2\n"+
		"2026-04-03|outcome-negative|a.md|s3\n"))
	sig := SignalsBefore(events, "2026-04-10")
	a := sig["a.md"]
	if a.Pos != 0 || a.Neg != 0 {
		t.Errorf("Pos/Neg = %d/%d, want 0/0 (legacy outcome-* must not count)", a.Pos, a.Neg)
	}
}

func TestCandidatePool(t *testing.T) {
	events, _ := ParseWAL(writeWAL(t, ""+
		"2026-04-01|inject|a.md|s1\n"+
		"2026-04-01|inject|b.md|s1\n"+
		"2026-04-02|cite-useful|a.md|s1\n"))
	pool := CandidatePool(events)
	if len(pool) != 2 {
		t.Fatalf("pool = %v, want 2 distinct slugs", pool)
	}
	if pool[0] != "a.md" || pool[1] != "b.md" {
		t.Errorf("pool not sorted: %v", pool)
	}
}
