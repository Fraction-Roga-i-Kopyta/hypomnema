package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateDryRunAndExecute(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(filepath.Join(memDir, "feedback"), 0o755)
	os.WriteFile(filepath.Join(memDir, "feedback", "f1.md"),
		[]byte("---\ntype: feedback\nproject: global\ncreated: 2026-04-01\nstatus: active\nref_count: 5\n---\nrule body\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte("2026-04-01|inject|f1.md|s0\n"), 0o644)

	env := map[string]string{"CLAUDE_HOME": filepath.Join(home, ".claude"), "CLAUDE_MEMORY_DIR": memDir, "HYPOMNEMA_TODAY": "2026-05-29"}

	// dry-run: reports a plan, writes nothing.
	out, _, code := run(t, env, "migrate", "--dry-run")
	if code != 0 {
		t.Fatalf("dry-run exit=%d", code)
	}
	if !strings.Contains(out, "keep") && !strings.Contains(out, "Kept") {
		t.Errorf("dry-run should report a plan: %q", out)
	}
	globalDir := filepath.Join(home, ".claude", "memory-global")
	if _, err := os.Stat(filepath.Join(globalDir, "f1.md")); err == nil {
		t.Error("dry-run must NOT write native files")
	}

	// execute: writes native + backup.
	_, errOut, code := run(t, env, "migrate", "--execute")
	if code != 0 {
		t.Fatalf("execute exit=%d stderr=%s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(globalDir, "f1.md")); err != nil {
		t.Errorf("execute must write native f1.md: %v", err)
	}
}
