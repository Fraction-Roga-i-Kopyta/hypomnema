package inject

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/closer"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
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
		Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj", Prompt: "docker",
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
			[]byte("---\nname: "+name+"\ntype: knowledge\nkeywords: [docker]\n---\n"+mkBody("docker", 1200)+"\n"), 0o644)
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
		[]byte("---\nname: big\ntype: note\nkeywords: [docker]\n---\n"+mkBody("docker", 5000)+"\n"), 0o644)
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
	if !strings.Contains(res.Markdown, "…(truncated") ||
		!strings.Contains(res.Markdown, "full text: ") ||
		!strings.Contains(res.Markdown, "big.md") {
		t.Errorf("oversized body must carry the truncation marker with size + path hint:\n%s", res.Markdown)
	}
}

func TestRun_SkipsAlreadyInjected(t *testing.T) {
	memDir, projDir, home := setup(t)
	os.WriteFile(filepath.Join(projDir, "a.md"),
		[]byte("---\nname: alpha\ntype: knowledge\nkeywords: [docker]\n---\ndocker alpha\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "b.md"),
		[]byte("---\nname: beta\ntype: knowledge\nkeywords: [docker]\n---\ndocker beta\n"), 0o644)
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
		[]byte("---\nname: alpha\ntype: knowledge\nkeywords: [docker]\n---\ndocker alpha\n"), 0o644)
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

	// The prompt names both facts (two terms each), so only scope keeps
	// theirs.md out.
	res, err := Run(Input{
		Event: "UserPromptSubmit", SessionID: "sa", CWD: "/tmp/proj", Prompt: "docker mine theirs",
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
		Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj", Prompt: "docker body",
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
		Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj", Prompt: "kubernetes",
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
		Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj", Prompt: "kubernetes",
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

// TestRun_CiteInstructionAndFactHeaders verifies that every rendered fact
// header carries its citable file name, the citation instruction line
// appears once right after the heading, and the whole payload (instruction
// line included) still respects MaxTotalBytes even with 8 fat facts
// competing for the budget.
func TestRun_CiteInstructionAndFactHeaders(t *testing.T) {
	memDir, projDir, home := setup(t)
	names := []string{"a.md", "b.md", "c.md", "d.md", "e.md", "f.md", "g.md", "h.md"}
	for _, name := range names {
		os.WriteFile(filepath.Join(projDir, name),
			[]byte("---\nname: "+name+"\ntype: knowledge\ncreated: 2026-05-01\nkeywords: [docker]\n---\n"+mkBody("docker", 900)+"\n"), 0o644)
	}
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)

	res, err := Run(Input{
		Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj", Prompt: "docker",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 8,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantPrefix := "# Memory Context\n" + CiteInstruction + "\n"
	if !strings.HasPrefix(res.Markdown, wantPrefix) {
		n := len(res.Markdown)
		if n > 200 {
			n = 200
		}
		t.Errorf("markdown must start with heading + CiteInstruction, got:\n%s", res.Markdown[:n])
	}
	if strings.Contains(res.Markdown, SubagentCiteInstruction) {
		t.Errorf("parent delivery must not carry the subagent instruction")
	}
	if len(res.Markdown) > MaxTotalBytes {
		t.Errorf("markdown exceeds MaxTotalBytes with 8 fat facts + instruction line: %d bytes", len(res.Markdown))
	}
	if len(res.Injected) == 0 {
		t.Fatalf("expected at least one fact injected")
	}
	for _, slug := range res.Injected {
		if !strings.Contains(res.Markdown, "— "+slug+" (") {
			t.Errorf("header for %s must carry its citable file name, got:\n%s", slug, res.Markdown)
		}
	}
}

func TestFactHeader(t *testing.T) {
	cases := []struct {
		name string
		f    native.MemFile
		want string
	}{
		{"full", native.MemFile{Name: "Docker cache", Slug: "docker.md", Type: "mistake", Created: "2026-05-01"},
			"## Docker cache — docker.md (mistake, 2026-05-01)"},
		{"no created omits date", native.MemFile{Name: "Docker cache", Slug: "docker.md", Type: "mistake"},
			"## Docker cache — docker.md (mistake)"},
		{"type falls back to note", native.MemFile{Name: "X", Slug: "x.md"},
			"## X — x.md (note)"},
		{"name falls back to slug", native.MemFile{Slug: "x.md", Type: "knowledge"},
			"## x.md — x.md (knowledge)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FactHeader(c.f); got != c.want {
				t.Errorf("FactHeader(%+v) = %q, want %q", c.f, got, c.want)
			}
		})
	}
}

func TestExportedCapBody(t *testing.T) {
	long := strings.Repeat("я", 3000) // 6000 bytes of UTF-8
	got := CapBody(long, MaxBodyBytes, "full text: /p/x.md")
	if len(got) > MaxBodyBytes {
		t.Fatalf("cap exceeded: %d bytes", len(got))
	}
	if !strings.Contains(got, "…(truncated — 6000 B total; full text: /p/x.md)") {
		t.Fatalf("missing size+hint marker; got tail %q", got[len(got)-90:])
	}
}

func TestCapBody_MarkerInsideBudgetWithHint(t *testing.T) {
	body := strings.Repeat("x", 5000)
	got := capBody(body, 500, "full text: /home/u/.claude/memory-global/big-note.md")
	if len(got) > 500 {
		t.Fatalf("marker must fit inside the cap: %d bytes", len(got))
	}
	if !strings.Contains(got, "…(truncated — 5000 B total; full text: /home/u/.claude/memory-global/big-note.md)") {
		t.Fatalf("marker must name total size and hint; got tail %q", got[len(got)-90:])
	}
}

// Cutting back by the marker length must still land on a rune boundary.
func TestCapBody_MultiByteAfterMarkerReserve(t *testing.T) {
	body := strings.Repeat("я", 3000) // 2 bytes each
	for _, cap := range []int{500, 501, 502, 503} {
		got := capBody(body, cap, "full text: /p/x.md")
		if len(got) > cap {
			t.Fatalf("cap %d: %d bytes", cap, len(got))
		}
		if !utf8.ValidString(got) {
			t.Fatalf("cap %d: split a rune", cap)
		}
	}
}

func TestCapBody_TinyBudgetFallsBackToShortMarker(t *testing.T) {
	got := capBody(strings.Repeat("x", 100), 20, "full text: /p/n.md")
	if !strings.HasSuffix(got, "…(truncated)") {
		t.Fatalf("tiny budget keeps the legacy marker; got %q", got)
	}
	if len(got) > 20+len("\n\n…(truncated)") {
		t.Fatalf("legacy overflow bound exceeded: %d", len(got))
	}
}

func TestCapBody_NoHint(t *testing.T) {
	got := capBody(strings.Repeat("y", 3000), 500, "")
	if !strings.Contains(got, "…(truncated — 3000 B total)") {
		t.Fatalf("hintless marker wrong: %q", got[len(got)-60:])
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
	Candidates(dir, native.Store{Project: "-work-proj"}, files, []string{"a"})
	for _, suffix := range []string{"-wal", "-shm"} {
		if data, err := os.ReadFile(side + suffix); err == nil &&
			strings.Contains(string(data), "not a database") {
			t.Errorf("poisoned .sidecar.db%s survived the corrupt-sidecar fallback", suffix)
		}
	}
}

// A subagent never writes to the user, so its delivery tells it to cite in
// the final report; the parent line must not appear there, and vice versa.
func TestRun_SubagentGetsReportCiteInstruction(t *testing.T) {
	memDir, projDir, home := setup(t)
	os.WriteFile(filepath.Join(projDir, "a.md"),
		[]byte("---\nname: a\ntype: knowledge\ncreated: 2026-05-01\nkeywords: [docker]\ndescription: docker layer cache\n---\ndocker layer cache\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)

	sub, err := Run(Input{
		Event: "SubagentStart", SessionID: "s1:a1", CWD: "/tmp/proj", Prompt: "docker",
		Brief:      "tune the docker layer cache",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 5, MaxBytes: 5000,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasPrefix(sub.Markdown, "# Memory Context\n"+SubagentCiteInstruction+"\n") {
		t.Errorf("subagent delivery must open with SubagentCiteInstruction:\n%s", sub.Markdown)
	}
	if strings.Contains(sub.Markdown, CiteInstruction) {
		t.Errorf("subagent delivery must not carry the parent instruction")
	}
	if len(sub.Markdown) > 5000 {
		t.Errorf("subagent payload %d bytes exceeds MaxBytes 5000", len(sub.Markdown))
	}
}

func TestCiteInstruction_CoversDecisions(t *testing.T) {
	if !strings.Contains(CiteInstruction, "changes what you say, do, or decide") {
		t.Errorf("CiteInstruction must cover decisions: %q", CiteInstruction)
	}
}

// On UserPromptSubmit a fact injects only when the prompt names what it is
// about (a prompt term in its name/description/keywords/domains) or shares
// at least two terms with it. Without the gate the top-K window was filled
// by recency and popularity alone; with a one-term gate a single filler word
// that happens to occur in a long body still passed. A cwd-basename hit does
// not count — it is not something the user asked. SessionStart does not
// rank at all: it injects the fixed core, and none of these facts is core.
func TestRun_UserPromptSubmitRequiresPromptMatch(t *testing.T) {
	memDir, projDir, home := setup(t)
	for name, body := range map[string]string{
		"docker.md":    "---\nname: Docker cache\ntype: mistake\n---\nlayer cache stale\n",
		"bodytwo.md":   "---\nname: Layer notes\ntype: knowledge\n---\nthe docker cache layer\n",
		"bodyone.md":   "---\nname: Redis ttl\ntype: knowledge\n---\nredis ttl cache\n",
		"unrelated.md": "---\nname: Postgres index\ntype: knowledge\n---\npostgres index plan\n",
		"cwdonly.md":   "---\nname: Proj layout\ntype: knowledge\n---\nproj layout notes\n",
	} {
		os.WriteFile(filepath.Join(projDir, name), []byte(body), 0o644)
	}
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)

	run := func(event, prompt string) Result {
		t.Helper()
		res, err := Run(Input{
			Event: event, SessionID: "s1", CWD: "/tmp/proj", Prompt: prompt,
			ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
			Today: "2026-05-29", MaxK: 8,
		})
		if err != nil {
			t.Fatalf("Run(%s): %v", event, err)
		}
		return res
	}

	res := run("UserPromptSubmit", "fix the docker cache")
	got := append([]string(nil), res.Injected...)
	sort.Strings(got)
	if strings.Join(got, ",") != "bodytwo.md,docker.md" {
		t.Errorf("want the head match and the two-term body match only, got %v", res.Injected)
	}

	res = run("UserPromptSubmit", "да, и что там")
	if len(res.Injected) != 0 || res.Markdown != "" {
		t.Errorf("a prompt with no relevance terms must inject nothing, got %v", res.Injected)
	}

	res = run("SessionStart", "")
	if len(res.Injected) != 0 {
		t.Errorf("SessionStart injects the core only (empty here), got %v", res.Injected)
	}
}

// The prompt gate scores a fact on its own file. continuity.md / notes.md
// exist in every project, and the sidecar's keyword overlap is grouped by
// slug alone — a gate fed from it let this project's continuity.md pass on
// the words of another project's continuity.md.
func TestRun_PromptGateIgnoresSameSlugInOtherProject(t *testing.T) {
	memDir, projDir, home := setup(t)
	otherDir := filepath.Join(home, ".claude", "projects", "-tmp-other", "memory")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(otherDir, "continuity.md"),
		[]byte("---\nname: continuity\ntype: continuity\n---\ndocker cache rebuild pending\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "continuity.md"),
		[]byte("---\nname: continuity\ntype: continuity\n---\npostgres index plan pending\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker cache\ntype: mistake\n---\nlayer cache stale\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)

	run := func(cwd, event, prompt string) Result {
		t.Helper()
		res, err := Run(Input{
			Event: event, SessionID: "s1", CWD: cwd, Prompt: prompt,
			ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
			Today: "2026-05-29", MaxK: 8,
		})
		if err != nil {
			t.Fatalf("Run(%s, %s): %v", cwd, event, err)
		}
		return res
	}
	// Seed the shared sidecar with the other project's rows first.
	run("/tmp/other", "SessionStart", "")

	res := run("/tmp/proj", "UserPromptSubmit", "fix the docker cache")
	if strings.Join(res.Injected, ",") != "docker.md" {
		t.Errorf("only docker.md matches the prompt in this project, got %v", res.Injected)
	}
}

// Only a project's freshest continuity is pushed: older continuity files are
// history and would otherwise crowd SessionStart (one file per session, none
// ever aged). Freshness is the later of created and Claude Code's `modified`
// stamp, so continuity.md rewritten in place beats an older dated sibling.
func TestRun_OnlyNewestContinuityInjects(t *testing.T) {
	memDir, projDir, home := setup(t)
	os.WriteFile(filepath.Join(projDir, "continuity.md"),
		[]byte("---\nname: continuity\ntype: continuity\ncreated: 2026-08-01\nmetadata:\n  modified: 2026-10-01T10:00:00Z\n---\nnext: ship docker cache fix\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "continuity-2026-09-01.md"),
		[]byte("---\nname: continuity 2026-09-01\ntype: continuity\ncreated: 2026-09-01\n---\nnext: review docker cache\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker cache\ntype: mistake\n---\nlayer cache stale\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)

	for _, ev := range []string{"SessionStart", "UserPromptSubmit"} {
		res, err := Run(Input{
			Event: ev, SessionID: "s-" + ev, CWD: "/tmp/proj", Prompt: "docker cache continuity",
			ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
			Today: "2026-10-05", MaxK: 8,
		})
		if err != nil {
			t.Fatalf("Run(%s): %v", ev, err)
		}
		got := strings.Join(res.Injected, ",")
		if !strings.Contains(got, "continuity.md") || strings.Contains(got, "continuity-2026-09-01.md") {
			t.Errorf("%s: want the freshest continuity only, got %v", ev, res.Injected)
		}
	}
}

// Injection and decay must agree on which continuity is the freshest: if
// they disagree, decay stales injection's pick while injection drops decay's
// pick as "not the newest", and no continuity is pushed at all. A file with
// no frontmatter date must not borrow a WAL date on one side only.
func TestRun_ContinuityChoiceMatchesDecay(t *testing.T) {
	memDir, projDir, home := setup(t)
	os.WriteFile(filepath.Join(projDir, "continuity.md"),
		[]byte("---\nname: continuity\ntype: continuity\n---\nnext: ship it\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "continuity-old.md"),
		[]byte("---\nname: continuity old\ntype: continuity\ncreated: 2025-12-01\n---\nold plan\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte("2026-01-01|inject|-tmp-proj\x1fcontinuity.md|s0\n"), 0o644)
	if _, err := closer.Run(closer.Input{SessionID: "s0", CWD: "/tmp/proj",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-11-09"}); err != nil {
		t.Fatal(err)
	}
	// SessionStart takes it through the core, UserPromptSubmit through the
	// ranked path; both must agree with decay.
	for ev, prompt := range map[string]string{"SessionStart": "", "UserPromptSubmit": "continuity plan"} {
		res, err := Run(Input{Event: ev, SessionID: "s1", CWD: "/tmp/proj", Prompt: prompt,
			ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-11-09", MaxK: 8})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Injected) != 1 {
			t.Errorf("%s: exactly one continuity (the freshest by frontmatter) must inject, got %v", ev, res.Injected)
		}
	}
}

// The window is MaxK however many candidates there are.
func TestRun_CapsAtMaxK(t *testing.T) {
	memDir, projDir, home := setup(t)
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		os.WriteFile(filepath.Join(projDir, n+".md"),
			[]byte("---\nname: "+n+" docker\ntype: knowledge\n---\ndocker cache\n"), 0o644)
	}
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	res, err := Run(Input{Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj", Prompt: "docker cache",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-05-29", MaxK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Injected) != 3 {
		t.Errorf("want MaxK=3 facts, got %v", res.Injected)
	}
}

// writeFM writes a native fact with the given frontmatter lines and body.
func writeFM(t *testing.T, dir, slug, fm, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, slug), []byte("---\n"+fm+"\n---\n"+body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runSS(t *testing.T, memDir, home, source, prompt string) Result {
	t.Helper()
	res, err := Run(Input{
		Event: "SessionStart", Source: source, SessionID: "s1", CWD: "/tmp/proj", Prompt: prompt,
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-11-24", MaxK: 8,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

// A fresh session gets the core (continuity, pinned, project feedback) and
// nothing ranked on cwd/git overlap.
func TestRun_SessionStartStartupInjectsOnlyCore(t *testing.T) {
	memDir, projDir, home := setup(t)
	writeFM(t, projDir, "continuity.md", "name: continuity\ntype: continuity\ncreated: 2026-11-23", "next: ship the core")
	writeFM(t, projDir, "rule.md", "name: proj rule\ntype: feedback\ncreated: 2026-11-01", "always run make lint")
	writeFM(t, projDir, "layout.md", "name: proj layout\ntype: knowledge\ncreated: 2026-11-24\nkeywords: [proj, tmp]", "proj tmp layout")
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	for _, src := range []string{"startup", "resume", "clear", "", "compact"} {
		res := runSS(t, memDir, home, src, "")
		if strings.Join(res.Injected, ",") != "continuity.md,rule.md" {
			t.Errorf("source %q: want the core only, got %v", src, res.Injected)
		}
		if strings.Contains(res.Markdown, "proj tmp layout") {
			t.Errorf("source %q: a cwd-matching knowledge fact must not inject:\n%s", src, res.Markdown)
		}
	}
}

// After compaction SessionStart injects the core only: a fact whose head
// the summary names twice stays out (the summary carries the task state).
func TestRun_SessionStartCompactInjectsCoreOnly(t *testing.T) {
	memDir, projDir, home := setup(t)
	writeFM(t, projDir, "continuity.md", "name: continuity\ntype: continuity\ncreated: 2026-11-23", "next: ship the core")
	writeFM(t, projDir, "zz-target.md", "name: Postgres migration lock\ntype: mistake\ncreated: 2025-01-01", "take the advisory lock first")
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	res := runSS(t, memDir, home, "compact", "fixing the postgres migration lock; the postgres migration lock times out")
	if strings.Join(res.Injected, ",") != "continuity.md" {
		t.Errorf("compact must inject the core only, got %v", res.Injected)
	}
}

// tail returns the last n bytes of s (all of it when shorter).
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// Core facts that do not fit are listed as pointers (not deliveries), and
// the Memory-queue line keeps its room.
func TestRun_SessionStartCoreOverBudget(t *testing.T) {
	memDir, projDir, home := setup(t)
	writeFM(t, projDir, "continuity.md", "name: continuity\ntype: continuity\ncreated: 2026-11-23", strings.Repeat("c", 2000))
	writeFM(t, projDir, "pin-a.md", "name: pin a\ntype: project\nstatus: pinned\ncreated: 2026-11-22", strings.Repeat("a", 2400))
	pinB := "---\nname: pin b\ntype: project\nstatus: pinned\ncreated: 2026-11-21\n---\n" + strings.Repeat("b", 2400) + "\n"
	os.WriteFile(filepath.Join(projDir, "pin-b.md"), []byte(pinB), 0o644)
	writeFM(t, projDir, "rule.md", "name: lint rule\ndescription: run make lint before commit\ntype: feedback\ncreated: 2026-11-20", strings.Repeat("f", 2400))
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	st := native.StoreFor(filepath.Join(home, ".claude"), "/tmp/proj")
	writeMark(t, memDir, st.Project, "pin-b", "review", native.BodySHA(pinB))

	res := runSS(t, memDir, home, "startup", "")
	if strings.Join(res.Injected, ",") != "continuity.md,pin-a.md,pin-b.md" {
		t.Fatalf("continuity and pinned render; got %v", res.Injected)
	}
	md := res.Markdown
	if len(md) > MaxTotalBytes {
		t.Errorf("markdown %d bytes exceeds the budget", len(md))
	}
	ptr := "- lint rule — " + filepath.Join(projDir, "rule.md") + " — run make lint before commit"
	if !strings.Contains(md, "_Not loaded (budget): read if relevant._\n"+ptr+"\n") {
		t.Errorf("feedback must appear as a pointer line:\n%s", tail(md, 700))
	}
	if strings.Contains(md, strings.Repeat("f", 100)) {
		t.Errorf("the overflowing body must not render")
	}
	if !strings.Contains(md, "Memory queue: 1 open task(s)") {
		t.Errorf("the Memory-queue line must survive the core:\n%s", tail(md, 700))
	}
	if strings.Index(md, ptr) > strings.Index(md, "Memory queue") {
		t.Errorf("pointers come before the queue line")
	}
}

func TestRun_SessionStartCoreHonoursMarksAndStale(t *testing.T) {
	memDir, projDir, home := setup(t)
	writeFM(t, projDir, "pin.md", "name: hidden pin\ntype: project\nstatus: pinned\ncreated: 2026-11-20", "pinned body")
	writeFM(t, projDir, "less.md", "name: less rule\ntype: feedback\ncreated: 2026-11-20", "less body")
	writeFM(t, projDir, "old.md", "name: old rule\ntype: feedback\ncreated: 2025-01-01", "old body")
	writeFM(t, projDir, "ok.md", "name: ok rule\ntype: feedback\ncreated: 2026-11-20", "ok body")
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	st := native.StoreFor(filepath.Join(home, ".claude"), "/tmp/proj")
	writeMark(t, memDir, st.Project, "pin", "hide", st.Project)
	writeMark(t, memDir, st.Project, "less", "less", "")
	// Seed the sidecar, then hold the year-old feedback as stale (decay no
	// longer stales project feedback; a row staled before that exemption, or
	// any other sidecar-owned stale state, must still leave the core).
	runSS(t, memDir, home, "startup", "")
	s, err := sidecar.Open(filepath.Join(memDir, ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(sidecar.Record{Slug: "old.md", Project: st.Project, Type: "feedback",
		Created: "2025-01-01", Status: "stale"}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	res := runSS(t, memDir, home, "startup", "")
	if strings.Join(res.Injected, ",") != "ok.md" {
		t.Errorf("hide, less and stale leave the core; got %v", res.Injected)
	}
}

func TestRun_SessionStartCoreDegradedSidecar(t *testing.T) {
	memDir, projDir, home := setup(t)
	writeFM(t, projDir, "continuity.md", "name: continuity\ntype: continuity\ncreated: 2026-11-23", "next: ship")
	writeFM(t, projDir, "card.md", "name: card\ntype: project\nstatus: pinned\ncreated: 2026-11-01", "project card")
	writeFM(t, projDir, "kn.md", "name: kn\ntype: knowledge\ncreated: 2026-11-24", "proj knowledge")
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	if err := os.MkdirAll(filepath.Join(memDir, ".sidecar.db", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := runSS(t, memDir, home, "startup", "")
	if strings.Join(res.Injected, ",") != "continuity.md,card.md" {
		t.Errorf("degraded core must carry frontmatter pinned; got %v", res.Injected)
	}
}

func TestRun_SessionStartEmptyCoreKeepsQueue(t *testing.T) {
	memDir, home := queueFixture(t)
	res := runSS(t, memDir, home, "startup", "")
	if !strings.HasPrefix(res.Markdown, "# Memory Context\nMemory queue: 1 open task(s)") {
		t.Fatalf("an empty core still announces the queue: %q", res.Markdown)
	}
	if len(res.Injected) != 0 {
		t.Errorf("no core facts here: %v", res.Injected)
	}
}

// Dedup by slug runs before the K cut, so a project/global same-slug pair
// does not waste a slot: the K-th distinct fact still renders.
func TestRun_DedupDoesNotWasteTopK(t *testing.T) {
	memDir, projDir, home := setup(t)
	globDir := filepath.Join(home, ".claude", "memory-global")
	if err := os.MkdirAll(globDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFM(t, projDir, "x.md", "name: x\ntype: knowledge\nkeywords: [docker, cache]", "docker cache")
	writeFM(t, globDir, "x.md", "name: x\ntype: knowledge\nkeywords: [docker, cache]", "docker cache")
	writeFM(t, projDir, "a.md", "name: a docker\ntype: knowledge", "docker")
	writeFM(t, projDir, "b.md", "name: b docker\ntype: knowledge", "docker")
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	res, err := Run(Input{
		Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj", Prompt: "docker cache",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-11-24", MaxK: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Injected, ",") != "x.md,a.md,b.md" {
		t.Errorf("want 3 distinct facts, got %v", res.Injected)
	}
}

// A project feedback the sidecar holds as stale (decayed before feedback was
// exempt) is not core; one close (reproject + decay) must bring it back and
// keep it — otherwise a core fact, refreshed only by being core, is lost.
func TestRun_StaleProjectFeedbackReentersCoreAfterClose(t *testing.T) {
	memDir, projDir, home := setup(t)
	writeFM(t, projDir, "rule.md", "name: rule\ntype: feedback\ncreated: 2025-01-01", "always lint")
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	runSS(t, memDir, home, "startup", "") // seeds the sidecar
	st := native.StoreFor(filepath.Join(home, ".claude"), "/tmp/proj")
	s, err := sidecar.Open(filepath.Join(memDir, ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(sidecar.Record{Slug: "rule.md", Project: st.Project, Type: "feedback",
		Created: "2025-01-01", Status: "stale"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if res := runSS(t, memDir, home, "startup", ""); len(res.Injected) != 0 {
		t.Fatalf("precondition: a stale fact is not core, got %v", res.Injected)
	}
	if _, err := closer.Run(closer.Input{SessionID: "s0", CWD: "/tmp/proj",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-11-24"}); err != nil {
		t.Fatal(err)
	}
	if res := runSS(t, memDir, home, "startup", ""); strings.Join(res.Injected, ",") != "rule.md" {
		t.Errorf("after one close the project feedback must be core again, got %v", res.Injected)
	}
}

// The Memory-queue line reserves its bytes before the core bodies too: a
// pointer that would fit without the reserve is dropped, the queue stays.
func TestRun_SessionStartQueueReservedBeforePointers(t *testing.T) {
	memDir, projDir, home := setup(t)
	writeFM(t, projDir, "continuity.md", "name: continuity\ntype: continuity\ncreated: 2026-11-23", strings.Repeat("c", 2400))
	rule := "---\nname: lint rule\ndescription: run make lint\ntype: feedback\ncreated: 2026-11-20\n---\n" + strings.Repeat("f", 2400) + "\n"
	os.WriteFile(filepath.Join(projDir, "rule.md"), []byte(rule), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	run := func(maxBytes int) Result {
		t.Helper()
		res, err := Run(Input{Event: "SessionStart", Source: "startup", SessionID: "s1", CWD: "/tmp/proj",
			ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-11-24", MaxK: 8, MaxBytes: maxBytes})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	noQueue := run(3000)
	if !strings.Contains(noQueue.Markdown, "- lint rule — ") {
		t.Fatalf("precondition: without a queue the pointer fits:\n%s", tail(noQueue.Markdown, 400))
	}
	budget := len(noQueue.Markdown)
	st := native.StoreFor(filepath.Join(home, ".claude"), "/tmp/proj")
	writeMark(t, memDir, st.Project, "rule", "review", native.BodySHA(rule))
	// The new mark must reach the sidecar's mark table: rebuild it.
	for _, sfx := range []string{"", "-wal", "-shm"} {
		os.Remove(filepath.Join(memDir, ".sidecar.db"+sfx))
	}
	res := run(budget)
	if !strings.Contains(res.Markdown, "Memory queue: 1 open task(s)") {
		t.Errorf("the queue line must survive:\n%s", tail(res.Markdown, 500))
	}
	if strings.Contains(res.Markdown, "- lint rule — ") {
		t.Errorf("the pointer only fits without the queue reserve:\n%s", tail(res.Markdown, 500))
	}
	if len(res.Markdown) > budget {
		t.Errorf("markdown %d bytes exceeds the %d budget", len(res.Markdown), budget)
	}
}

// SessionStart is always core-only, so it needs no query terms (no git
// calls, no overlap).
func TestNeedsTerms(t *testing.T) {
	for _, c := range []struct {
		in   Input
		want bool
	}{
		{Input{Event: "SessionStart", Source: "startup"}, false},
		{Input{Event: "SessionStart", Source: "resume"}, false},
		{Input{Event: "SessionStart", Source: "clear"}, false},
		{Input{Event: "SessionStart"}, false},
		{Input{Event: "SessionStart", Source: "compact"}, false},
		{Input{Event: "SessionStart", Source: "compact", Prompt: "summary"}, false},
		{Input{Event: "UserPromptSubmit", Prompt: "q"}, true},
		{Input{Event: "SubagentStart", Prompt: "q"}, true},
		{Input{Prompt: "q"}, true},
	} {
		if got := needsTerms(c.in); got != c.want {
			t.Errorf("needsTerms(%+v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// When the sidecar opens but cannot serve this scope (here: read-only, so
// the self-heal reproject fails) the degraded list still honours its marks.
func TestRun_DegradedWithOpenSidecarHonoursMarks(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read-only file mode")
	}
	memDir, projDir, home := setup(t)
	otherDir := filepath.Join(home, ".claude", "projects", "-tmp-other", "memory")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFM(t, otherDir, "o.md", "name: o\ntype: knowledge", "other")
	writeFM(t, projDir, "continuity.md", "name: continuity\ntype: continuity\ncreated: 2026-11-23", "next: ship")
	writeFM(t, projDir, "pin.md", "name: pin\ntype: project\nstatus: pinned\ncreated: 2026-11-20", "pinned body")
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	st := native.StoreFor(filepath.Join(home, ".claude"), "/tmp/proj")
	writeMark(t, memDir, st.Project, "pin", "hide", st.Project)
	// Another project's session builds the sidecar (rows for it only, the
	// mark table for everyone); then it turns read-only.
	if _, err := Run(Input{Event: "SessionStart", SessionID: "so", CWD: "/tmp/other",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-11-24"}); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(memDir, ".sidecar.db")
	if err := os.Chmod(db, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(db, 0o644) })
	res := runSS(t, memDir, home, "startup", "")
	if strings.Join(res.Injected, ",") != "continuity.md" {
		t.Errorf("degraded core must drop the hidden pinned fact, got %v", res.Injected)
	}
}
