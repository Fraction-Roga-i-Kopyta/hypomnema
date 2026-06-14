package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func skillFixture(t *testing.T) map[string]string {
	t.Helper()
	home := t.TempDir()
	globalDir := filepath.Join(home, ".claude", "memory-global")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude", "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "memory", ".wal"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	write := func(name, skill string) {
		c := "---\ntype: skill-learning\nname: " + name +
			"\ndescription: learning for " + skill +
			"\nskill: " + skill + "\nstatus: active\ncreated: 2026-06-14\n---\n\nbody of " + name + "\n"
		if err := os.WriteFile(filepath.Join(globalDir, name+".md"), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("commit-learning", "commit")
	write("audit-learning", "audit")
	return map[string]string{
		"CLAUDE_HOME":            filepath.Join(home, ".claude"),
		"CLAUDE_MEMORY_DIR":      filepath.Join(home, ".claude", "memory"),
		"CLAUDE_PROJECT_CWD":     "/tmp/whatever",
		"HYPOMNEMA_TODAY":        "2026-06-14",
		"HYPOMNEMA_SESSION_ID":   "s9",
		"CLAUDE_CODE_SESSION_ID": "",
	}
}

func TestSkillInjectReturnsOnlyMatchingSkill(t *testing.T) {
	env := skillFixture(t)
	stdin := `{"session_id":"s9","tool_input":{"skill":"commit"}}`
	stdout, _, exit := runStdin(t, env, stdin, "skill-inject")
	if exit != 0 {
		t.Fatalf("exit=%d, want 0", exit)
	}
	if !strings.Contains(stdout, "commit-learning") {
		t.Fatalf("want commit-learning in output, got: %s", stdout)
	}
	if strings.Contains(stdout, "audit-learning") {
		t.Fatalf("audit-learning leaked across skills: %s", stdout)
	}
	if !strings.Contains(stdout, "hookSpecificOutput") {
		t.Fatalf("want hookSpecificOutput envelope, got: %s", stdout)
	}
}

func TestSkillInjectNoLearningsIsSilentExit0(t *testing.T) {
	env := skillFixture(t)
	stdin := `{"session_id":"s9","tool_input":{"skill":"nonexistent"}}`
	stdout, _, exit := runStdin(t, env, stdin, "skill-inject")
	if exit != 0 {
		t.Fatalf("exit=%d, want 0", exit)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("want empty output for no learnings, got: %s", stdout)
	}
}

func TestSkillInjectMissingSkillIsSilentExit0(t *testing.T) {
	env := skillFixture(t)
	stdin := `{"session_id":"s9","tool_input":{}}`
	_, _, exit := runStdin(t, env, stdin, "skill-inject")
	if exit != 0 {
		t.Fatalf("exit=%d, want 0 (fail-safe)", exit)
	}
}

// TestSkillInjectRecallUsesStdinSessionID is a regression test for the bug
// where skill-inject keyed WAL recall events to the env-resolved session id
// instead of the session_id carried in the stdin envelope.
//
// In the real PostToolUse hook path no shim exports HYPOMNEMA_SESSION_ID, so
// events were silently attributed to "cli", breaking reinforcement attribution
// and same-session dedup. The fix reads session_id from stdin first and only
// falls back to env when stdin carries nothing.
func TestSkillInjectRecallUsesStdinSessionID(t *testing.T) {
	env := skillFixture(t)
	// Use a different env session id so the two sources are distinguishable.
	env["HYPOMNEMA_SESSION_ID"] = "env-session"
	env["CLAUDE_CODE_SESSION_ID"] = ""

	// Stdin carries a different session_id — this is the one that must win.
	stdin := `{"session_id":"stdin-xyz","tool_input":{"skill":"commit"}}`
	_, _, exit := runStdin(t, env, stdin, "skill-inject")
	if exit != 0 {
		t.Fatalf("exit=%d, want 0", exit)
	}

	walPath := filepath.Join(env["CLAUDE_MEMORY_DIR"], ".wal")
	walBytes, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatalf("reading WAL: %v", err)
	}
	walContent := string(walBytes)

	// Must contain a recall event keyed to the stdin session.
	if !strings.Contains(walContent, "|recall|") {
		t.Fatalf("WAL has no recall events at all:\n%s", walContent)
	}
	if !strings.Contains(walContent, "stdin-xyz") {
		t.Fatalf("WAL recall events not keyed to stdin session_id 'stdin-xyz':\n%s", walContent)
	}
	// Must NOT be keyed to the env session or the fallback "cli".
	for _, bad := range []string{"|recall|commit-learning|env-session", "|recall|commit-learning|cli"} {
		if strings.Contains(walContent, bad) {
			t.Fatalf("WAL recall event incorrectly keyed to %q instead of stdin session:\n%s", bad, walContent)
		}
	}
}
