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

// mkBody returns a ~n-byte body that contains the given keyword so every
// file matches the prompt with overlap 1 and ranking falls back to the
// deterministic slug tie-break.
func mkBody(keyword string, n int) string {
	filler := strings.Repeat("lorem ", n/6)
	return keyword + " " + filler
}

func TestRun_TotalBudgetCapsRender(t *testing.T) {
	memDir, projDir, home := setup(t)
	for _, name := range []string{"a.md", "b.md", "c.md", "d.md", "e.md", "f.md"} {
		os.WriteFile(filepath.Join(projDir, name),
			[]byte("---\nname: "+name+"\ntype: knowledge\n---\n"+mkBody("docker", 1200)+"\n"), 0o644)
	}
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)

	res, err := Run(Input{
		Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj", Prompt: "docker",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 8, MaxBytes: 3000,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Markdown) > 3000 {
		t.Errorf("markdown exceeds the 3000-byte budget: %d bytes", len(res.Markdown))
	}
	if len(res.Injected) == 0 || len(res.Injected) >= 6 {
		t.Errorf("budget should cut the injected set to a non-empty prefix, got %d: %v",
			len(res.Injected), res.Injected)
	}
	// Injected must be the top-ranked prefix (slug tie-break ascending), and
	// the render must agree with the Injected bookkeeping.
	if res.Injected[0] != "a.md" {
		t.Errorf("expected top-ranked a.md first, got %v", res.Injected)
	}
	if !strings.Contains(res.Markdown, "## a.md") {
		t.Errorf("rendered markdown must contain the injected entry:\n%s", res.Markdown)
	}
	for _, slug := range []string{"e.md", "f.md"} {
		if strings.Contains(res.Markdown, "## "+slug) {
			t.Errorf("%s should have been cut by the budget:\n%s", slug, res.Markdown)
		}
	}
}

func TestRun_FirstEntryLargerThanBudgetIsTruncated(t *testing.T) {
	memDir, projDir, home := setup(t)
	os.WriteFile(filepath.Join(projDir, "big.md"),
		[]byte("---\nname: big\ntype: note\n---\n"+mkBody("docker", 5000)+"\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)

	res, err := Run(Input{
		Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj", Prompt: "docker",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 8, MaxBytes: 1000,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Markdown) > 1000 {
		t.Errorf("markdown exceeds budget: %d bytes", len(res.Markdown))
	}
	if len(res.Injected) != 1 || res.Injected[0] != "big.md" {
		t.Errorf("the single fact must still inject (truncated), got %v", res.Injected)
	}
	if !strings.Contains(res.Markdown, "…(truncated)") {
		t.Errorf("oversized body must carry the truncation marker:\n%s", res.Markdown)
	}
}

func TestRun_SkipsAlreadyInjected(t *testing.T) {
	memDir, projDir, home := setup(t)
	os.WriteFile(filepath.Join(projDir, "a.md"),
		[]byte("---\nname: alpha\ntype: knowledge\n---\ndocker alpha\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "b.md"),
		[]byte("---\nname: beta\ntype: knowledge\n---\ndocker beta\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)

	res, err := Run(Input{
		Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj", Prompt: "docker",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 8, AlreadyInjected: []string{"a.md"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Injected) != 1 || res.Injected[0] != "b.md" {
		t.Errorf("only b.md should inject (a.md already in session), got %v", res.Injected)
	}
	if strings.Contains(res.Markdown, "## alpha") {
		t.Errorf("already-injected fact must not re-render:\n%s", res.Markdown)
	}
}

func TestRun_AllAlreadyInjectedYieldsEmpty(t *testing.T) {
	memDir, projDir, home := setup(t)
	os.WriteFile(filepath.Join(projDir, "a.md"),
		[]byte("---\nname: alpha\ntype: knowledge\n---\ndocker alpha\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)

	res, err := Run(Input{
		Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj", Prompt: "docker",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 8, AlreadyInjected: []string{"a.md"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Injected) != 0 {
		t.Errorf("nothing new to inject, got %v", res.Injected)
	}
	if res.Markdown != "" {
		t.Errorf("no new facts → empty markdown, got %q", res.Markdown)
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
