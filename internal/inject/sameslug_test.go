package inject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/rank"
)

// A project fact and a global fact sharing a basename are two facts: the body
// rendered and the store attributed must come from the same one, the
// project-local on a tie.
func TestRun_SameSlugProjectAndGlobal(t *testing.T) {
	run := func(t *testing.T, projKW, globKW string) Result {
		t.Helper()
		memDir, projDir, home := setup(t)
		globDir := filepath.Join(home, ".claude", "memory-global")
		if err := os.MkdirAll(globDir, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(projDir, "x.md"),
			[]byte("---\nname: x\ntype: knowledge\nkeywords: ["+projKW+"]\n---\nPROJECT BODY\n"), 0o644)
		os.WriteFile(filepath.Join(globDir, "x.md"),
			[]byte("---\nname: x\ntype: knowledge\nkeywords: ["+globKW+"]\n---\nGLOBAL BODY\n"), 0o644)
		os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
		res, err := Run(Input{
			Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj",
			Prompt:     "docker cache",
			ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
			Today: "2026-05-29", MaxK: 8,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return res
	}

	res := run(t, "docker, cache", "docker, cache")
	if !strings.Contains(res.Markdown, "PROJECT BODY") || strings.Contains(res.Markdown, "GLOBAL BODY") {
		t.Errorf("project body must win a basename tie:\n%s", res.Markdown)
	}
	if len(res.Injected) != 1 || res.Injected[0] != "x.md" {
		t.Errorf("Injected = %v, want [x.md]", res.Injected)
	}
	if len(res.InjectedKeys) != 1 || res.InjectedKeys[0] != native.QKey("-tmp-proj", "x.md") {
		t.Errorf("InjectedKeys = %q, want the project-qualified x.md", res.InjectedKeys)
	}

	res = run(t, "redis", "docker, cache")
	if !strings.Contains(res.Markdown, "GLOBAL BODY") || strings.Contains(res.Markdown, "PROJECT BODY") {
		t.Errorf("only the global fact matches; its body must render:\n%s", res.Markdown)
	}
	if len(res.InjectedKeys) != 1 || res.InjectedKeys[0] != native.QKey(native.GlobalProject, "x.md") {
		t.Errorf("InjectedKeys = %q, want the global x.md", res.InjectedKeys)
	}
}

// AlreadyInjected names facts, not file names: global x.md in context does
// not hide project x.md; a bare line (older binary) hides both.
func TestRun_AlreadyInjectedByQKey(t *testing.T) {
	run := func(t *testing.T, already []string) Result {
		t.Helper()
		memDir, projDir, home := setup(t)
		globDir := filepath.Join(home, ".claude", "memory-global")
		os.MkdirAll(globDir, 0o755)
		os.WriteFile(filepath.Join(projDir, "x.md"),
			[]byte("---\nname: x\ntype: knowledge\nkeywords: [docker]\n---\nPROJECT BODY\n"), 0o644)
		os.WriteFile(filepath.Join(globDir, "x.md"),
			[]byte("---\nname: x\ntype: knowledge\nkeywords: [docker]\n---\nGLOBAL BODY\n"), 0o644)
		os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
		res, err := Run(Input{
			Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj", Prompt: "docker",
			ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
			Today: "2026-05-29", MaxK: 8, AlreadyInjected: already,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return res
	}
	res := run(t, []string{native.QKey(native.GlobalProject, "x.md")})
	if len(res.InjectedKeys) != 1 || !strings.HasSuffix(res.InjectedKeys[0], "x.md") ||
		strings.HasPrefix(res.InjectedKeys[0], native.GlobalProject) || !strings.Contains(res.Markdown, "PROJECT BODY") {
		t.Errorf("project x.md must render beside global x.md in context: keys=%q\n%s", res.InjectedKeys, res.Markdown)
	}
	if res = run(t, []string{"x.md"}); len(res.InjectedKeys) != 0 {
		t.Errorf("a bare line hides the file name in every store: keys=%q", res.InjectedKeys)
	}
}

func TestDedupBySlug(t *testing.T) {
	sc := func(project, slug string) rank.Scored {
		return rank.Scored{Candidate: rank.Candidate{Project: project, Slug: slug}}
	}
	in := []rank.Scored{sc(native.GlobalProject, "x.md"), sc("p", "y.md"), sc("p", "x.md")}
	got := dedupBySlug(in, "p")
	if len(got) != 2 || got[0].Slug != "x.md" || got[0].Project != "p" || got[1].Slug != "y.md" {
		t.Errorf("dedupBySlug = %+v, want project x.md then y.md", got)
	}
}

// The already-in-context filter runs before the K cut: a global x.md in
// context must not lend its top position to the project x.md (which then
// competes on its own score), and a fact already in context must not hold a
// K slot a fresh fact could take.
func TestRun_AlreadyInjectedFilteredBeforeKCut(t *testing.T) {
	memDir, projDir, home := setup(t)
	globDir := filepath.Join(home, ".claude", "memory-global")
	os.MkdirAll(globDir, 0o755)
	os.WriteFile(filepath.Join(globDir, "x.md"),
		[]byte("---\nname: x\ntype: knowledge\nkeywords: [docker, cache, redis]\n---\nGLOBAL BODY\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "x.md"),
		[]byte("---\nname: x\ntype: knowledge\nkeywords: [docker]\n---\nPROJECT BODY\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "y.md"),
		[]byte("---\nname: y\ntype: knowledge\nkeywords: [docker, cache]\n---\nY BODY\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	run := func(already []string, k int) Result {
		res, err := Run(Input{
			Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj",
			Prompt:     "docker cache redis",
			ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
			Today: "2026-05-29", MaxK: k,
			AlreadyInjected: already,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return res
	}
	res := run([]string{native.QKey(native.GlobalProject, "x.md")}, 1)
	if strings.Contains(res.Markdown, "PROJECT BODY") || !strings.Contains(res.Markdown, "Y BODY") {
		t.Errorf("project x.md must compete on its own (lower) score and lose the single slot to y.md:\n%s", res.Markdown)
	}

}

// A fact ranked K+1 renders when one of the top-K is already in context.
func TestRun_AlreadyInjectedFreesKSlot(t *testing.T) {
	memDir, projDir, home := setup(t)
	for i, n := range []string{"a", "b", "c"} {
		kw := []string{"docker, cache, redis", "docker, cache", "docker"}[i]
		os.WriteFile(filepath.Join(projDir, n+".md"),
			[]byte("---\nname: "+n+"\ntype: knowledge\nkeywords: ["+kw+"]\n---\nBODY "+n+"\n"), 0o644)
	}
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	res, err := Run(Input{
		Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj",
		Prompt:     "docker cache redis",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 2,
		AlreadyInjected: []string{native.QKey("-tmp-proj", "a.md")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Markdown, "BODY b") || !strings.Contains(res.Markdown, "BODY c") || strings.Contains(res.Markdown, "BODY a") {
		t.Errorf("want b and c rendered (a in context), got:\n%s", res.Markdown)
	}
}
