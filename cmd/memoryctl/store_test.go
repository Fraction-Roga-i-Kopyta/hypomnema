package main

import (
	"os"
	"path/filepath"
	"testing"
)

func setStoreEnv(t *testing.T, claude, mem string) {
	t.Helper()
	for _, k := range []string{"CLAUDE_PROJECT_DIR", "CLAUDE_PROJECT_CWD", "CLAUDE_CODE_SESSION_ID",
		"HYPOMNEMA_SESSION_ID", "CLAUDE_CONFIG_DIR", "CLAUDE_COWORK_MEMORY_PATH_OVERRIDE"} {
		t.Setenv(k, "")
	}
	t.Setenv("CLAUDE_HOME", claude)
	t.Setenv("CLAUDE_MEMORY_DIR", mem)
}

func TestResolveStore_AnchorOrder(t *testing.T) {
	home := t.TempDir()
	claude, mem := filepath.Join(home, ".claude"), filepath.Join(home, ".claude", "memory")
	setStoreEnv(t, claude, mem)

	if r := resolveStore("/tmp/cwd"); r.Kind != "cwd" || r.Project != "-tmp-cwd" {
		t.Errorf("cwd fallback: %+v", r)
	}
	t.Setenv("CLAUDE_PROJECT_CWD", "/tmp/explicit")
	if r := resolveStore("/tmp/cwd"); r.Kind != "CLAUDE_PROJECT_CWD" || r.Project != "-tmp-explicit" {
		t.Errorf("explicit override must beat cwd outside hooks: %+v", r)
	}
	// Inside a hook CLAUDE_PROJECT_DIR is always set and must win, so a
	// CLAUDE_PROJECT_CWD exported globally (as pre-v2.13 docs suggested)
	// cannot collapse every session into one store.
	t.Setenv("CLAUDE_PROJECT_DIR", "/tmp/proj")
	if r := resolveStore("/tmp/cwd"); r.Kind != "CLAUDE_PROJECT_DIR" || r.Project != "-tmp-proj" {
		t.Errorf("harness anchor must beat CLAUDE_PROJECT_CWD and stdin cwd: %+v", r)
	}
}

func TestConfigDir_SeparateFromClaudeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "cfg"))
	if got := configDir(); got != filepath.Join(home, "cfg") {
		t.Errorf("configDir() = %q, want CLAUDE_CONFIG_DIR", got)
	}
	if got := claudeDir(); got != filepath.Join(home, ".claude") {
		t.Errorf("claudeDir() must ignore CLAUDE_CONFIG_DIR (global store stays beside ~/.claude), got %q", got)
	}
}

func TestSessionPin_RoundTrip(t *testing.T) {
	home := t.TempDir()
	claude, mem := filepath.Join(home, ".claude"), filepath.Join(home, ".claude", "memory")
	setStoreEnv(t, claude, mem)
	t.Setenv("CLAUDE_PROJECT_DIR", "/tmp/proj")
	r := resolveStore("")
	writePin("s-1", r)

	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "s-1")
	got := resolveStore("")
	if got.Kind != "session-pin" || got.Dir != r.Dir || got.Project != r.Project || got.Anchor != "/tmp/proj" {
		t.Errorf("pin lookup: got %+v, want store of %+v", got, r)
	}
}

func TestSessionPin_CorruptFallsBack(t *testing.T) {
	home := t.TempDir()
	claude, mem := filepath.Join(home, ".claude"), filepath.Join(home, ".claude", "memory")
	setStoreEnv(t, claude, mem)
	os.MkdirAll(filepath.Join(mem, ".runtime"), 0o755)
	os.WriteFile(pinPath("s-2"), []byte(`{"dir":"relative","project":""}`), 0o600)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "s-2")
	if r := resolveStore("/tmp/cwd"); r.Kind != "cwd" {
		t.Errorf("corrupt pin must be ignored: %+v", r)
	}
}
