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
