package native

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestList_ParsesFrontmatterAndSkipsIndex(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "feedback_x.md", "---\nname: Rule X\ndescription: do X\ntype: feedback\noriginSessionId: abc\n---\nBody text here.\n")
	writeFile(t, dir, "MEMORY.md", "# Memory Index\n- index, must be skipped\n")
	writeFile(t, dir, "notes.txt", "not markdown, skipped\n")

	files, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file (index + non-md skipped), got %d", len(files))
	}
	f := files[0]
	if f.Slug != "feedback_x.md" {
		t.Errorf("Slug = %q, want feedback_x.md", f.Slug)
	}
	if f.Name != "Rule X" || f.Description != "do X" || f.Type != "feedback" {
		t.Errorf("frontmatter = %+v", f)
	}
	if f.Body != "Body text here." {
		t.Errorf("Body = %q", f.Body)
	}
	if len(f.ContentSHA) != 64 {
		t.Errorf("ContentSHA len = %d, want 64 (hex sha256)", len(f.ContentSHA))
	}
}

func TestList_MissingDirIsEmpty(t *testing.T) {
	files, err := List(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("missing dir should be nil error, got %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestList_ToleratesBOMAndCRLFAndQuotes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.md", "\xef\xbb\xbf---\r\nname: \"Quoted\"\r\ntype: reference\r\n---\r\nbody\r\n")
	files, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 1 || files[0].Name != "Quoted" || files[0].Type != "reference" {
		t.Fatalf("BOM/CRLF/quote tolerance failed: %+v", files)
	}
}

func TestList_UnterminatedFrontmatterIsSkippedNotFatal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "good.md", "---\nname: G\ntype: reference\n---\nok\n")
	writeFile(t, dir, "bad.md", "---\nname: never closed\nbody with no end fence\n")
	files, err := List(dir)
	if err != nil {
		t.Fatalf("List should not error on a bad file: %v", err)
	}
	var haveGood bool
	for _, f := range files {
		if f.Slug == "good.md" && f.Name == "G" {
			haveGood = true
		}
	}
	if !haveGood {
		t.Fatalf("good.md missing from %+v", files)
	}
}
