package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func guardStdin(memDir, name, content string) string {
	fp := filepath.Join(memDir, name)
	return `{"tool_name":"Write","tool_input":{"file_path":"` + fp + `","content":"` + content + `"}}`
}

func TestGuard_BlocksSecret(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(memDir, 0o755)
	env := map[string]string{"CLAUDE_MEMORY_DIR": memDir}
	_, errOut, code := runStdin(t, env, guardStdin(memDir, "mistakes/x.md", "api_key: sk_live_abcd1234efgh"), "guard")
	if code != 2 {
		t.Fatalf("secret in memory write must block (exit 2), got %d", code)
	}
	if !strings.Contains(errOut, "Blocked") {
		t.Errorf("expected a Blocked message on stderr, got %q", errOut)
	}
}

func TestGuard_AllowsClean(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(memDir, 0o755)
	env := map[string]string{"CLAUDE_MEMORY_DIR": memDir}
	_, _, code := runStdin(t, env, guardStdin(memDir, "mistakes/x.md", "a clean note about rate limits"), "guard")
	if code != 0 {
		t.Errorf("clean content must allow (exit 0), got %d", code)
	}
}

func TestGuard_OutsideMemoryDirAllowed(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(memDir, 0o755)
	env := map[string]string{"CLAUDE_MEMORY_DIR": memDir}
	stdin := `{"tool_name":"Write","tool_input":{"file_path":"/tmp/code.py","content":"api_key: sk_live_abcd1234efgh"}}`
	_, _, code := runStdin(t, env, stdin, "guard")
	if code != 0 {
		t.Errorf("non-memory write must be out of scope (exit 0), got %d", code)
	}
}

func TestGuard_AllowEnvBypass(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(memDir, 0o755)
	env := map[string]string{"CLAUDE_MEMORY_DIR": memDir, "HYPOMNEMA_ALLOW_SECRETS": "1"}
	_, _, code := runStdin(t, env, guardStdin(memDir, "seeds/hazard.md", "api_key: sk_live_abcd1234efgh"), "guard")
	if code != 0 {
		t.Errorf("HYPOMNEMA_ALLOW_SECRETS=1 must bypass (exit 0), got %d", code)
	}
}

func TestGuard_SecretsignoreWhitelist(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(memDir, 0o755)
	os.WriteFile(filepath.Join(memDir, ".secretsignore"), []byte("seeds/**\n"), 0o644)
	env := map[string]string{"CLAUDE_MEMORY_DIR": memDir}
	_, _, code := runStdin(t, env, guardStdin(memDir, "seeds/hazard.md", "api_key: sk_live_abcd1234efgh"), "guard")
	if code != 0 {
		t.Errorf("seeds/** whitelist must allow (exit 0), got %d", code)
	}
}

func TestGuard_FailSafeBadStdin(t *testing.T) {
	env := map[string]string{}
	_, _, code := runStdin(t, env, "not json", "guard")
	if code != 0 {
		t.Errorf("bad stdin must exit 0, got %d", code)
	}
}

func TestGuard_BlocksNativeProjectStore(t *testing.T) {
	home := t.TempDir()
	claudeHome := filepath.Join(home, ".claude")
	memDir := filepath.Join(claudeHome, "memory")
	projMem := filepath.Join(claudeHome, "projects", "-tmp-proj", "memory")
	os.MkdirAll(memDir, 0o755)
	os.MkdirAll(projMem, 0o755)
	env := map[string]string{"CLAUDE_HOME": claudeHome, "CLAUDE_MEMORY_DIR": memDir}
	stdin := `{"tool_name":"Write","tool_input":{"file_path":"` +
		filepath.Join(projMem, "x.md") + `","content":"api_key: sk_live_abcd1234efgh"}}`
	_, errOut, code := runStdin(t, env, stdin, "guard")
	if code != 2 {
		t.Fatalf("secret write into a native project store must block (exit 2), got %d (stderr=%s)", code, errOut)
	}
}

func TestGuard_BlocksGlobalStore(t *testing.T) {
	home := t.TempDir()
	claudeHome := filepath.Join(home, ".claude")
	memDir := filepath.Join(claudeHome, "memory")
	globDir := filepath.Join(claudeHome, "memory-global")
	os.MkdirAll(memDir, 0o755)
	os.MkdirAll(globDir, 0o755)
	env := map[string]string{"CLAUDE_HOME": claudeHome, "CLAUDE_MEMORY_DIR": memDir}
	stdin := `{"tool_name":"Write","tool_input":{"file_path":"` +
		filepath.Join(globDir, "x.md") + `","content":"password=hunter2hunter2"}}`
	_, _, code := runStdin(t, env, stdin, "guard")
	if code != 2 {
		t.Fatalf("secret write into the global store must block (exit 2), got %d", code)
	}
}

func TestGuard_BlocksEditNewStringInNativeStore(t *testing.T) {
	home := t.TempDir()
	claudeHome := filepath.Join(home, ".claude")
	memDir := filepath.Join(claudeHome, "memory")
	projMem := filepath.Join(claudeHome, "projects", "-tmp-proj", "memory")
	os.MkdirAll(memDir, 0o755)
	os.MkdirAll(projMem, 0o755)
	env := map[string]string{"CLAUDE_HOME": claudeHome, "CLAUDE_MEMORY_DIR": memDir}
	stdin := `{"tool_name":"Edit","tool_input":{"file_path":"` +
		filepath.Join(projMem, "x.md") + `","new_string":"token: ghp_abcdef1234567890"}}`
	_, _, code := runStdin(t, env, stdin, "guard")
	if code != 2 {
		t.Fatalf("secret Edit into a native store must block (exit 2), got %d", code)
	}
}

// Pins the escape hatch: a .secretsignore in the global store whitelists
// paths inside it (the documented location).
func TestGuard_GlobalSecretsignoreWhitelists(t *testing.T) {
	home := t.TempDir()
	claudeHome := filepath.Join(home, ".claude")
	memDir := filepath.Join(claudeHome, "memory")
	globDir := filepath.Join(claudeHome, "memory-global")
	os.MkdirAll(memDir, 0o755)
	os.MkdirAll(globDir, 0o755)
	os.WriteFile(filepath.Join(globDir, ".secretsignore"), []byte("allowed-*.md\n"), 0o644)
	env := map[string]string{"CLAUDE_HOME": claudeHome, "CLAUDE_MEMORY_DIR": memDir}
	stdin := `{"tool_name":"Write","tool_input":{"file_path":"` +
		filepath.Join(globDir, "allowed-x.md") + `","content":"api_key: sk_live_abcd1234efgh"}}`
	_, _, code := runStdin(t, env, stdin, "guard")
	if code != 0 {
		t.Errorf("whitelisted path must pass (exit 0), got %d", code)
	}
}
