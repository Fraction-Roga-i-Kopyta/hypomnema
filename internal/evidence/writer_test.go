package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectPhrases_AppendsToExistingBlock(t *testing.T) {
	src := `---
type: feedback
evidence:
  - "already here"
  - "second one"
---

Body.
`
	got, err := injectPhrases(src, []string{"new phrase", "another new"})
	if err != nil {
		t.Fatal(err)
	}
	// Existing phrases preserved verbatim (not reformatted).
	if !strings.Contains(got, `  - "already here"`) {
		t.Errorf("original phrase lost:\n%s", got)
	}
	// New phrases inserted inside the block, before the closing `---`.
	if !strings.Contains(got, `  - "new phrase"`) {
		t.Errorf("new phrase not inserted:\n%s", got)
	}
	// Frontmatter still well-formed — "Body." is after two `---`.
	delims := strings.Count(got, "\n---\n")
	if delims == 0 && !strings.HasPrefix(got, "---\n") {
		t.Errorf("frontmatter delimiter damaged:\n%s", got)
	}
}

func TestInjectPhrases_CreatesBlockWhenMissing(t *testing.T) {
	src := `---
type: mistake
status: active
---

Body.
`
	got, err := injectPhrases(src, []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "evidence:\n") {
		t.Errorf("expected new evidence: header:\n%s", got)
	}
	if !strings.Contains(got, `  - "first"`) || !strings.Contains(got, `  - "second"`) {
		t.Errorf("phrases missing:\n%s", got)
	}
	// Block must be before the closing `---`.
	first := strings.Index(got, "---\n")
	closing := strings.LastIndex(got, "---\n")
	blockIdx := strings.Index(got, "evidence:")
	if !(first < blockIdx && blockIdx < closing) {
		t.Errorf("evidence block not inside frontmatter:\n%s", got)
	}
}

func TestInjectPhrases_SkipsDuplicatesCaseInsensitive(t *testing.T) {
	src := `---
evidence:
  - "Already Listed"
---
`
	got, err := injectPhrases(src, []string{"already listed", "truly new"})
	if err != nil {
		t.Fatal(err)
	}
	// "already listed" (any case) must not be duplicated.
	if strings.Count(strings.ToLower(got), "already listed") != 1 {
		t.Errorf("duplicate appended:\n%s", got)
	}
	if !strings.Contains(got, `  - "truly new"`) {
		t.Errorf("new phrase missing:\n%s", got)
	}
}

func TestInjectPhrases_NoFrontmatterFails(t *testing.T) {
	src := "no frontmatter in this file\njust body\n"
	_, err := injectPhrases(src, []string{"x"})
	if err == nil {
		t.Error("expected error on file without frontmatter")
	}
}

func TestInjectPhrases_EmptyPhrasesNoOp(t *testing.T) {
	src := "---\ntype: feedback\n---\n"
	got, err := injectPhrases(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Errorf("empty input should be no-op, got:\n%s", got)
	}
}

func TestInjectPhrases_EscapesEmbeddedQuotes(t *testing.T) {
	src := "---\nevidence:\n  - \"existing\"\n---\n"
	got, err := injectPhrases(src, []string{`phrase with "inner" quote`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `\"inner\"`) {
		t.Errorf("inner quotes not escaped:\n%s", got)
	}
}

func TestAppendToFrontmatter_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rule.md")
	src := "---\ntype: feedback\nevidence:\n  - \"first\"\n---\nbody\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AppendToFrontmatter(path, []string{"second"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `  - "second"`) {
		t.Errorf("AppendToFrontmatter didn't persist:\n%s", data)
	}
	// No leftover tmp files.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".evidence-append.") {
			t.Errorf("temp file leaked: %s", e.Name())
		}
	}
}

// --- rejection list tests ---

func TestRejections_LoadAndAppendRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := RejectionsPath(dir, "some-slug")
	if err := AppendRejection(path, "Phrase One"); err != nil {
		t.Fatal(err)
	}
	if err := AppendRejection(path, "second phrase"); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRejections(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["phrase one"]; !ok {
		t.Errorf("first rejection not loaded (should be lowercased): %v", got)
	}
	if _, ok := got["second phrase"]; !ok {
		t.Errorf("second rejection not loaded: %v", got)
	}
}

func TestRejections_MissingFileIsEmpty(t *testing.T) {
	got, err := LoadRejections(filepath.Join(t.TempDir(), "nope.list"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("missing file should yield empty set, got %v", got)
	}
}
