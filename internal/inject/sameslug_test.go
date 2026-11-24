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
	if p := res.ProjectBySlug["x.md"]; p == "" || p == native.GlobalProject {
		t.Errorf("ProjectBySlug[x.md] = %q, want the project tag", p)
	}

	res = run(t, "redis", "docker, cache")
	if !strings.Contains(res.Markdown, "GLOBAL BODY") || strings.Contains(res.Markdown, "PROJECT BODY") {
		t.Errorf("only the global fact matches; its body must render:\n%s", res.Markdown)
	}
	if p := res.ProjectBySlug["x.md"]; p != native.GlobalProject {
		t.Errorf("ProjectBySlug[x.md] = %q, want %q", p, native.GlobalProject)
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
