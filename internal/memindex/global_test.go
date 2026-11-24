package memindex

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

func TestRenderGlobal_Layout(t *testing.T) {
	facts := []native.MemFile{
		{Slug: "tz.md", Name: "server-timezone", Description: "Prod servers run in UTC", Path: "/u/.claude/memory-global/tz.md"},
		{Slug: "mem.md", Name: "in-memory", Description: "Built without a path"},
	}
	out := RenderGlobal(facts, DefaultMaxBytes)

	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want header, intro and two entries, got %d lines:\n%s", len(lines), out)
	}
	if lines[0] != "# Global Memory Index" {
		t.Errorf("first line must be the generated-index header, got %q", lines[0])
	}
	if !IsGeneratedGlobal([]byte(out)) {
		t.Error("WriteGlobal must recognise RenderGlobal's output as its own")
	}
	if !strings.Contains(out, "- [server-timezone](/u/.claude/memory-global/tz.md) — Prod servers run in UTC\n") {
		t.Errorf("a fact with a path links by that absolute path:\n%s", out)
	}
	if !strings.Contains(out, "- [in-memory](mem.md) — Built without a path\n") {
		t.Errorf("a fact without a path falls back to its file name:\n%s", out)
	}
	if strings.Contains(Render(facts, DefaultMaxBytes), "/u/.claude/") {
		t.Error("the project index keeps bare file-name links")
	}
}

func TestRenderGlobal_StaysWithinBudget(t *testing.T) {
	var facts []native.MemFile
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		facts = append(facts, native.MemFile{Slug: s + ".md", Description: strings.Repeat(s, 60), Path: "/g/" + s + ".md"})
	}
	for _, budget := range []int{150, 300, 600} {
		if out := RenderGlobal(facts, budget); len(out) > budget {
			t.Errorf("budget %d: rendered %d bytes", budget, len(out))
		}
	}
}

func writeFact(t *testing.T, dir, file, front string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte("---\n"+front+"---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncGlobal(t *testing.T) {
	t.Run("writes the index of a non-empty store", func(t *testing.T) {
		dir := t.TempDir()
		writeFact(t, dir, "tz.md", "name: server-timezone\ndescription: Prod servers run in UTC\ntype: knowledge\n")
		done, err := SyncGlobal(dir)
		if err != nil {
			t.Fatalf("SyncGlobal: %v", err)
		}
		idx := filepath.Join(dir, "MEMORY.md")
		got, _ := os.ReadFile(idx)
		if done.Path != idx || done.Facts != 1 || done.Bytes != len(got) {
			t.Errorf("report %+v does not describe the %d-byte write to %s", done, len(got), idx)
		}
		if !strings.Contains(string(got), "]("+filepath.Join(dir, "tz.md")+")") {
			t.Errorf("index must link the fact by absolute path:\n%s", got)
		}
	})
	t.Run("empty store writes nothing", func(t *testing.T) {
		dir := t.TempDir()
		done, err := SyncGlobal(dir)
		if err != nil || done != (GlobalSync{}) {
			t.Errorf("want zero report and nil, got %+v, %v", done, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "MEMORY.md")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("no MEMORY.md may appear in an empty store (stat err %v)", err)
		}
	})
	t.Run("foreign index is kept", func(t *testing.T) {
		dir := t.TempDir()
		writeFact(t, dir, "tz.md", "name: server-timezone\ntype: knowledge\n")
		mine := "my own notes\n"
		os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(mine), 0o644)
		if _, err := SyncGlobal(dir); !errors.Is(err, ErrForeignIndex) {
			t.Errorf("want ErrForeignIndex, got %v", err)
		}
		if got, _ := os.ReadFile(filepath.Join(dir, "MEMORY.md")); string(got) != mine {
			t.Errorf("foreign index changed to:\n%s", got)
		}
	})
}
