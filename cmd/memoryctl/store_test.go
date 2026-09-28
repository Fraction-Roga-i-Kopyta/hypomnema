package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
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
	if got.Kind != "session-pin" || got.Anchor != "/tmp/proj" {
		t.Errorf("pin lookup: got %+v (want Kind=session-pin, Anchor=/tmp/proj)", got)
	}
	// Verify store is re-derived: Dir and Project should match what StoreFor produces from the anchor
	expected := native.StoreFor(configDir(), "/tmp/proj")
	if got.Dir != expected.Dir || got.Project != expected.Project {
		t.Errorf("store not re-derived: got Dir=%q Project=%q, want Dir=%q Project=%q",
			got.Dir, got.Project, expected.Dir, expected.Project)
	}
}

func TestSessionPin_ForgedStoreFieldsIgnored(t *testing.T) {
	home := t.TempDir()
	claude, mem := filepath.Join(home, ".claude"), filepath.Join(home, ".claude", "memory")
	setStoreEnv(t, claude, mem)
	os.MkdirAll(filepath.Join(mem, ".runtime"), 0o755)
	// Forge a pin with false dir/project fields; they must be ignored.
	os.WriteFile(pinPath("s-3"), []byte(`{"anchor":"/tmp/proj","dir":"/tmp/attacker-dir","project":"forged"}`), 0o600)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "s-3")
	got := resolveStore("")
	if got.Kind != "session-pin" || got.Anchor != "/tmp/proj" {
		t.Errorf("forged pin: got %+v (want Kind=session-pin, Anchor=/tmp/proj)", got)
	}
	// Verify the store is re-derived from /tmp/proj, NOT from attacker-dir.
	expected := native.StoreFor(configDir(), "/tmp/proj")
	if got.Dir != expected.Dir {
		t.Errorf("forged pin: Dir=%q (got from attacker-dir?), want %q (from anchor)", got.Dir, expected.Dir)
	}
	if got.Dir == "/tmp/attacker-dir" {
		t.Errorf("SECURITY: forged dir field was trusted! got %q", got.Dir)
	}
}

func TestSessionPin_CorruptFallsBack(t *testing.T) {
	home := t.TempDir()
	claude, mem := filepath.Join(home, ".claude"), filepath.Join(home, ".claude", "memory")
	setStoreEnv(t, claude, mem)
	os.MkdirAll(filepath.Join(mem, ".runtime"), 0o755)
	os.WriteFile(pinPath("s-2"), []byte(`{"anchor":"relative"}`), 0o600)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "s-2")
	if r := resolveStore("/tmp/cwd"); r.Kind != "cwd" {
		t.Errorf("relative anchor: must fallback to cwd, got %+v", r)
	}
}
