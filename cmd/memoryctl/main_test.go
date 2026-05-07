package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// binPath holds the path to the built memoryctl binary, set once by
// TestMain so each test reuses the same executable.
var binPath string

// coverDir is the GOCOVERDIR every subprocess invocation writes its
// binary coverage data to. Shared across the whole TestMain so the
// per-test data accumulates instead of being thrown away (the previous
// `t.TempDir()` was auto-cleaned before aggregation, which is why
// `go test -cover ./cmd/memoryctl/...` reported 0.0% despite real
// functional coverage).
var coverDir string

// TestMain builds the memoryctl binary to a temp location and runs
// every test against it as a subprocess. Each subcommand exercised
// this way counts toward cmd/memoryctl coverage via -coverpkg.
//
// When MEMORYCTL_SUBPROCESS_COVER_OUT is set, after all tests finish
// we convert the accumulated binary coverage data into Go's textual
// cover format and write it to that path. Callers (Makefile, CI)
// can then merge this with the test-binary cover profile to report
// real coverage instead of the 0% the runtime alone would show.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "memoryctl-test-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	binPath = filepath.Join(dir, "memoryctl")
	// Use -coverpkg=./... so subprocess invocations accrue coverage.
	build := exec.Command("go", "build",
		"-cover", "-coverpkg=./...",
		"-o", binPath, ".")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}

	coverDir = filepath.Join(dir, "cover")
	if err := os.MkdirAll(coverDir, 0o755); err != nil {
		panic(err)
	}

	code := m.Run()

	if outPath := os.Getenv("MEMORYCTL_SUBPROCESS_COVER_OUT"); outPath != "" {
		conv := exec.Command("go", "tool", "covdata", "textfmt",
			"-i="+coverDir, "-o="+outPath)
		conv.Stdout = os.Stderr
		conv.Stderr = os.Stderr
		if err := conv.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "covdata textfmt failed: %v\n", err)
		}
	}

	os.Exit(code)
}

// run invokes the built binary with `args` and optional env overrides.
// Returns stdout, stderr, exit code. GOCOVERDIR points at the shared
// TestMain-level coverdir so subprocess coverage accumulates across
// every test instead of being deleted with each per-test t.TempDir.
func run(t *testing.T, env map[string]string, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	cmd.Env = append(os.Environ(), "GOCOVERDIR="+coverDir)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run: unexpected error %v\nstderr: %s", err, errBuf.String())
	}
	return outBuf.String(), errBuf.String(), exit
}

// --- Top-level dispatch ---

func TestMain_NoArgs_ExitsTwo(t *testing.T) {
	_, stderr, code := run(t, nil)
	if code != 2 {
		t.Errorf("no-args: expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "Usage") {
		t.Errorf("stderr should print usage, got %q", stderr)
	}
}

func TestMain_Help(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		stdout, _, code := run(t, nil, flag)
		if code != 0 {
			t.Errorf("%s: expected exit 0, got %d", flag, code)
		}
		if !strings.Contains(stdout, "memoryctl") {
			t.Errorf("%s: usage should mention memoryctl, got %q", flag, stdout)
		}
	}
}

func TestMain_UnknownCommand(t *testing.T) {
	_, stderr, code := run(t, nil, "nonexistent")
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Errorf("stderr should name the problem, got %q", stderr)
	}
}

// --- fts subcommand ---

func TestFts_NoSubcommand(t *testing.T) {
	_, _, code := run(t, nil, "fts")
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
}

func TestFts_UnknownSubcommand(t *testing.T) {
	_, stderr, code := run(t, nil, "fts", "bogus")
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "unknown fts subcommand") {
		t.Errorf("stderr should identify unknown fts subcommand, got %q", stderr)
	}
}

// TestFts_QueryInvalidLimit pins the v0.10.3 fix: non-integer or
// zero/negative limit must exit 2 with an actionable message rather
// than silently running with a degraded value.
func TestFts_QueryInvalidLimit(t *testing.T) {
	for _, bad := range []string{"abc", "0", "-5"} {
		_, stderr, code := run(t, nil, "fts", "query", "prompt", bad)
		if code != 2 {
			t.Errorf("limit=%q: expected exit 2, got %d", bad, code)
		}
		if !strings.Contains(stderr, "invalid limit") {
			t.Errorf("limit=%q: stderr should say invalid limit, got %q", bad, stderr)
		}
	}
}

func TestFts_QueryNoArgs(t *testing.T) {
	_, _, code := run(t, nil, "fts", "query")
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
}

// --- dedup subcommand ---

func TestDedup_NoSubcommand(t *testing.T) {
	_, _, code := run(t, nil, "dedup")
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
}

func TestDedup_UnknownSubcommand(t *testing.T) {
	_, stderr, code := run(t, nil, "dedup", "bogus")
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "unknown dedup subcommand") {
		t.Errorf("stderr should identify unknown dedup subcommand, got %q", stderr)
	}
}

// --- tfidf subcommand ---

func TestTfidf_UnknownSubcommand(t *testing.T) {
	_, stderr, code := run(t, nil, "tfidf", "bogus")
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "unknown tfidf subcommand") {
		t.Errorf("stderr should identify unknown tfidf subcommand, got %q", stderr)
	}
}

func TestTfidf_RebuildBuildsIndex(t *testing.T) {
	mem := t.TempDir()
	for _, sub := range []string{"mistakes", "feedback", "knowledge", "strategies", "notes"} {
		if err := os.MkdirAll(filepath.Join(mem, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for i, body := range []string{
		"alpha beta gamma delta epsilon",
		"alpha beta foxtrot",
		"unique tokens here onlyone",
	} {
		name := []string{"a.md", "b.md", "c.md"}[i]
		frontmatter := "---\ntype: mistake\n---\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(mem, "mistakes", name),
			[]byte(frontmatter), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, stderr, code := run(t, map[string]string{"CLAUDE_MEMORY_DIR": mem},
		"tfidf", "rebuild")
	if code != 0 {
		t.Fatalf("tfidf rebuild: exit %d, stderr=%q", code, stderr)
	}
	idxPath := filepath.Join(mem, ".tfidf-index")
	data, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatalf(".tfidf-index should exist: %v", err)
	}
	if !strings.Contains(string(data), "foxtrot") {
		t.Errorf("index should include a token unique to b.md ('foxtrot'), got:\n%s", data)
	}
}

// --- doctor subcommand ---

func newDoctorFixture(t *testing.T) string {
	t.Helper()
	claude := t.TempDir()
	for _, sub := range []string{
		"hooks", "bin", "memory/mistakes", "memory/strategies",
		"memory/feedback", "memory/knowledge",
	} {
		if err := os.MkdirAll(filepath.Join(claude, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// settings.json with all required hypomnema hook commands (v0.14: 8).
	hooks := []string{
		"memory-session-start.sh", "memory-stop.sh",
		"memory-user-prompt-submit.sh", "memory-dedup.sh",
		"memory-outcome.sh", "memory-error-detect.sh",
		"memory-precompact.sh", "memory-secrets-detect.sh",
	}
	lines := make([]string, 0, len(hooks))
	for _, h := range hooks {
		lines = append(lines, `"command": "~/.claude/hooks/`+h+`"`)
	}
	settings := `{"hooks":{"_stub":[` + strings.Join(lines, ",") + `]}}`
	if err := os.WriteFile(filepath.Join(claude, "settings.json"),
		[]byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "bin", "memoryctl"),
		[]byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return claude
}

func TestDoctor_CleanFixtureExitsZero(t *testing.T) {
	claude := newDoctorFixture(t)
	env := map[string]string{
		"CLAUDE_HOME":       claude,
		"CLAUDE_MEMORY_DIR": filepath.Join(claude, "memory"),
	}
	stdout, _, code := run(t, env, "doctor")
	if code != 0 {
		t.Errorf("clean fixture: expected exit 0, got %d\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "hypomnema doctor") {
		t.Errorf("missing header, got %q", stdout)
	}
	if !strings.Contains(stdout, "Summary:") {
		t.Errorf("missing summary line, got %q", stdout)
	}
}

func TestDoctor_JSONOutputParses(t *testing.T) {
	claude := newDoctorFixture(t)
	env := map[string]string{
		"CLAUDE_HOME":       claude,
		"CLAUDE_MEMORY_DIR": filepath.Join(claude, "memory"),
	}
	stdout, _, code := run(t, env, "doctor", "--json")
	if code != 0 {
		t.Errorf("--json: expected exit 0, got %d", code)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("--json output not valid JSON: %v\nstdout: %s", err, stdout)
	}
	if _, ok := decoded["checks"]; !ok {
		t.Errorf("JSON missing 'checks' field")
	}
}

func TestDoctor_MissingMemoryDirFails(t *testing.T) {
	claude := t.TempDir()
	env := map[string]string{
		"CLAUDE_HOME":       claude,
		"CLAUDE_MEMORY_DIR": filepath.Join(claude, "memory-does-not-exist"),
	}
	_, _, code := run(t, env, "doctor")
	if code != 1 {
		t.Errorf("missing memory_dir: expected exit 1, got %d", code)
	}
}

func TestDoctor_UnknownFlagFails(t *testing.T) {
	_, stderr, code := run(t, nil, "doctor", "--bogus-flag")
	if code != 2 {
		t.Errorf("unknown flag: expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "unknown flag") {
		t.Errorf("stderr should mention unknown flag, got %q", stderr)
	}
}

// --- decisions subcommand ---

func TestDecisions_UnknownSubcommand(t *testing.T) {
	_, stderr, code := run(t, nil, "decisions", "bogus")
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "unknown decisions subcommand") {
		t.Errorf("stderr should identify unknown decisions subcommand, got %q", stderr)
	}
}

// writeADR seeds a decisions/*.md with given frontmatter lines
// (review-triggers: included inline).
func writeADR(t *testing.T, dir, slug, frontmatter string) {
	t.Helper()
	body := "---\ntype: decision\nstatus: active\n" + frontmatter + "---\nBody.\n"
	p := filepath.Join(dir, slug+".md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDecisions_ReviewAllOK(t *testing.T) {
	adrDir := t.TempDir()
	mem := t.TempDir()
	// Two ADRs: one calendar in the future, one without triggers.
	writeADR(t, adrDir, "future-calendar", "review-triggers:\n  - after: \"2099-01-01\"\n")
	writeADR(t, adrDir, "no-triggers", "")

	env := map[string]string{
		"HYPOMNEMA_TODAY":   "2026-04-23",
		"CLAUDE_MEMORY_DIR": mem,
	}
	stdout, _, code := run(t, env, "decisions", "review", "--dir", adrDir)
	if code != 0 {
		t.Errorf("expected exit 0, got %d\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "[ok] future-calendar") {
		t.Errorf("stdout missing future-calendar ok line:\n%s", stdout)
	}
}

func TestDecisions_ReviewPressureExitsOne(t *testing.T) {
	adrDir := t.TempDir()
	mem := t.TempDir()
	// Self-profile with low precision — triggers our fixture ADR.
	profile := "## Metrics\n- measurable precision: 30% (3/10)\n"
	if err := os.WriteFile(filepath.Join(mem, "self-profile.md"),
		[]byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	writeADR(t, adrDir, "under-pressure",
		"review-triggers:\n  - metric: measurable_precision\n    operator: \"<\"\n    threshold: 0.40\n    source: self-profile\n")

	env := map[string]string{
		"HYPOMNEMA_TODAY":   "2026-04-23",
		"CLAUDE_MEMORY_DIR": mem,
	}
	stdout, _, code := run(t, env, "decisions", "review", "--dir", adrDir)
	if code != 1 {
		t.Errorf("expected exit 1 on pressure, got %d\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "[pressure] under-pressure") {
		t.Errorf("stdout missing pressure line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "0.30") || !strings.Contains(stdout, "0.40") {
		t.Errorf("pressure message should show value + threshold, got:\n%s", stdout)
	}
}

func TestDecisions_ReviewOverdueExitsOne(t *testing.T) {
	adrDir := t.TempDir()
	mem := t.TempDir()
	writeADR(t, adrDir, "overdue-cal",
		"review-triggers:\n  - after: \"2020-01-01\"\n")

	env := map[string]string{
		"HYPOMNEMA_TODAY":   "2026-04-23",
		"CLAUDE_MEMORY_DIR": mem,
	}
	stdout, _, code := run(t, env, "decisions", "review", "--dir", adrDir)
	if code != 1 {
		t.Errorf("expected exit 1 on overdue, got %d", code)
	}
	if !strings.Contains(stdout, "[overdue] overdue-cal") {
		t.Errorf("stdout missing overdue line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "passed") {
		t.Errorf("overdue message should say 'passed', got:\n%s", stdout)
	}
}

func TestDecisions_ReviewJSONOutputValid(t *testing.T) {
	adrDir := t.TempDir()
	mem := t.TempDir()
	writeADR(t, adrDir, "calendar-future",
		"review-triggers:\n  - after: \"2099-01-01\"\n")

	env := map[string]string{
		"HYPOMNEMA_TODAY":   "2026-04-23",
		"CLAUDE_MEMORY_DIR": mem,
	}
	stdout, _, code := run(t, env, "decisions", "review", "--dir", adrDir, "--json")
	if code != 0 {
		t.Errorf("expected exit 0, got %d", code)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("--json output not valid JSON: %v\n%s", err, stdout)
	}
	if _, ok := decoded["results"]; !ok {
		t.Errorf("JSON missing 'results' key")
	}
}

func TestDecisions_ReviewUnknownFlagFails(t *testing.T) {
	_, stderr, code := run(t, nil, "decisions", "review", "--bogus")
	if code != 2 {
		t.Errorf("unknown flag: expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "unknown flag") {
		t.Errorf("stderr should mention unknown flag, got %q", stderr)
	}
}

func TestDecisions_ReviewMissingDirFails(t *testing.T) {
	_, _, code := run(t, nil, "decisions", "review",
		"--dir", "/nonexistent/decisions-path")
	if code != 2 {
		t.Errorf("missing dir: expected exit 2, got %d", code)
	}
}

// --- evidence subcommand ---

func TestEvidence_UnknownSubcommand(t *testing.T) {
	_, stderr, code := run(t, nil, "evidence", "bogus")
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "unknown evidence subcommand") {
		t.Errorf("stderr should name the problem, got %q", stderr)
	}
}

func TestEvidence_LearnWithoutTargetFails(t *testing.T) {
	_, stderr, code := run(t, nil, "evidence", "learn")
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "--target") {
		t.Errorf("stderr should flag missing --target, got %q", stderr)
	}
}

func TestEvidence_LearnInvalidSessionsFlag(t *testing.T) {
	_, stderr, code := run(t, nil, "evidence", "learn",
		"--target", "some-slug", "--sessions", "abc")
	if code != 2 {
		t.Errorf("expected exit 2 on non-integer --sessions, got %d", code)
	}
	if !strings.Contains(stderr, "--sessions") {
		t.Errorf("stderr should name the flag, got %q", stderr)
	}
}

func TestEvidence_LearnUnknownFlag(t *testing.T) {
	_, stderr, code := run(t, nil, "evidence", "learn", "--bogus")
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "unknown flag") {
		t.Errorf("stderr should mention unknown flag, got %q", stderr)
	}
}

func TestEvidence_LearnMissingTargetFile(t *testing.T) {
	mem := t.TempDir()
	// Seed the dir structure but not the file.
	for _, sub := range []string{"mistakes", "feedback", "knowledge", "strategies"} {
		_ = os.MkdirAll(filepath.Join(mem, sub), 0o755)
	}
	env := map[string]string{"CLAUDE_MEMORY_DIR": mem}
	_, stderr, code := run(t, env, "evidence", "learn", "--target", "nonexistent-slug")
	if code != 2 {
		t.Errorf("missing target file: expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "not found") {
		t.Errorf("stderr should say 'not found', got %q", stderr)
	}
}

// End-to-end dry-run smoke: target file exists, transcripts absent,
// should exit 1 (no transcripts) — exercises the happy-path
// dispatch without requiring a fixture JSONL tree.
func TestEvidence_LearnNoTranscriptsExitsOne(t *testing.T) {
	mem := t.TempDir()
	fakeHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fakeHome, ".claude", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mem, "mistakes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mem, "mistakes", "test-rule.md"),
		[]byte("---\ntype: mistake\nstatus: active\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"CLAUDE_MEMORY_DIR": mem,
		"HOME":              fakeHome,
	}
	_, stderr, code := run(t, env, "evidence", "learn", "--target", "test-rule", "--dry-run")
	if code != 1 {
		t.Errorf("no-transcripts: expected exit 1, got %d\nstderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "no session transcripts") {
		t.Errorf("stderr should explain, got %q", stderr)
	}
}
