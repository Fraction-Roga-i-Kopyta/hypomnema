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
