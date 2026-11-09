package inject

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// writeMark seeds a mark WAL row before the first Run, so the sidecar
// self-heal replays it.
func writeMark(t *testing.T, memDir, factProject, stem, directive, arg string) {
	t.Helper()
	target := native.QKey(factProject, stem) + ":" + directive
	if arg != "" {
		target += ":" + arg
	}
	f, err := os.OpenFile(filepath.Join(memDir, ".wal"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("2026-05-28|mark|" + target + "|cli\n"); err != nil {
		t.Fatal(err)
	}
}

func markFixture(t *testing.T) (memDir, home string) {
	t.Helper()
	memDir, _, home = setup(t)
	globalDir := filepath.Join(home, ".claude", "memory-global")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: gfact\ntype: knowledge\ndescription: docker cache\nstatus: active\nkeywords: [docker]\n---\ndocker cache layer\n"
	if err := os.WriteFile(filepath.Join(globalDir, "gfact.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return memDir, home
}

func injectFor(t *testing.T, memDir, home, cwd, event string) []string {
	t.Helper()
	res, err := Run(Input{
		Event: event, SessionID: "s-" + cwd, CWD: cwd, Prompt: "docker cache",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 5,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res.Injected
}

func hasSlug(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestInject_HideOnlyInMarkedProject(t *testing.T) {
	memDir, home := markFixture(t)
	st := native.StoreFor(filepath.Join(home, ".claude"), "/tmp/proj")
	writeMark(t, memDir, native.GlobalProject, "gfact", "hide", st.Project)

	if got := injectFor(t, memDir, home, "/tmp/proj", "UserPromptSubmit"); hasSlug(got, "gfact.md") {
		t.Fatalf("fact hidden for %s still injected: %v", st.Project, got)
	}
	if got := injectFor(t, memDir, home, "/tmp/other", "UserPromptSubmit"); !hasSlug(got, "gfact.md") {
		t.Fatalf("hide for another project must not apply, got %v", got)
	}
}

func TestInject_RecallIgnoresMarks(t *testing.T) {
	memDir, home := markFixture(t)
	st := native.StoreFor(filepath.Join(home, ".claude"), "/tmp/proj")
	writeMark(t, memDir, native.GlobalProject, "gfact", "hide", st.Project)
	writeMark(t, memDir, native.GlobalProject, "gfact", "less", "")
	files := native.Collect(filepath.Join(home, ".claude"), st)
	var found bool
	for _, c := range Candidates(memDir, st, files, []string{"docker"}) {
		if c.Slug == "gfact.md" {
			found = true
			if c.Hidden || c.Less {
				t.Fatalf("Candidates must ignore marks: %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("gfact.md missing from Candidates")
	}
}
