package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerate walks a representative fixture end-to-end: WAL with every
// event type the script counts, an ambient-classified feedback file, two
// mistakes of differing recurrence, and two strategies. Output is
// compared section by section so a regression in any single counter
// points at the specific feature.
func TestGenerate(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "mistakes"))
	mustMkdir(t, filepath.Join(dir, "strategies"))

	mustWrite(t, filepath.Join(dir, ".wal"), walFixture)
	mustWrite(t, filepath.Join(dir, "ambient-rule.md"), ambientFile)
	mustWrite(t, filepath.Join(dir, "mistakes/bar-mistake.md"), mistakeUniversal)
	mustWrite(t, filepath.Join(dir, "mistakes/foo-mistake.md"), mistakeDomain)
	mustWrite(t, filepath.Join(dir, "strategies/s1.md"), strategy4)
	mustWrite(t, filepath.Join(dir, "strategies/s2.md"), strategy1)

	t.Setenv("HYPOMNEMA_NOW", "2026-04-22 12:34")
	if err := Generate(dir); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	out, err := os.ReadFile(filepath.Join(dir, "self-profile.md"))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	got := string(out)

	// Header stamp pinned via HYPOMNEMA_NOW.
	mustContain(t, got, "generated: 2026-04-22 12:34")

	// Meta-signals: counts come straight from the fixture.
	// 3 session-metrics rows, 1 clean-session, 1 outcome-positive, 1 outcome-negative,
	// 1 strategy-used, 1 strategy-gap. Trigger buckets:
	//   ambient activations = 1 (only `trigger-useful|ambient-rule`)
	//   trigger-useful measurable = 1 (bar-mistake — ambient-rule excluded)
	//   trigger-silent measurable = 2 (foo-mistake|sess2 + noise-rule|sess7)
	mustContain(t, got, "| total sessions (logged) | 3 |")
	mustContain(t, got, "| clean sessions (0 errors) | 1 |")
	mustContain(t, got, "| outcome-positive (mistake not repeated) | 1 |")
	mustContain(t, got, "| outcome-negative (mistake repeated) | 1 |")
	mustContain(t, got, "| strategy-used (clean session + strategy injected) | 1 |")
	mustContain(t, got, "| strategy-gap (clean session, no strategy) | 1 |")
	mustContain(t, got, "| ambient activations (rules excluded from precision by design) | 1 |")
	mustContain(t, got, "| trigger-useful measurable (referenced explicitly) | 1 |")

	// silent-applied: trigger-silent on foo-mistake|sess2 + outcome-positive
	// on foo-mistake|sess2 share the key → silent_applied = 1.
	mustContain(t, got, "| silent-applied measurable (silent + outcome-positive) | 1 |")
	// silent-noise = trigger_silent_measurable(2) - silent_applied(1) = 1.
	mustContain(t, got, "| silent-noise (silent, no application signal — **tuning targets**) | 1 |")

	// Precision = (useful + applied) / (useful + silent) * 100
	//           = (1 + 1) / (1 + 2) * 100 = 67 (rounded from 66.67)
	mustContain(t, got, "| **measurable precision** (useful + applied) / (useful + silent) | **67%** |")

	// evidence-empty unique slugs: bar-mistake, baz-mistake = 2.
	mustContain(t, got, "| evidence-empty rules (unique, cannot match trigger by design) | 2 |")
	mustContain(t, got, "| outcome-new (fresh mistakes recorded — learning rate) | 1 |")

	// Weaknesses: bar-mistake has recurrence 5 (universal → red dot),
	// foo-mistake has recurrence 2.
	mustContain(t, got, "- `bar-mistake` [major, x5, universal] 🔴")
	mustContain(t, got, "- `foo-mistake` [minor, x2, domain]")

	// Strengths: s1 (success 4) beats s2 (success 1).
	mustContain(t, got, "- `s1` (success: 4)")
	mustContain(t, got, "- `s2` (success: 1)")

	// Calibration: testing had 1 session with 3 errors (rate 3.00),
	// backend had 2 sessions totaling 3 errors → 1.50, frontend 1 err / 1 session = 1.00.
	mustContain(t, got, "- `testing`: 3.00 err/session avg (n=1)")
	mustContain(t, got, "- `backend`: 1.50 err/session avg (n=2)")
	mustContain(t, got, "- `frontend`: 1.00 err/session avg (n=1)")

	// Weakness ordering: bar-mistake must come before foo-mistake (recurrence 5 > 2).
	if strings.Index(got, "bar-mistake") > strings.Index(got, "foo-mistake") {
		t.Errorf("weakness order: bar-mistake (rec 5) must precede foo-mistake (rec 2)")
	}
}

func TestGenerateNoWAL(t *testing.T) {
	// Mirrors bash `[ -f "$WAL_FILE" ] || exit 0` — no .wal means no-op,
	// no self-profile.md written.
	dir := t.TempDir()
	if err := Generate(dir); err != nil {
		t.Fatalf("Generate without WAL should be no-op, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "self-profile.md")); !os.IsNotExist(err) {
		t.Errorf("expected no self-profile.md written; err=%v", err)
	}
}

func TestGenerateEmptyCorpus(t *testing.T) {
	// WAL exists but is empty, no mistakes or strategies. All counters
	// should be zero, precision "n/a", sections show placeholder strings.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".wal"), "")

	if err := Generate(dir); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(dir, "self-profile.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(out)
	mustContain(t, got, "| total sessions (logged) | 0 |")
	mustContain(t, got, "| **measurable precision** (useful + applied) / (useful + silent) | **n/a%** |")
	mustContain(t, got, "_no data_") // strengths/weaknesses placeholder
	mustContain(t, got, "_not enough data for calibration_")
}

// --- fixtures ------------------------------------------------------------

const walFixture = `2026-04-01|session-metrics|backend,testing|error_count:3,tool_calls:20,duration:120s
2026-04-02|session-metrics|backend|error_count:0,tool_calls:10,duration:60s
2026-04-02|clean-session|unknown|sess1
2026-04-03|session-metrics|frontend|error_count:1,tool_calls:5,duration:30s
2026-04-04|outcome-positive|foo-mistake|sess2
2026-04-04|trigger-silent|foo-mistake|sess2
2026-04-04|outcome-negative|bar-mistake|sess3
2026-04-05|trigger-useful|bar-mistake|sess4
2026-04-06|strategy-used|unknown|sess5
2026-04-06|strategy-gap|unknown|sess6
2026-04-07|trigger-useful|ambient-rule|sess7
2026-04-07|trigger-silent|noise-rule|sess7
2026-04-08|evidence-empty|bar-mistake|sess8
2026-04-08|evidence-empty|baz-mistake|sess9
2026-04-09|outcome-new|new-mistake|sess10
`

const ambientFile = `---
type: feedback
status: active
precision_class: ambient
---
body
`

const mistakeUniversal = `---
type: mistake
recurrence: 5
scope: universal
severity: major
---
body
`

const mistakeDomain = `---
type: mistake
recurrence: 2
scope: domain
severity: minor
---
body
`

const strategy4 = `---
type: strategy
success_count: 4
---
body
`

const strategy1 = `---
type: strategy
success_count: 1
---
body
`

// --- helpers -------------------------------------------------------------

func mustContain(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("output missing expected substring:\n  want: %q\n  ---got---\n%s", want, got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
