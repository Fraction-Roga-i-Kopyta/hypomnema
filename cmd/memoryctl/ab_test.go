package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestABVerb(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wal := "2026-04-01|inject-agg|hot.md|50\n2026-04-01|outcome-positive|hot.md|s0\n"
	for i := 0; i < 10; i++ {
		wal += "2026-04-01|inject|cold" + string(rune('0'+i)) + ".md|s0\n"
	}
	wal += "2026-04-10|trigger-useful|hot.md|s1\n"
	if err := os.WriteFile(filepath.Join(memDir, ".wal"), []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"CLAUDE_MEMORY_DIR": memDir}

	out, errOut, code := run(t, env, "ab")
	if code != 0 {
		t.Fatalf("ab exit=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "recall") || !strings.Contains(out, "ranked") {
		t.Errorf("expected a recall table mentioning ranked; got:\n%s", out)
	}
	if !strings.Contains(out, "K=") {
		t.Errorf("expected per-K rows; got:\n%s", out)
	}
}
