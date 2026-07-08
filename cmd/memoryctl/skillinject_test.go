package main

import (
	"encoding/json"
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

func TestSkillActiveWritesMarker(t *testing.T) {
	env := skillFixture(t)
	stdin := `{"session_id":"s9","tool_input":{"skill":"commit"}}`
	_, _, exit := runStdin(t, env, stdin, "skill-active")
	if exit != 0 {
		t.Fatalf("exit=%d, want 0", exit)
	}
	marker := filepath.Join(env["CLAUDE_MEMORY_DIR"], ".runtime", "active-skill-s9")
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("marker not written: %v", err)
	}
	if strings.TrimSpace(string(data)) != "commit" {
		t.Fatalf("want marker=commit, got %q", string(data))
	}
}

func TestSkillActiveSanitizesSessionID(t *testing.T) {
	env := skillFixture(t)
	// hostile session id with path separators and traversal components
	stdin := `{"session_id":"../../evil","tool_input":{"skill":"commit"}}`
	_, _, exit := runStdin(t, env, stdin, "skill-active")
	if exit != 0 {
		t.Fatalf("exit=%d, want 0", exit)
	}
	runtimeDir := filepath.Join(env["CLAUDE_MEMORY_DIR"], ".runtime")
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		t.Fatalf("read .runtime: %v", err)
	}
	// exactly one marker, and it lives directly under .runtime (no traversal escape)
	found := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "active-skill-") {
			found++
			// The marker must be a plain filename component: no slash means
			// filepath.Join can never escape the .runtime directory regardless
			// of dot sequences. SafeFileName replaces '/' with '_'; dots are
			// allowed as filename characters and are harmless without a separator.
			if strings.Contains(e.Name(), "/") {
				t.Fatalf("marker name contains path separator (traversal possible): %q", e.Name())
			}
		}
	}
	if found != 1 {
		t.Fatalf("want exactly 1 sanitized marker in .runtime, got %d", found)
	}
	// nothing escaped above .runtime into CLAUDE_MEMORY_DIR/evil
	if _, err := os.Stat(filepath.Join(env["CLAUDE_MEMORY_DIR"], "evil")); err == nil {
		t.Fatal("traversal escaped: found CLAUDE_MEMORY_DIR/evil")
	}
}

func TestSkillInject_TotalBudgetBounded(t *testing.T) { // review H2
	home := t.TempDir()
	globalDir := filepath.Join(home, ".claude", "memory-global")
	os.MkdirAll(globalDir, 0o755)
	os.MkdirAll(filepath.Join(home, ".claude", "memory"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", "memory", ".wal"), nil, 0o644)
	big := strings.Repeat("x", 2400) // each learning ~2.4KB → 5 of them ~12KB
	for i := 0; i < 5; i++ {
		name := "big-learning-" + string(rune('a'+i))
		c := "---\ntype: skill-learning\nname: " + name +
			"\ndescription: d\nskill: commit\nstatus: active\ncreated: 2026-06-14\n---\n\n" + big + "\n"
		os.WriteFile(filepath.Join(globalDir, name+".md"), []byte(c), 0o644)
	}
	env := map[string]string{
		"CLAUDE_HOME": filepath.Join(home, ".claude"), "CLAUDE_MEMORY_DIR": filepath.Join(home, ".claude", "memory"),
		"CLAUDE_PROJECT_CWD": "/tmp/whatever", "HYPOMNEMA_TODAY": "2026-06-14", "HYPOMNEMA_SESSION_ID": "s9",
	}
	stdout, _, exit := runStdin(t, env, `{"session_id":"s9","tool_input":{"skill":"commit"}}`, "skill-inject")
	if exit != 0 {
		t.Fatalf("exit=%d", exit)
	}
	var out struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &out); err != nil {
		t.Fatalf("bad envelope: %v\n%s", err, stdout)
	}
	if n := len(out.HookSpecificOutput.AdditionalContext); n > 8000 {
		t.Errorf("skill-inject additionalContext is %d bytes — no total budget (would be diverted)", n)
	}
}
