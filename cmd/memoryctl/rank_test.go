package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRankVerb(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker cache\ntype: mistake\n---\ndocker layer cache stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "sql.md"),
		[]byte("---\nname: SQL index\ntype: knowledge\n---\npostgres index plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{
		"CLAUDE_HOME":        filepath.Join(home, ".claude"),
		"CLAUDE_MEMORY_DIR":  memDir,
		"CLAUDE_PROJECT_CWD": "/tmp/proj",
	}

	if _, errOut, code := run(t, env, "sidecar", "rebuild"); code != 0 {
		t.Fatalf("rebuild failed: %s", errOut)
	}

	out, errOut, code := run(t, env, "rank", "--query", "docker cache", "--k", "2")
	if code != 0 {
		t.Fatalf("rank exit=%d stderr=%s", code, errOut)
	}
	di := strings.Index(out, "docker.md")
	si := strings.Index(out, "sql.md")
	if di == -1 {
		t.Fatalf("docker.md not in output: %q", out)
	}
	if si != -1 && di > si {
		t.Errorf("docker.md should rank above sql.md for 'docker cache'; got:\n%s", out)
	}
}
