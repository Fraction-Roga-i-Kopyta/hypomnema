package tfidf

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestTokenize_LatinBasic pins the original bash behaviour on Latin
// input so a regression on the Go tokeniser is impossible to miss.
func TestTokenize_LatinBasic(t *testing.T) {
	got := Tokenize("The quick BROWN-fox (jumped). over-the-lazy dog.",
		map[string]struct{}{"the": {}, "over": {}})
	want := []string{"quick", "brown", "fox", "jumped", "lazy", "dog"}
	assertTokens(t, got, want)
}

// TestTokenize_CyrillicIncluded is the whole point of the Go port —
// the bash `gsub(/[^a-zA-Z\300-\377]/, " ")` regex corrupts Cyrillic
// input. This test pins that unicode.IsLetter-based split produces
// real tokens instead.
func TestTokenize_CyrillicIncluded(t *testing.T) {
	got := Tokenize("База данных медленно работает при большой нагрузке — проверь индексы.", nil)
	want := []string{"база", "данных", "медленно", "работает", "при", "большой", "нагрузке", "проверь", "индексы"}
	assertTokens(t, got, want)
}

// TestTokenize_MixedScriptsSurvive covers the realistic "English
// stacktrace plus Russian commentary" file — both sides must tokenise.
func TestTokenize_MixedScriptsSurvive(t *testing.T) {
	got := Tokenize("SQLAlchemy metadata reserved — атрибут конфликтует с Base.metadata.", nil)
	latin := []string{"sqlalchemy", "metadata", "reserved"}
	cyrillic := []string{"атрибут", "конфликтует", "base", "metadata"}
	for _, w := range append(latin, cyrillic...) {
		if !contains(got, w) {
			t.Errorf("expected token %q in output, got %v", w, got)
		}
	}
}

// TestTokenize_ShortTokensDroppedByRune ensures "да" (2 Cyrillic runes
// / 4 bytes) is filtered by rune count, not byte count — otherwise
// short Cyrillic words sneak through the MinTokenRunes=3 gate.
func TestTokenize_ShortTokensDroppedByRune(t *testing.T) {
	got := Tokenize("да нет ok привет", nil)
	// "да" (2), "нет" (3), "ok" (2), "привет" (6) -> "нет" and "привет"
	// pass the >= 3 rune filter.
	want := []string{"нет", "привет"}
	assertTokens(t, got, want)
}

func TestTokenize_CJKBasic(t *testing.T) {
	got := Tokenize("データベースが遅い — check indexes.", nil)
	// Japanese string tokenises as one run since there's no whitespace
	// between characters — that's correct for our purpose (we're
	// looking for content signal, not morphology).
	for _, w := range []string{"データベースが遅い", "check", "indexes"} {
		if !contains(got, w) {
			t.Errorf("expected %q in %v", w, got)
		}
	}
}

func TestTokenize_Stopwords(t *testing.T) {
	sw := map[string]struct{}{"the": {}, "and": {}, "это": {}}
	got := Tokenize("the cat AND the dog это тест", sw)
	// "это" (3 runes) passes length filter but must be dropped by stopword.
	for _, bad := range []string{"the", "and", "это"} {
		if contains(got, bad) {
			t.Errorf("stopword %q leaked into output: %v", bad, got)
		}
	}
	for _, good := range []string{"cat", "dog", "тест"} {
		if !contains(got, good) {
			t.Errorf("expected %q in %v", good, got)
		}
	}
}

// newFixture builds a minimal memoryDir with a few tokenisable files
// under mistakes/ for Rebuild integration tests.
func newFixture(t *testing.T) string {
	t.Helper()
	mem := t.TempDir()
	for _, s := range indexSubdirs {
		if err := os.MkdirAll(filepath.Join(mem, s), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return mem
}

// writeFile seeds a memory file with the given frontmatter + body.
func writeFile(t *testing.T, mem, sub, slug, frontmatter, body string) {
	t.Helper()
	contents := "---\n" + frontmatter + "---\n\n" + body + "\n"
	p := filepath.Join(mem, sub, slug+".md")
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRebuild_SkipsFilesWithKeywords(t *testing.T) {
	mem := newFixture(t)
	// File with keywords: MUST be skipped — bash parity (`if (has_kw) return`).
	writeFile(t, mem, "mistakes", "has-keywords",
		"type: mistake\nkeywords: [foo, bar]\n",
		"connection timeout when querying the external API")
	// File without keywords: SHOULD index.
	writeFile(t, mem, "mistakes", "no-keywords",
		"type: mistake\n",
		"missing readme instructions during onboarding")

	var buf bytes.Buffer
	if err := RebuildWithWriter(mem, &buf); err != nil {
		t.Fatal(err)
	}
	index := buf.String()
	if strings.Contains(index, "has-keywords") {
		t.Errorf("file with keywords must be excluded from TF-IDF, got:\n%s", index)
	}
	// One doc, tf·idf collapses to log2(1/1) = 0 — filtered by MinIDFKept.
	// That's fine; the point is that `has-keywords` never appeared.
}

func TestRebuild_CyrillicBodyProducesRealTokens(t *testing.T) {
	mem := newFixture(t)
	// Two distinct bodies so IDF > 0.1 for most tokens.
	writeFile(t, mem, "mistakes", "pg-slow",
		"type: mistake\n",
		"База данных postgres медленно работает при большой нагрузке.")
	writeFile(t, mem, "mistakes", "docker-cache",
		"type: mistake\n",
		"Docker layer cache preserves stale compiled artefacts between builds.")
	writeFile(t, mem, "mistakes", "migration-fail",
		"type: mistake\n",
		"Migration failed because the production schema diverged from staging.")

	var buf bytes.Buffer
	if err := RebuildWithWriter(mem, &buf); err != nil {
		t.Fatal(err)
	}
	index := buf.String()
	// Tokens unique to the Cyrillic file must appear in the index —
	// the whole justification for the Go port.
	for _, term := range []string{"база", "медленно", "нагрузке"} {
		if !strings.Contains(index, term+"\t") && !strings.HasPrefix(index, term+"\t") {
			t.Errorf("expected Cyrillic term %q in index, not found in:\n%s", term, index)
		}
	}
}

func TestRebuild_TermsSortedDeterministically(t *testing.T) {
	mem := newFixture(t)
	writeFile(t, mem, "mistakes", "a-one", "type: mistake\n", "zebra apple middle kilo")
	writeFile(t, mem, "mistakes", "b-two", "type: mistake\n", "kilo lemma nova oscar")
	writeFile(t, mem, "mistakes", "c-three", "type: mistake\n", "oscar papa quest regex")

	var buf bytes.Buffer
	if err := RebuildWithWriter(mem, &buf); err != nil {
		t.Fatal(err)
	}
	var terms []string
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		terms = append(terms, strings.SplitN(line, "\t", 2)[0])
	}
	sorted := append([]string(nil), terms...)
	sort.Strings(sorted)
	if strings.Join(terms, ",") != strings.Join(sorted, ",") {
		t.Errorf("terms must be emitted in sorted order for parity; got %v, want %v", terms, sorted)
	}
}

// TestRebuild_AtomicFileReplacement checks Rebuild (the on-disk
// variant) writes via tmp+rename and leaves no .tmp residue on success.
func TestRebuild_AtomicFileReplacement(t *testing.T) {
	mem := newFixture(t)
	writeFile(t, mem, "mistakes", "a", "type: mistake\n", "alpha beta gamma")
	writeFile(t, mem, "mistakes", "b", "type: mistake\n", "alpha delta epsilon")

	if err := Rebuild(mem); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mem, ".tfidf-index")); err != nil {
		t.Errorf(".tfidf-index should exist after Rebuild: %v", err)
	}
	// No lingering tmp files.
	entries, _ := os.ReadDir(mem)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tfidf-index.") && strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("tmp file should be cleaned up, found: %s", e.Name())
		}
	}
}

func TestRebuild_EmptyMemoryDirIsNoop(t *testing.T) {
	mem := newFixture(t)
	// Empty dirs — no .md files.
	if err := Rebuild(mem); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mem, ".tfidf-index")); !os.IsNotExist(err) {
		t.Errorf("empty memory should not produce a .tfidf-index, stat err: %v", err)
	}
}

func TestRebuild_UnclosedFrontmatterSkipped(t *testing.T) {
	mem := newFixture(t)
	// Only opening `---`, no closing — file has no body by the parser
	// definition, bash skips it the same way.
	p := filepath.Join(mem, "mistakes", "broken.md")
	if err := os.WriteFile(p,
		[]byte("---\ntype: mistake\n\nbody without closing frontmatter\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, mem, "mistakes", "ok", "type: mistake\n", "good content here")

	var buf bytes.Buffer
	if err := RebuildWithWriter(mem, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "broken") {
		t.Errorf("unclosed-frontmatter file must not appear in index, got:\n%s",
			buf.String())
	}
}

func TestRebuild_StopwordsFileHonoured(t *testing.T) {
	mem := newFixture(t)
	// Seed stopwords that include a term we'll use in bodies. Need
	// multi-doc so idf > 0 (otherwise everything drops anyway).
	if err := os.WriteFile(filepath.Join(mem, ".stopwords"),
		[]byte("stopped\nanother\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, mem, "mistakes", "a", "type: mistake\n",
		"stopped at boundary with visible content")
	writeFile(t, mem, "mistakes", "b", "type: mistake\n",
		"another case with boundary and words")
	writeFile(t, mem, "mistakes", "c", "type: mistake\n",
		"third case no stopwords present here")

	var buf bytes.Buffer
	if err := RebuildWithWriter(mem, &buf); err != nil {
		t.Fatal(err)
	}
	for _, sw := range []string{"stopped", "another"} {
		// Lines are "term\tslug:score..."; look for the term at start-of-line.
		for _, line := range strings.Split(buf.String(), "\n") {
			if strings.HasPrefix(line, sw+"\t") {
				t.Errorf("stopword %q must not appear as index term, line: %q", sw, line)
			}
		}
	}
}

// --- helpers ---

func assertTokens(t *testing.T, got, want []string) {
	t.Helper()
	if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
		t.Errorf("tokens mismatch\n  got:  %v\n  want: %v", got, want)
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
