package inject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T) (memDir, projDir, home string) {
	t.Helper()
	home = t.TempDir()
	memDir = filepath.Join(home, ".claude", "memory")
	projDir = filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return memDir, projDir, home
}

func TestRun_RanksAndRenders(t *testing.T) {
	memDir, projDir, home := setup(t)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker cache\ntype: mistake\n---\ndocker layer cache stale\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "sql.md"),
		[]byte("---\nname: SQL index\ntype: knowledge\n---\npostgres index plan\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)

	res, err := Run(Input{
		Event: "UserPromptSubmit", SessionID: "s1",
		CWD: "/tmp/proj", Prompt: "fix the docker cache",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 5,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasPrefix(res.Markdown, "# Memory Context") {
		t.Errorf("markdown must start with the context header, got:\n%s", res.Markdown)
	}
	if !strings.Contains(res.Markdown, "docker") {
		t.Errorf("docker memory should be injected:\n%s", res.Markdown)
	}
	var hasDocker bool
	for _, s := range res.Injected {
		if s == "docker.md" {
			hasDocker = true
		}
	}
	if !hasDocker {
		t.Errorf("docker.md should be in Injected: %v", res.Injected)
	}
}

func TestRun_EmptyCorpus(t *testing.T) {
	memDir, _, home := setup(t)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	res, err := Run(Input{
		Event: "SessionStart", SessionID: "s1", CWD: "/tmp/proj",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 5,
	})
	if err != nil {
		t.Fatalf("Run on empty corpus should not error: %v", err)
	}
	if res.Markdown != "" && !strings.HasPrefix(res.Markdown, "# Memory Context") {
		t.Errorf("empty corpus markdown unexpected: %q", res.Markdown)
	}
	if len(res.Injected) != 0 {
		t.Errorf("empty corpus should inject nothing, got %v", res.Injected)
	}
}

func TestRun_DegradedWhenSidecarUnusable(t *testing.T) {
	memDir, projDir, home := setup(t)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker\ntype: mistake\n---\ndocker cache\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	// .sidecar.db is a NON-EMPTY directory → Open fails, Remove fails → degraded path.
	if err := os.MkdirAll(filepath.Join(memDir, ".sidecar.db", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := Run(Input{
		Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj", Prompt: "docker",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-05-29", MaxK: 5,
	})
	if err != nil {
		t.Fatalf("Run must not error when sidecar is unusable: %v", err)
	}
	if !strings.Contains(res.Markdown, "docker") {
		t.Errorf("degraded fallback must still inject docker, got:\n%s", res.Markdown)
	}
}

func TestRun_DegradedOnCorruptSidecar(t *testing.T) {
	memDir, projDir, home := setup(t)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker\ntype: mistake\n---\ndocker cache\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	if err := os.WriteFile(filepath.Join(memDir, ".sidecar.db"), []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Run(Input{
		Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj",
		Prompt: "docker", ClaudeHome: filepath.Join(home, ".claude"),
		MemoryDir: memDir, Today: "2026-05-29", MaxK: 5,
	})
	if err != nil {
		t.Fatalf("Run must recover from a corrupt sidecar, not error: %v", err)
	}
	if !strings.Contains(res.Markdown, "docker") {
		t.Errorf("after recovery, docker memory should still inject:\n%s", res.Markdown)
	}
}
