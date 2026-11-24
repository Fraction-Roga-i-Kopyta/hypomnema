package memindex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// The orphaned v1 MEMORY.md linked to ../../../.claude/memory/<sub>/<slug>.md.
// Native files are flat siblings of MEMORY.md, so links must be bare slugs.
func TestRender_LinksPointToSiblingFiles(t *testing.T) {
	files := []native.MemFile{{
		Slug:        "real-db-in-tests.md",
		Name:        "real-db-in-tests",
		Description: "Integration tests hit a real DB",
		Type:        "feedback",
	}}

	out := Render(files, 24_000)

	if !strings.Contains(out, "](real-db-in-tests.md)") {
		t.Errorf("want sibling link ](real-db-in-tests.md); got:\n%s", out)
	}
	if strings.Contains(out, "../") {
		t.Errorf("index must not contain v1 relative paths (../); got:\n%s", out)
	}
}

// The orphaned index was 31KB; the harness truncates anything over ~24.4KB.
// Render must stay under budget and never emit a half-written line.
func TestRender_StaysUnderByteBudget(t *testing.T) {
	var files []native.MemFile
	for i := 0; i < 200; i++ {
		files = append(files, native.MemFile{
			Slug:        fmt.Sprintf("file-%03d.md", i),
			Name:        fmt.Sprintf("file-%03d", i),
			Description: strings.Repeat("длинное описание ", 20),
			Type:        "note",
		})
	}

	out := Render(files, 4000)

	if len(out) > 4000 {
		t.Errorf("output %d bytes exceeds budget 4000", len(out))
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output must end on a complete line; got tail %q", tail(out, 40))
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// The global store is not hypomnema's alone: a MEMORY.md the user or the
// model keeps there (pointers with hand-written descriptions) must survive
// the Stop hook. WriteGlobal replaces only an index it generated itself.
func TestWriteGlobal_LeavesForeignIndexUntouched(t *testing.T) {
	dir := t.TempDir()
	foreign := "- [creds](creds.md) — hand-written pointer\n"
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WriteGlobal(dir, "# Global Memory Index\nnew\n")
	if !errors.Is(err, ErrForeignIndex) {
		t.Errorf("want ErrForeignIndex, got %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "MEMORY.md")); string(got) != foreign {
		t.Errorf("foreign index must be left byte-identical; got:\n%s", got)
	}
}

func TestWriteGlobal_ReplacesOwnOrMissingIndex(t *testing.T) {
	for name, existing := range map[string]*string{
		"missing": nil,
		"own":     ptr("# Global Memory Index\nold\n"),
		"empty":   ptr(" \n"),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if existing != nil {
				os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(*existing), 0o644)
			}
			want := "# Global Memory Index\nnew\n"
			if err := WriteGlobal(dir, want); err != nil {
				t.Fatalf("WriteGlobal: %v", err)
			}
			if got, _ := os.ReadFile(filepath.Join(dir, "MEMORY.md")); string(got) != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func ptr(s string) *string { return &s }

// The index is cut at the byte/line budget, so the order decides which facts
// stay visible: pinned first, then the freshest (later of created/modified),
// slug as tie-break. A plain alphabetical listing silently dropped every fact
// past the budget's letter.
func TestRender_OrdersPinnedThenFreshest(t *testing.T) {
	files := []native.MemFile{
		{Slug: "a-old.md", Name: "a-old", Created: "2026-01-01"},
		{Slug: "b-new.md", Name: "b-new", Created: "2026-11-01"},
		{Slug: "c-pinned.md", Name: "c-pinned", Created: "2025-01-01", Status: "pinned"},
		{Slug: "d-edited.md", Name: "d-edited", Created: "2026-02-01", Modified: "2026-11-20"},
	}
	out := Render(files, DefaultMaxBytes)
	want := []string{"c-pinned", "d-edited", "b-new", "a-old"}
	last := -1
	for _, w := range want {
		i := strings.Index(out, "["+w+"]")
		if i < 0 || i < last {
			t.Fatalf("want order %v, got:\n%s", want, out)
		}
		last = i
	}
}

// A cut index says so: the model must know more facts exist and how to find
// them, and the note itself stays inside the budget.
func TestRender_TruncationNoteNamesHiddenCount(t *testing.T) {
	var files []native.MemFile
	for i := 0; i < 50; i++ {
		files = append(files, native.MemFile{
			Slug:        fmt.Sprintf("f-%02d.md", i),
			Name:        fmt.Sprintf("f-%02d", i),
			Description: strings.Repeat("x", 100),
		})
	}
	out := Render(files, 2000)
	if len(out) > 2000 {
		t.Fatalf("output %d bytes exceeds budget 2000", len(out))
	}
	shown := strings.Count(out, "\n- [")
	if strings.HasPrefix(out, "- [") {
		shown++
	}
	note := fmt.Sprintf("%d more not listed", 50-shown)
	if !strings.Contains(out, note) || !strings.Contains(out, "memoryctl recall") {
		t.Fatalf("want truncation note %q with a recall hint, got tail:\n%s", note, tail(out, 200))
	}
}

func TestRender_NoNoteWhenEverythingFits(t *testing.T) {
	out := Render([]native.MemFile{{Slug: "a.md", Name: "a"}}, DefaultMaxBytes)
	if strings.Contains(out, "not listed") {
		t.Fatalf("unexpected truncation note:\n%s", out)
	}
}

// The harness also stops reading MEMORY.md after 200 lines.
func TestRender_StaysUnderLineCap(t *testing.T) {
	var files []native.MemFile
	for i := 0; i < 400; i++ {
		files = append(files, native.MemFile{Slug: fmt.Sprintf("f%03d.md", i), Name: fmt.Sprintf("f%03d", i)})
	}
	out := Render(files, 1<<20)
	if n := strings.Count(out, "\n"); n > MaxLines {
		t.Fatalf("%d lines, cap %d", n, MaxLines)
	}
	if !strings.Contains(out, "more not listed") {
		t.Fatalf("line-capped index must carry the truncation note")
	}
}

// Write replaces <dir>/MEMORY.md with the rendered index. Atomic (I3): the
// close hook regenerates it every session, so a torn write must never leave a
// half-file the harness would inject.
func TestWrite_ReplacesMemoryMD(t *testing.T) {
	dir := t.TempDir()
	body := "# Memory Index\n- [x](x.md) — y\n"

	if err := Write(dir, body); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != body {
		t.Errorf("content mismatch:\nwant %q\ngot  %q", body, got)
	}
}
