package doctor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// newFixture builds a complete "healthy" claude+memory pair under tmp.
// Returns claudeDir, memoryDir. Individual tests degrade specific parts
// to exercise each failure mode.
func newFixture(t *testing.T) (string, string) {
	t.Helper()
	claude := t.TempDir()
	mem := filepath.Join(claude, "memory")
	for _, sub := range []string{
		"hooks", "bin", "memory/mistakes", "memory/strategies",
		"memory/feedback", "memory/knowledge",
	} {
		if err := os.MkdirAll(filepath.Join(claude, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A minimal settings.json that lists every hypomnema hook install.sh
	// registers, so checkSettings is happy out of the box.
	var hookLines []string
	for _, cmd := range requiredHookCommands {
		hookLines = append(hookLines, `"command": "~/.claude/hooks/`+cmd+`"`)
	}
	settings := `{"hooks":{"_stub":[` + strings.Join(hookLines, ",") + `]}}`
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	// memoryctl binary stub — doctor only checks existence / non-dir.
	if err := os.WriteFile(filepath.Join(claude, "bin", "memoryctl"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return claude, mem
}

func TestRun_CleanFixtureNoFails(t *testing.T) {
	claude, mem := newFixture(t)
	r := Run(claude, mem)
	_, _, fail := r.Counts()
	if fail != 0 {
		var buf bytes.Buffer
		r.Print(&buf)
		t.Errorf("expected 0 FAIL on clean fixture, got %d\n%s", fail, buf.String())
	}
	if code := r.ExitCode(); code != 0 {
		t.Errorf("expected ExitCode 0, got %d", code)
	}
}

func TestRun_MissingMemoryDirFails(t *testing.T) {
	claude := t.TempDir()
	// No memory dir created.
	r := Run(claude, filepath.Join(claude, "memory"))
	mustFindCheck(t, r, "memory_dir_exists", FAIL)
}

func TestRun_SettingsMissingHookCommandFails(t *testing.T) {
	claude, mem := newFixture(t)
	// Rewrite settings.json to omit memory-dedup.sh — reproduces the exact
	// pre-061c3e6 install.sh bug this whole subcommand exists to catch.
	var lines []string
	for _, cmd := range requiredHookCommands {
		if cmd == "memory-dedup.sh" {
			continue
		}
		lines = append(lines, `"command": "~/.claude/hooks/`+cmd+`"`)
	}
	broken := `{"hooks":{"_stub":[` + strings.Join(lines, ",") + `]}}`
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Run(claude, mem)
	c := mustFindCheck(t, r, "settings_hooks_registered", FAIL)
	if !strings.Contains(c.Detail, "memory-dedup.sh") {
		t.Errorf("expected detail to name the missing hook, got %q", c.Detail)
	}
}

func TestRun_BrokenSymlinkFails(t *testing.T) {
	claude, mem := newFixture(t)
	// Point a symlink at a nonexistent target — mirrors the left-over
	// memory-dedup.py we caught on the author's own install.
	if err := os.Symlink("/nonexistent/target.sh", filepath.Join(claude, "hooks", "stale-link.sh")); err != nil {
		t.Fatal(err)
	}
	r := Run(claude, mem)
	c := mustFindCheck(t, r, "no_broken_symlinks_hooks", FAIL)
	if !strings.Contains(c.Detail, "stale-link.sh") {
		t.Errorf("expected detail to name the broken symlink, got %q", c.Detail)
	}
}

func TestRun_WALErrorsInLast7Days(t *testing.T) {
	claude, mem := newFixture(t)
	// Seed WAL: one schema-error today, one format-unsupported yesterday,
	// one stale evidence-missing from 30 days ago (MUST be excluded by
	// the 7d window).
	now := time.Now()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	old := now.AddDate(0, 0, -30).Format("2006-01-02")
	walLines := []string{
		today + "|schema-error|broken-file|s1",
		yesterday + "|format-unsupported|future-file:2|s2",
		old + "|evidence-missing|hash123|s3",
		today + "|inject|some-slug|s4", // not an error event — must not count.
	}
	if err := os.WriteFile(filepath.Join(mem, ".wal"),
		[]byte(strings.Join(walLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Run(claude, mem)
	c := mustFindCheck(t, r, "wal_errors_last_7d", WARN)
	// Two events in window (schema-error + format-unsupported), not three.
	if !strings.Contains(c.Detail, "2 events") {
		t.Errorf("expected '2 events' in detail, got %q", c.Detail)
	}
	if strings.Contains(c.Detail, "evidence-missing") {
		t.Errorf("stale event should be excluded from 7d window; got %q", c.Detail)
	}
}

func TestRun_WALMissingIsOK(t *testing.T) {
	claude, mem := newFixture(t)
	// No .wal file — typical on a fresh install.
	r := Run(claude, mem)
	mustFindCheck(t, r, "wal_errors_last_7d", OK)
}

func TestRun_CorpusEmptyWarns(t *testing.T) {
	claude, mem := newFixture(t)
	r := Run(claude, mem)
	mustFindCheck(t, r, "corpus_counts", WARN)
}

func TestRun_CorpusWithFilesReportsCounts(t *testing.T) {
	claude, mem := newFixture(t)
	mk := func(sub, slug, status string) {
		path := filepath.Join(mem, sub, slug+".md")
		body := "---\ntype: " + sub + "\nstatus: " + status + "\n---\nBody.\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("mistakes", "m-one", "active")
	mk("mistakes", "m-two", "pinned")
	mk("strategies", "s-one", "stale")
	r := Run(claude, mem)
	c := mustFindCheck(t, r, "corpus_counts", OK)
	for _, fragment := range []string{"3 total", "mistakes:2", "strategies:1", "1 pinned", "1 stale"} {
		if !strings.Contains(c.Detail, fragment) {
			t.Errorf("expected %q in detail, got %q", fragment, c.Detail)
		}
	}
}

func TestCorpusQuality_TriggersEvidenceAmbientAllPass(t *testing.T) {
	claude, mem := newFixture(t)
	mk := func(sub, slug, body string) {
		path := filepath.Join(mem, sub, slug+".md")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Each of the three escape hatches must satisfy the check.
	mk("feedback", "has-triggers",
		"---\ntype: feedback\nstatus: active\ntriggers:\n  - \"foo\"\n---\nBody.\n")
	mk("feedback", "has-evidence",
		"---\ntype: feedback\nstatus: active\nevidence:\n  - \"bar\"\n---\nBody.\n")
	mk("feedback", "is-ambient",
		"---\ntype: feedback\nstatus: active\nprecision_class: ambient\n---\nBody.\n")
	mk("feedback", "is-superseded",
		"---\ntype: feedback\nstatus: superseded\n---\nBody.\n")
	mk("knowledge", "k-with-triggers",
		"---\ntype: knowledge\nstatus: active\ntriggers:\n  - \"baz\"\n---\nBody.\n")
	r := Run(claude, mem)
	c := mustFindCheck(t, r, "corpus_frontmatter_quality", OK)
	if !strings.Contains(c.Detail, "all files have valid frontmatter") {
		t.Errorf("expected clean detail, got %q", c.Detail)
	}
}

func TestCorpusQuality_BareFeedbackWithoutAnythingWarns(t *testing.T) {
	claude, mem := newFixture(t)
	body := "---\ntype: feedback\nstatus: active\n---\nBody only, no triggers, no evidence, not ambient.\n"
	if err := os.WriteFile(filepath.Join(mem, "feedback", "bare.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Run(claude, mem)
	c := mustFindCheck(t, r, "corpus_frontmatter_quality", WARN)
	if !strings.Contains(c.Detail, "1 feedback/knowledge without triggers:") {
		t.Errorf("expected bare-file warning, got %q", c.Detail)
	}
}

func TestDecisionsPressure_NoDirectoryIsOK(t *testing.T) {
	claude, mem := newFixture(t)
	r := Run(claude, mem)
	c := mustFindCheck(t, r, "decisions_review", OK)
	if !strings.Contains(c.Detail, "no personal decisions") {
		t.Errorf("detail should mention missing dir, got %q", c.Detail)
	}
}

func TestDecisionsPressure_CleanADRsAreOK(t *testing.T) {
	claude, mem := newFixture(t)
	if err := os.MkdirAll(filepath.Join(mem, "decisions"), 0o755); err != nil {
		t.Fatal(err)
	}
	// ADR with future calendar → all ok.
	body := "---\ntype: decision\nstatus: active\nreview-triggers:\n  - after: \"2099-01-01\"\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(mem, "decisions", "future.md"),
		[]byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Run(claude, mem)
	c := mustFindCheck(t, r, "decisions_review", OK)
	if !strings.Contains(c.Detail, "no pressure") {
		t.Errorf("detail should confirm no pressure, got %q", c.Detail)
	}
}

func TestDecisionsPressure_OverdueWarns(t *testing.T) {
	claude, mem := newFixture(t)
	if err := os.MkdirAll(filepath.Join(mem, "decisions"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\ntype: decision\nstatus: active\nreview-triggers:\n  - after: \"2020-01-01\"\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(mem, "decisions", "overdue-one.md"),
		[]byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Run(claude, mem)
	c := mustFindCheck(t, r, "decisions_review", WARN)
	if !strings.Contains(c.Detail, "overdue-one") {
		t.Errorf("detail should name the overdue slug, got %q", c.Detail)
	}
}

func TestOpenQuanta_AllClosedIsOK(t *testing.T) {
	claude, mem := newFixture(t)
	today := time.Now().Format("2006-01-02")
	// 3 sessions: each with an inject + a closing event.
	lines := []string{
		today + "|inject|slug-a|s1",
		today + "|trigger-useful|slug-a|s1",
		today + "|inject|slug-b|s2",
		today + "|trigger-silent|slug-b|s2",
		today + "|inject|slug-c|s3",
		today + "|clean-session|_global_|s3",
	}
	mustWriteWAL(t, mem, lines)
	r := Run(claude, mem)
	c := mustFindCheck(t, r, "open_quanta_last_30d", OK)
	if !strings.Contains(c.Detail, "0/3") {
		t.Errorf("expected 0/3 open, got %q", c.Detail)
	}
}

func TestOpenQuanta_MajorityOpenWarns(t *testing.T) {
	claude, mem := newFixture(t)
	today := time.Now().Format("2006-01-02")
	// 4 sessions: only one has a closing event → 75% open.
	lines := []string{
		today + "|inject|slug-a|s1",
		today + "|trigger-useful|slug-a|s1",
		// Three sessions with injects only.
		today + "|inject|slug-b|s2",
		today + "|inject|slug-c|s3",
		today + "|inject|slug-d|s4",
	}
	mustWriteWAL(t, mem, lines)
	r := Run(claude, mem)
	c := mustFindCheck(t, r, "open_quanta_last_30d", WARN)
	if !strings.Contains(c.Detail, "3/4") {
		t.Errorf("expected 3/4 open, got %q", c.Detail)
	}
	if !strings.Contains(c.Detail, "measurement-bias.md") {
		t.Errorf("WARN detail should point the operator at the doc, got %q", c.Detail)
	}
}

func TestOpenQuanta_ExcludesOldEntriesOutsideWindow(t *testing.T) {
	claude, mem := newFixture(t)
	old := time.Now().AddDate(0, 0, -60).Format("2006-01-02")
	lines := []string{
		// Old inject without closing — MUST be excluded from the 30d window.
		old + "|inject|slug-old|s-old",
	}
	mustWriteWAL(t, mem, lines)
	r := Run(claude, mem)
	// Empty-window path returns OK with a clear message; no WARN.
	c := mustFindCheck(t, r, "open_quanta_last_30d", OK)
	if !strings.Contains(c.Detail, "no inject-carrying sessions") {
		t.Errorf("expected empty-window message, got %q", c.Detail)
	}
}

// mustWriteWAL is a tiny fixture helper so test cases stay readable.
func mustWriteWAL(t *testing.T, mem string, lines []string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(mem, ".wal"),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRequiredHookCommands_MatchInstallScript guards against the
// tautological drift the doctor suite suffered pre-P1.4. Both the
// clean fixture and the missing-hook test iterate requiredHookCommands
// to build their input, so a hook added in install.sh without a
// matching entry in the constant left every existing assertion green.
// This test reads install.sh directly, greps the suffix of every
// register_hook invocation, and compares the set — a real drift
// between install.sh and the doctor's view of "what should be wired
// up" fails the test instead of surfacing only at runtime.
func TestRequiredHookCommands_MatchInstallScript(t *testing.T) {
	// Walk upward from the test file location to the repo root
	// (install.sh lives there). Tests run with CWD = package dir.
	installPath := ""
	for _, candidate := range []string{
		"../../install.sh",
		"../install.sh",
		"install.sh",
	} {
		if _, err := os.Stat(candidate); err == nil {
			installPath = candidate
			break
		}
	}
	if installPath == "" {
		t.Skip("install.sh not found from test CWD — skipping drift check")
	}

	data, err := os.ReadFile(installPath)
	if err != nil {
		t.Fatalf("read %s: %v", installPath, err)
	}
	// Match every register_hook line that references ~/.claude/hooks/<name>.sh.
	// register_hook lines follow the shape:
	//   register_hook <event> <matcher> "~/.claude/hooks/memory-X.sh" <pri> "<msg>"
	re := regexp.MustCompile(`~/\.claude/hooks/(memory-[a-z0-9-]+\.sh)`)
	matches := re.FindAllStringSubmatch(string(data), -1)

	fromInstall := map[string]struct{}{}
	for _, m := range matches {
		fromInstall[m[1]] = struct{}{}
	}

	fromDoctor := map[string]struct{}{}
	for _, cmd := range requiredHookCommands {
		fromDoctor[cmd] = struct{}{}
	}

	var missing, extra []string
	for k := range fromInstall {
		if _, ok := fromDoctor[k]; !ok {
			missing = append(missing, k)
		}
	}
	for k := range fromDoctor {
		if _, ok := fromInstall[k]; !ok {
			extra = append(extra, k)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		sort.Strings(missing)
		sort.Strings(extra)
		t.Errorf("requiredHookCommands drifted from install.sh:\n  in install.sh but NOT in requiredHookCommands: %v\n  in requiredHookCommands but NOT in install.sh: %v",
			missing, extra)
	}
}

func TestReport_PrintJSONRoundtrips(t *testing.T) {
	claude, mem := newFixture(t)
	r := Run(claude, mem)
	var buf bytes.Buffer
	r.PrintJSON(&buf)
	// Decode into a map — we only assert structural shape here, not field
	// names, so the test doesn't couple to JSON tag renames.
	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("--json output not valid JSON: %v\n%s", err, buf.String())
	}
	if _, ok := decoded["checks"]; !ok {
		t.Errorf("JSON missing 'checks' field: %s", buf.String())
	}
}

// mustFindCheck locates a check by name and asserts its Status. Fails the
// test (not the process) with a descriptive message if missing.
func mustFindCheck(t *testing.T, r Report, name string, want Status) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			if c.Status != want {
				t.Errorf("check %q: want status %s, got %s (detail=%q)",
					name, want, c.Status, c.Detail)
			}
			return c
		}
	}
	t.Fatalf("check %q not found in report; checks: %+v", name, r.Checks)
	return Check{}
}
