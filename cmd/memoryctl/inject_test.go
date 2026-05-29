package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectVerb(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	os.MkdirAll(projDir, 0o755)
	os.MkdirAll(memDir, 0o755)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker cache\ntype: mistake\n---\ndocker layer cache\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)

	env := map[string]string{
		"CLAUDE_HOME": filepath.Join(home, ".claude"), "CLAUDE_MEMORY_DIR": memDir,
		"HYPOMNEMA_TODAY": "2026-05-29",
	}
	stdin := `{"session_id":"s1","cwd":"/tmp/proj","prompt":"fix docker cache"}`
	out, errOut, code := runStdin(t, env, stdin, "inject", "--event=UserPromptSubmit")
	if code != 0 {
		t.Fatalf("inject exit=%d stderr=%s", code, errOut)
	}
	var env2 struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &env2); err != nil {
		t.Fatalf("stdout is not valid envelope JSON: %v\n%s", err, out)
	}
	if env2.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Errorf("hookEventName = %q", env2.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(env2.HookSpecificOutput.AdditionalContext, "docker") {
		t.Errorf("expected docker memory in context: %q", env2.HookSpecificOutput.AdditionalContext)
	}
	wal, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if !strings.Contains(string(wal), "|inject|docker.md|s1") {
		t.Errorf("expected inject WAL event, got:\n%s", wal)
	}
	if _, err := os.Stat(filepath.Join(memDir, ".runtime", "injected-s1.list")); err != nil {
		t.Errorf("injected-set file missing: %v", err)
	}
}

func TestInjectVerb_FailSafeOnBadStdin(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(memDir, 0o755)
	env := map[string]string{"CLAUDE_HOME": filepath.Join(home, ".claude"), "CLAUDE_MEMORY_DIR": memDir}
	out, _, code := runStdin(t, env, "not json at all", "inject", "--event=SessionStart")
	if code != 0 {
		t.Fatalf("bad stdin must exit 0 (fail-safe), got %d", code)
	}
	if strings.TrimSpace(out) != "" {
		var v map[string]any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Errorf("fail-safe output must be empty or valid JSON, got: %q", out)
		}
	}
}
