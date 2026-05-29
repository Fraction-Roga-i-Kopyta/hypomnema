package memindex

import (
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
