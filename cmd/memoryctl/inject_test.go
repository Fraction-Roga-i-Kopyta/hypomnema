package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if !strings.Contains(string(wal), "\x1fdocker.md|s1") {
		t.Errorf("expected inject WAL event, got:\n%s", wal)
	}
	if _, err := os.Stat(filepath.Join(memDir, ".runtime", "injected-s1.list")); err != nil {
		t.Errorf("injected-set file missing: %v", err)
	}
}

func TestInjectVerb_SessionDedup(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	os.MkdirAll(projDir, 0o755)
	os.MkdirAll(memDir, 0o755)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker\ntype: mistake\n---\ndocker cache\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	env := map[string]string{
		"CLAUDE_HOME": filepath.Join(home, ".claude"), "CLAUDE_MEMORY_DIR": memDir,
		"HYPOMNEMA_TODAY": "2026-05-29",
	}
	stdin := `{"session_id":"s1","cwd":"/tmp/proj","prompt":"docker"}`
	runStdin(t, env, stdin, "inject", "--event=SessionStart")
	out2, _, _ := runStdin(t, env, stdin, "inject", "--event=UserPromptSubmit")
	walBytes, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if n := strings.Count(string(walBytes), "\x1fdocker.md|s1"); n != 1 {
		t.Errorf("a fact injects once per session, expected 1 inject event, got %d:\n%s", n, walBytes)
	}
	var env2 struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out2), &env2); err != nil {
		t.Fatalf("second inject stdout not valid JSON: %v\n%s", err, out2)
	}
	if strings.Contains(env2.HookSpecificOutput.AdditionalContext, "docker cache") {
		t.Errorf("second inject must not re-render an already-injected fact: %q",
			env2.HookSpecificOutput.AdditionalContext)
	}
	// A different session starts fresh.
	stdin2 := `{"session_id":"s2","cwd":"/tmp/proj","prompt":"docker"}`
	runStdin(t, env, stdin2, "inject", "--event=SessionStart")
	walBytes, _ = os.ReadFile(filepath.Join(memDir, ".wal"))
	if n := strings.Count(string(walBytes), "\x1fdocker.md|s2"); n != 1 {
		t.Errorf("new session injects again, expected 1 event for s2, got %d:\n%s", n, walBytes)
	}
}

func TestInjectVerb_RuntimeListAccumulatesUnderBudget(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	os.MkdirAll(projDir, 0o755)
	os.MkdirAll(memDir, 0o755)
	filler := strings.Repeat("lorem ", 400) // ~2.4KB per body
	for _, name := range []string{"a.md", "b.md", "c.md", "d.md"} {
		os.WriteFile(filepath.Join(projDir, name),
			[]byte("---\nname: "+name+"\ntype: knowledge\n---\ndocker "+filler+"\n"), 0o644)
	}
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	env := map[string]string{
		"CLAUDE_HOME": filepath.Join(home, ".claude"), "CLAUDE_MEMORY_DIR": memDir,
		"HYPOMNEMA_TODAY": "2026-05-29",
	}
	stdin := `{"session_id":"s1","cwd":"/tmp/proj","prompt":"docker"}`
	out1, _, _ := runStdin(t, env, stdin, "inject", "--event=SessionStart")
	var env1 struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out1), &env1); err != nil {
		t.Fatalf("stdout not valid JSON: %v", err)
	}
	if n := len(env1.HookSpecificOutput.AdditionalContext); n > 8000 {
		t.Errorf("additionalContext exceeds the 8000-byte default budget: %d bytes", n)
	}
	// Budget cuts the first call short of the full corpus; the second call
	// injects the remainder, and the runtime list accumulates the union.
	runStdin(t, env, stdin, "inject", "--event=UserPromptSubmit")
	runStdin(t, env, stdin, "inject", "--event=UserPromptSubmit")
	walBytes, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	for _, name := range []string{"a.md", "b.md", "c.md", "d.md"} {
		if n := strings.Count(string(walBytes), "\x1f"+name+"|s1"); n != 1 {
			t.Errorf("%s: expected exactly 1 inject event across the session, got %d", name, n)
		}
	}
	list, err := os.ReadFile(filepath.Join(memDir, ".runtime", "injected-s1.list"))
	if err != nil {
		t.Fatalf("runtime list missing: %v", err)
	}
	for _, name := range []string{"a.md", "b.md", "c.md", "d.md"} {
		if n := strings.Count(string(list), name); n != 1 {
			t.Errorf("runtime list must accumulate %s exactly once, got %d:\n%s", name, n, list)
		}
	}
}

func TestInjectVerb_PrunesOldRuntimeLists(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	os.MkdirAll(projDir, 0o755)
	runtimeDir := filepath.Join(memDir, ".runtime")
	os.MkdirAll(runtimeDir, 0o755)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker\ntype: mistake\n---\ndocker cache\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	stale := filepath.Join(runtimeDir, "injected-stale.list")
	os.WriteFile(stale, []byte("old.md\n"), 0o600)
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"CLAUDE_HOME": filepath.Join(home, ".claude"), "CLAUDE_MEMORY_DIR": memDir,
		"HYPOMNEMA_TODAY": "2026-05-29",
	}
	stdin := `{"session_id":"s1","cwd":"/tmp/proj","prompt":"docker"}`
	runStdin(t, env, stdin, "inject", "--event=SessionStart")
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale runtime list should be pruned, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "injected-s1.list")); err != nil {
		t.Errorf("fresh runtime list must survive prune: %v", err)
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
