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

// TestSplitSessionMetrics covers the format-v1/v2 demultiplexer
// directly, since both real-WAL parsing paths and the calibration
// pipeline depend on it agreeing with the bash awk that mirrors the
// same logic. Each case asserts: given (col3, col4) on the wire, what
// (domains_csv, metrics_blob) does the reader yield.
func TestSplitSessionMetrics(t *testing.T) {
	cases := []struct {
		name        string
		col3, col4  string
		wantDomains string
		wantMetrics string
	}{
		{
			name:        "v1-single-domain",
			col3:        "backend",
			col4:        "error_count:0,tool_calls:5,duration:60s",
			wantDomains: "backend",
			wantMetrics: "error_count:0,tool_calls:5,duration:60s",
		},
		{
			name:        "v1-multi-domain",
			col3:        "backend,testing",
			col4:        "error_count:3,tool_calls:20,duration:120s",
			wantDomains: "backend,testing",
			wantMetrics: "error_count:3,tool_calls:20,duration:120s",
		},
		{
			name:        "v1-unknown",
			col3:        "unknown",
			col4:        "error_count:0,tool_calls:10,duration:30s",
			wantDomains: "unknown",
			wantMetrics: "error_count:0,tool_calls:10,duration:30s",
		},
		{
			name:        "v2-single-domain",
			col3:        "domains:backend,error_count:0,tool_calls:5,duration:60s",
			col4:        "sess-uuid",
			wantDomains: "backend",
			wantMetrics: "error_count:0,tool_calls:5,duration:60s",
		},
		{
			name:        "v2-multi-domain",
			col3:        "domains:backend,testing,error_count:3,tool_calls:20,duration:120s",
			col4:        "sess-uuid",
			wantDomains: "backend,testing",
			wantMetrics: "error_count:3,tool_calls:20,duration:120s",
		},
		{
			name:        "v2-unknown",
			col3:        "domains:unknown,error_count:0,tool_calls:11,duration:247s",
			col4:        "sess-uuid",
			wantDomains: "unknown",
			wantMetrics: "error_count:0,tool_calls:11,duration:247s",
		},
	}
	for _, tc := range cases {
		gotD, gotM := splitSessionMetrics(tc.col3, tc.col4)
		if gotD != tc.wantDomains || gotM != tc.wantMetrics {
			t.Errorf("%s:\n  col3=%q col4=%q\n  got  (%q, %q)\n  want (%q, %q)",
				tc.name, tc.col3, tc.col4, gotD, gotM, tc.wantDomains, tc.wantMetrics)
		}
	}
}

// TestGenerateMixedV1V2WAL verifies that a single WAL containing both
// format-v1 and format-v2 session-metrics rows aggregates correctly.
// This is the realistic upgrade path: existing v1 entries stay on
// disk, new v2 entries land alongside them, every reader must
// reconcile both shapes in one pass.
func TestGenerateMixedV1V2WAL(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "mistakes"), 0o755); err != nil {
		t.Fatal(err)
	}
	mixed := `2026-04-01|session-metrics|backend|error_count:5,tool_calls:10,duration:60s
2026-04-02|session-metrics|domains:backend,error_count:3,tool_calls:20,duration:120s|sess-v2-a
2026-04-03|session-metrics|frontend,testing|error_count:0,tool_calls:8,duration:45s
2026-04-04|session-metrics|domains:frontend,testing,error_count:2,tool_calls:15,duration:90s|sess-v2-b
`
	mustWrite(t, filepath.Join(dir, ".wal"), mixed)
	if err := Generate(dir); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "self-profile.md"))
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)

	// 4 session-metrics rows (2 v1 + 2 v2).
	mustContain(t, out, "| total sessions (logged) | 4 |")
	// Calibration domains: backend ×2 (1 v1 + 1 v2), frontend ×2, testing ×2.
	// Errors: backend has 5+3=8, frontend has 0+2=2, testing has 0+2=2.
	// Per-session avg: backend 4.00 (8/2), frontend 1.00 (2/2), testing 1.00 (2/2).
	mustContain(t, out, "`backend`: 4.00 err/session avg (n=2)")
	mustContain(t, out, "`frontend`: 1.00 err/session avg (n=2)")
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

// TestGenerate_NoOrphanTmpOnSuccess locks the post-condition for the
// atomic write path (external review 2026-05-08, finding E2): after
// Generate exits cleanly, the only file in memoryDir matching the
// `.self-profile.*.tmp` glob must be zero (i.e. the temp file is
// either renamed into self-profile.md or cleaned up by the deferred
// remove). Without this guard, a silently-orphaned tmp file would
// accumulate across runs and contaminate `find -size -512k`-based
// scans.
func TestGenerate_NoOrphanTmpOnSuccess(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".wal"), "")
	if err := Generate(dir); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".self-profile.*.tmp"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("expected zero orphan tmp files, found: %v", matches)
	}
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
