package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reindex regenerates <project>/memory/MEMORY.md from the native files, with
// sibling links — replacing any orphaned v1 index.
func TestReindex_WritesProjectIndexWithSiblingLinks(t *testing.T) {
	claudeHome := filepath.Join(t.TempDir(), ".claude")
	cwd := "/work/myproj"
	memDir := filepath.Join(claudeHome, "projects", "-work-myproj", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := "---\nname: real-db-in-tests\ndescription: Integration tests hit a real DB\ntype: feedback\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(memDir, "real-db-in-tests.md"), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := run(t, map[string]string{
		"CLAUDE_HOME":        claudeHome,
		"CLAUDE_PROJECT_CWD": cwd,
	}, "reindex")
	if code != 0 {
		t.Fatalf("reindex exit %d; stderr %s", code, stderr)
	}

	got, err := os.ReadFile(filepath.Join(memDir, "MEMORY.md"))
	if err != nil {
		t.Fatalf("MEMORY.md not written: %v", err)
	}
	if !strings.Contains(string(got), "](real-db-in-tests.md)") {
		t.Errorf("index missing sibling link; got:\n%s", got)
	}
	if strings.Contains(string(got), "../") {
		t.Errorf("index must not contain v1 relative paths; got:\n%s", got)
	}
}
