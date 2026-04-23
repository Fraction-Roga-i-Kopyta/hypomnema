package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// binPath holds the path to the built memoryctl binary, set once by
// TestMain so each test reuses the same executable.
var binPath string

// TestMain builds the memoryctl binary to a temp location and runs
// every test against it as a subprocess. Each subcommand exercised
// this way counts toward cmd/memoryctl coverage via -coverpkg.
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
	os.Exit(m.Run())
}

// run invokes the built binary with `args` and optional env overrides.
// Returns stdout, stderr, exit code. The t.TempDir() for coverage
// output keeps parallel tests from stomping on each other.
func run(t *testing.T, env map[string]string, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	cmd.Env = append(os.Environ(), "GOCOVERDIR="+t.TempDir())
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
	// settings.json with all 7 required hypomnema hook commands.
	hooks := []string{
		"memory-session-start.sh", "memory-stop.sh",
		"memory-user-prompt-submit.sh", "memory-dedup.sh",
		"memory-outcome.sh", "memory-error-detect.sh",
		"memory-precompact.sh",
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
