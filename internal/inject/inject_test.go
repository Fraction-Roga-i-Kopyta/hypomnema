package inject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
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

// Regression for the live cross-project leak: rows seeded into the shared
// sidecar by another project's session must not inject here, and this
// project's own files must (the sidecar self-heals via a scoped reproject).
func TestRun_ExcludesOtherProjectCandidates(t *testing.T) {
	memDir, projDirA, home := setup(t)
	projDirB := filepath.Join(home, ".claude", "projects", "-tmp-other", "memory")
	if err := os.MkdirAll(projDirB, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(projDirA, "mine.md"),
		[]byte("---\nname: mine\ntype: knowledge\n---\ndocker mine\n"), 0o644)
	os.WriteFile(filepath.Join(projDirB, "theirs.md"),
		[]byte("---\nname: theirs\ntype: knowledge\n---\ndocker theirs\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)

	// A session in the OTHER project seeds the shared sidecar first.
	if _, err := Run(Input{
		Event: "SessionStart", SessionID: "sb", CWD: "/tmp/other", Prompt: "docker",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 8,
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	res, err := Run(Input{
		Event: "SessionStart", SessionID: "sa", CWD: "/tmp/proj", Prompt: "docker",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 8,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var hasMine bool
	for _, slug := range res.Injected {
		if slug == "theirs.md" {
			t.Errorf("another project's fact leaked into this session: %v", res.Injected)
		}
		if slug == "mine.md" {
			hasMine = true
		}
	}
	if !hasMine {
		t.Errorf("own project's fact must inject, got %v", res.Injected)
	}
}

// On otherwise equal signals a project-local fact outranks a global one
// (the rank projectBoost, which requires Query.Project to be wired up).
func TestRun_ProjectFactOutranksGlobalOnTie(t *testing.T) {
	memDir, projDir, home := setup(t)
	globDir := filepath.Join(home, ".claude", "memory-global")
	if err := os.MkdirAll(globDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Slug order alone would put the global fact first.
	os.WriteFile(filepath.Join(globDir, "a-global.md"),
		[]byte("---\nname: aglob\ntype: knowledge\n---\ndocker body\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "b-project.md"),
		[]byte("---\nname: bproj\ntype: knowledge\n---\ndocker body\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)

	res, err := Run(Input{
		Event: "SessionStart", SessionID: "s1", CWD: "/tmp/proj", Prompt: "docker",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 8,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Injected) == 0 || res.Injected[0] != "b-project.md" {
		t.Errorf("project-local fact must outrank the global tie, got %v", res.Injected)
	}
}

// Frontmatter keywords are a first-class relevance signal: a fact whose
// keywords match the prompt outranks one that matches nothing, even when
// the keyword never appears in the body.
func TestRun_FrontmatterKeywordsDriveOverlap(t *testing.T) {
	memDir, projDir, home := setup(t)
	// Slug order alone would put a-pg.md first.
	os.WriteFile(filepath.Join(projDir, "a-pg.md"),
		[]byte("---\nname: pg\ntype: knowledge\n---\npostgres planner notes\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "z-kube.md"),
		[]byte("---\nname: kube\ntype: knowledge\nkeywords: [kubernetes, probes]\n---\nprobe tuning\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)

	res, err := Run(Input{
		Event: "SessionStart", SessionID: "s1", CWD: "/tmp/proj", Prompt: "kubernetes",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 8,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Injected) == 0 || res.Injected[0] != "z-kube.md" {
		t.Errorf("keyword-matching fact must rank first, got %v", res.Injected)
	}
}

// The degraded (sidecar-less) path must honour frontmatter keywords too.
func TestRun_DegradedHonoursFrontmatterKeywords(t *testing.T) {
	memDir, projDir, home := setup(t)
	os.WriteFile(filepath.Join(projDir, "a-pg.md"),
		[]byte("---\nname: pg\ntype: knowledge\n---\npostgres planner notes\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "z-kube.md"),
		[]byte("---\nname: kube\ntype: knowledge\nkeywords: [kubernetes]\n---\nprobe tuning\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	// Unremovable non-empty dir at the sidecar path forces the degraded rank.
	if err := os.MkdirAll(filepath.Join(memDir, ".sidecar.db", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := Run(Input{
		Event: "SessionStart", SessionID: "s1", CWD: "/tmp/proj", Prompt: "kubernetes",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 8,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Injected) == 0 || res.Injected[0] != "z-kube.md" {
		t.Errorf("degraded rank must use frontmatter keywords, got %v", res.Injected)
	}
}

func TestExportedCapBody(t *testing.T) {
	long := strings.Repeat("я", 3000) // 6000 bytes of UTF-8
	got := CapBody(long, MaxBodyBytes)
	if len(got) > MaxBodyBytes+len("\n\n…(truncated)") {
		t.Fatalf("cap exceeded: %d bytes", len(got))
	}
	if !strings.HasSuffix(got, "…(truncated)") {
		t.Fatal("missing truncation marker")
	}
}

func TestCandidates_CorruptSidecarSiblingsRemoved(t *testing.T) {
	dir := t.TempDir()
	side := filepath.Join(dir, ".sidecar.db")
	for _, p := range []string{side, side + "-wal", side + "-shm"} {
		if err := os.WriteFile(p, []byte("not a database"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := []native.MemFile{{Slug: "a.md", Project: "global", Type: "note", Status: "active"}}
	Candidates(dir, "/work/proj", files, []string{"a"})
	for _, suffix := range []string{"-wal", "-shm"} {
		if data, err := os.ReadFile(side + suffix); err == nil &&
			strings.Contains(string(data), "not a database") {
			t.Errorf("poisoned .sidecar.db%s survived the corrupt-sidecar fallback", suffix)
		}
	}
}
