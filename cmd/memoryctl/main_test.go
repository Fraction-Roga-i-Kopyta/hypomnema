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

// runStdin is like run() but feeds `stdin` to the process.
func runStdin(t *testing.T, env map[string]string, stdin string, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Stdin = strings.NewReader(stdin)
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
		t.Fatalf("runStdin: %v\nstderr: %s", err, errBuf.String())
	}
	return outBuf.String(), errBuf.String(), exit
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
	// settings.json with all required hypomnema v2 hook shims (v2: 4).
	hooks := []string{
		"session-start.sh", "user-prompt-submit.sh",
		"pre-tool-write.sh", "session-stop.sh",
	}
	lines := make([]string, 0, len(hooks))
	for _, h := range hooks {
		lines = append(lines, `"command": "~/.claude/hooks/v2/`+h+`"`)
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

// --- wal validate -------------------------------------------------------

func TestWalValidate_CleanWALExitsZero(t *testing.T) {
	mem := t.TempDir()
	if err := os.WriteFile(filepath.Join(mem, ".wal"),
		[]byte("2026-04-01|inject|foo|sess1\n2026-04-02|trigger-useful|foo|sess1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := run(t, map[string]string{"CLAUDE_MEMORY_DIR": mem}, "wal", "validate")
	if code != 0 {
		t.Errorf("clean WAL: expected exit 0, got %d", code)
	}
	if !strings.Contains(stdout, "passing:  2") || !strings.Contains(stdout, "failures: 0") {
		t.Errorf("clean WAL stdout mismatch: %s", stdout)
	}
}

func TestWalValidate_BrokenWALExitsOne(t *testing.T) {
	mem := t.TempDir()
	// Mix one good row with one broken (5 columns) and one with empty session.
	content := "2026-04-01|inject|foo|sess1\n2026-04-02|broken|extra|cols|here\n2026-04-03|outcome-positive|foo|\n"
	if err := os.WriteFile(filepath.Join(mem, ".wal"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := run(t, map[string]string{"CLAUDE_MEMORY_DIR": mem}, "wal", "validate")
	if code != 1 {
		t.Errorf("broken WAL: expected exit 1, got %d", code)
	}
	if !strings.Contains(stdout, "failures: 2") {
		t.Errorf("broken WAL stdout should report 2 failures, got: %s", stdout)
	}
	if !strings.Contains(stdout, "column count = 5") {
		t.Errorf("broken WAL should mention column count failure: %s", stdout)
	}
	if !strings.Contains(stdout, "session is empty") {
		t.Errorf("broken WAL should mention empty session failure: %s", stdout)
	}
}

func TestWalValidate_MissingWALExitsTwo(t *testing.T) {
	mem := t.TempDir() // empty, no .wal
	_, stderr, code := run(t, map[string]string{"CLAUDE_MEMORY_DIR": mem}, "wal", "validate")
	if code != 2 {
		t.Errorf("missing WAL: expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "does not exist") {
		t.Errorf("missing WAL stderr should say 'does not exist': %s", stderr)
	}
}

func TestWalValidate_UnknownSubcommand(t *testing.T) {
	_, stderr, code := run(t, nil, "wal", "garbage")
	if code != 2 {
		t.Errorf("unknown subcommand: expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "unknown wal subcommand") {
		t.Errorf("stderr mismatch: %s", stderr)
	}
}

// --- audit invariants ---

// makeAuditFixtureRepo creates a synthetic hypomnema-shaped repo
// for the audit tests. Returns the repo root path. Tests can
// override individual files (e.g. write a hook with `set -e` to
// trigger the I5 checker).
func makeAuditFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"internal/foo", "hooks"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestAudit_NoSubcommand(t *testing.T) {
	_, stderr, code := run(t, nil, "audit")
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "missing subcommand") {
		t.Errorf("stderr mismatch: %s", stderr)
	}
}

func TestAudit_UnknownSubcommand(t *testing.T) {
	_, stderr, code := run(t, nil, "audit", "garbage")
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "unknown audit subcommand") {
		t.Errorf("stderr mismatch: %s", stderr)
	}
}

func TestAuditInvariants_CleanRepoExitsZero(t *testing.T) {
	dir := makeAuditFixtureRepo(t)
	// Clean fixture: no os.WriteFile, no `set -e` hooks.
	stdout, _, code := run(t, nil, "audit", "invariants", "--repo", dir)
	if code != 0 {
		t.Errorf("clean repo: expected exit 0, got %d\nstdout=%s", code, stdout)
	}
	if !strings.Contains(stdout, "violations: 0") {
		t.Errorf("clean repo stdout should report 0 violations: %s", stdout)
	}
}

func TestAuditInvariants_DirtyRepoExitsOne(t *testing.T) {
	dir := makeAuditFixtureRepo(t)
	// Plant one I3 violation and one I5 violation.
	if err := os.WriteFile(filepath.Join(dir, "internal/foo/foo.go"),
		[]byte(`package foo
import "os"
func Bad() error { return os.WriteFile("x", []byte{}, 0o644) }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hooks/bad.sh"),
		[]byte("#!/bin/bash\nset -e\necho ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := run(t, nil, "audit", "invariants", "--repo", dir)
	if code != 1 {
		t.Errorf("dirty repo: expected exit 1, got %d\nstdout=%s", code, stdout)
	}
	if !strings.Contains(stdout, "[I3]") || !strings.Contains(stdout, "[I5]") {
		t.Errorf("stdout should mention both I3 and I5 violations: %s", stdout)
	}
}

func TestAuditInvariants_RepoWithoutHookDirExitsTwo(t *testing.T) {
	dir := t.TempDir() // no internal/, no hooks/
	_, stderr, code := run(t, nil, "audit", "invariants", "--repo", dir)
	if code != 2 {
		t.Errorf("non-repo: expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "missing") {
		t.Errorf("stderr should explain: %s", stderr)
	}
}

func TestAuditInvariants_UnknownFlag(t *testing.T) {
	_, stderr, code := run(t, nil, "audit", "invariants", "--bogus")
	if code != 2 {
		t.Errorf("unknown flag: expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "unknown flag") {
		t.Errorf("stderr mismatch: %s", stderr)
	}
}
