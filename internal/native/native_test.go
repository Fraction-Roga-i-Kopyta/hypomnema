package native

import (
	"os"
	"path/filepath"
	"strings"
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

func TestList_TrailingWhitespaceClosingFence(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tw.md", "---\nname: Y\ntype: reference\n---  \nbody\n")
	files, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 1 || files[0].Name != "Y" || files[0].Body != "body" {
		t.Fatalf("trailing-space closing fence not parsed: %+v", files)
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
	if len(files) != 2 {
		t.Fatalf("expected 2 files (unterminated returned as body), got %d: %+v", len(files), files)
	}
	bySlug := map[string]MemFile{}
	for _, f := range files {
		bySlug[f.Slug] = f
	}
	if bySlug["good.md"].Name != "G" {
		t.Errorf("good.md Name = %q, want G", bySlug["good.md"].Name)
	}
	bad, ok := bySlug["bad.md"]
	if !ok {
		t.Fatal("bad.md missing — unterminated frontmatter must not drop the file")
	}
	if bad.Name != "" {
		t.Errorf("bad.md Name = %q, want empty (unterminated frontmatter)", bad.Name)
	}
	if !strings.Contains(bad.Body, "never closed") {
		t.Errorf("bad.md Body should contain raw content, got %q", bad.Body)
	}
}

func TestParse_RankingFrontmatterFields(t *testing.T) {
	dir := t.TempDir()
	content := "---\n" +
		"type: mistake\n" +
		"name: kube-probe\n" +
		"description: probes\n" +
		"created: 2026-06-01\n" +
		"status: pinned\n" +
		"keywords: [kubernetes, healthcheck, probe]\n" +
		"domains:\n" +
		"  - backend\n" +
		"  - devops\n" +
		"---\nbody text\n"
	if err := os.WriteFile(filepath.Join(dir, "kube.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Created != "2026-06-01" {
		t.Errorf("Created = %q, want 2026-06-01", f.Created)
	}
	if f.Status != "pinned" {
		t.Errorf("Status = %q, want pinned", f.Status)
	}
	if len(f.Keywords) != 3 || f.Keywords[0] != "kubernetes" || f.Keywords[2] != "probe" {
		t.Errorf("Keywords = %v, want [kubernetes healthcheck probe]", f.Keywords)
	}
	if len(f.Domains) != 2 || f.Domains[0] != "backend" || f.Domains[1] != "devops" {
		t.Errorf("Domains = %v (block-style list), want [backend devops]", f.Domains)
	}
}
