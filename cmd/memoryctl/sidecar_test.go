package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSidecarRebuildAndShow(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("mkdir mem: %v", err)
	}
	os.WriteFile(filepath.Join(projDir, "feedback_x.md"),
		[]byte("---\nname: Rule X\ndescription: do X\ntype: feedback\n---\nbody\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"),
		[]byte("2026-04-01|inject|feedback_x.md|s1\n2026-04-02|inject|feedback_x.md|s2\n2026-04-02|outcome-positive|feedback_x.md|s2\n"), 0o644)

	env := map[string]string{
		"CLAUDE_HOME":        filepath.Join(home, ".claude"),
		"CLAUDE_MEMORY_DIR":  memDir,
		"CLAUDE_PROJECT_CWD": "/tmp/proj",
	}

	out, errOut, code := run(t, env, "sidecar", "rebuild")
	if code != 0 {
		t.Fatalf("rebuild exit=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "1 file") && !strings.Contains(out, "rebuilt") {
		t.Errorf("rebuild summary unexpected: %q", out)
	}

	out, errOut, code = run(t, env, "sidecar", "show")
	if code != 0 {
		t.Fatalf("show exit=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "feedback_x.md") || !strings.Contains(out, "ref=2") {
		t.Errorf("show output missing slug/ref: %q", out)
	}
}
