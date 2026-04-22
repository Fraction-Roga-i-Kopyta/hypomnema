// Package tfidf reimplements hooks/memory-index.sh in Go. It scans
// memory/{mistakes,feedback,knowledge,strategies,notes}/*.md for files
// that carry no explicit `keywords:` frontmatter, tokenises their body,
// and emits a TSV index mapping tokens → per-file tf·idf scores.
//
// Why port it:
//   - The bash/awk tokenizer at hooks/memory-index.sh:55 uses the byte-
//     class `[^a-zA-Z\300-\377]` which keeps Latin + Latin-1 Supplement
//     and drops everything else. Cyrillic, Greek, CJK, Arabic bodies
//     get tokenised as mostly-empty strings and contribute nothing to
//     the index, so non-English records silently lose TF-IDF signal at
//     SessionStart. This file uses unicode.IsLetter / IsDigit so every
//     script that Unicode considers a letter participates.
//
// The bash implementation is kept as a silent fallback (the calling
// hook delegates to this Go binary when present and falls through to
// awk otherwise) — existing installs don't have to rebuild to benefit,
// but the Latin-only limitation applies until they do. The output TSV
// schema is identical in both paths.
package tfidf

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MinTokenRunes is the minimum UTF-8-rune length a token must reach to
// be kept. bash uses `length(w) < 3` which compares bytes — OK for
// ASCII, wrong for multi-byte scripts where "да" (2 Cyrillic letters =
// 4 bytes) passes the bash filter but has no semantic value. Compare
// by runes for consistency across scripts.
const MinTokenRunes = 3

// MinIDFKept drops terms whose inverse document frequency is too small
// to carry ranking signal — matches the `if (idf < 0.1) continue` in
// the bash awk. At 0.1, a token appearing in >93% of docs is dropped.
const MinIDFKept = 0.1

// indexSubdirs is the set of frontmatter directories the index covers,
// matching hooks/memory-index.sh:19 verbatim. `projects` / `decisions`
// are deliberately excluded — projects are invariant prose and
// decisions have explicit keywords in every template, so neither
// benefits from TF-IDF recall in practice.
var indexSubdirs = []string{"mistakes", "feedback", "knowledge", "strategies", "notes"}

// Tokenize turns free-form text into a slice of canonical lowercase
// tokens. `stopwords` may be nil; when provided, tokens in it are
// skipped. Length filter (MinTokenRunes) runs after stopword check.
func Tokenize(text string, stopwords map[string]struct{}) []string {
	split := func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			return r
		}
		return ' '
	}
	cleaned := strings.Map(split, text)
	fields := strings.Fields(cleaned)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		w := strings.ToLower(f)
		if utf8.RuneCountInString(w) < MinTokenRunes {
			continue
		}
		if stopwords != nil {
			if _, ok := stopwords[w]; ok {
				continue
			}
		}
		out = append(out, w)
	}
	return out
}

// LoadStopwords reads one-per-line stopwords from path. Blank lines
// skipped. A missing file is not an error — the bash implementation
// quietly proceeds with an empty stopword set in that case and this
// function mirrors it.
func LoadStopwords(path string) (map[string]struct{}, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]struct{}{}, nil
		}
		return nil, err
	}
	defer f.Close()
	sw := map[string]struct{}{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		sw[strings.ToLower(line)] = struct{}{}
	}
	return sw, sc.Err()
}

// doc is the per-file intermediate the rebuild pipeline passes between
// its two stages (parse → score).
type doc struct {
	slug string
	// termFreq is raw counts; divided by totalTokens to produce tf when
	// the index assembles. Keeping both separates parse errors (bad
	// frontmatter) from score errors (zero-length body).
	termFreq    map[string]int
	totalTokens int
}

// parseBody reads a single markdown file and returns either the parsed
// body + token frequencies, or (nil, nil) if the file should be skipped
// for any non-fatal reason: unclosed frontmatter, explicit `keywords:`
// field present (same skip as bash `if (has_kw) return`), empty body
// after tokenisation. Errors surface only for I/O problems.
func parseBody(path string, sw map[string]struct{}) (*doc, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		inFM     bool
		fmCount  int
		hasKw    bool
		bodyBuf  strings.Builder
		pastFM   bool
	)
	for sc.Scan() {
		line := sc.Text()
		// Handle both CRLF and LF.
		line = strings.TrimRight(line, "\r")
		if line == "---" {
			fmCount++
			switch fmCount {
			case 1:
				inFM = true
				continue
			case 2:
				inFM = false
				pastFM = true
				continue
			}
		}
		if inFM {
			if strings.HasPrefix(line, "keywords:") {
				hasKw = true
			}
			continue
		}
		if pastFM {
			bodyBuf.WriteString(line)
			bodyBuf.WriteByte(' ')
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// Unclosed frontmatter or file with explicit keywords: skip (bash
	// parity — those files get ranked by keyword score instead, so the
	// TF-IDF bonus would double-count).
	if fmCount < 2 || hasKw {
		return nil, nil
	}
	tokens := Tokenize(bodyBuf.String(), sw)
	if len(tokens) == 0 {
		return nil, nil
	}
	tf := map[string]int{}
	for _, t := range tokens {
		tf[t]++
	}
	slug := strings.TrimSuffix(filepath.Base(path), ".md")
	return &doc{slug: slug, termFreq: tf, totalTokens: len(tokens)}, nil
}

// Rebuild scans memoryDir for candidate files, builds a fresh TF-IDF
// index, and writes it to memoryDir/.tfidf-index atomically (tmp file
// + rename). Returns on first error; intermediate tmp files are
// cleaned up on failure.
func Rebuild(memoryDir string) error {
	sw, err := LoadStopwords(filepath.Join(memoryDir, ".stopwords"))
	if err != nil {
		return fmt.Errorf("tfidf: load stopwords: %w", err)
	}

	var paths []string
	for _, sub := range indexSubdirs {
		dir := filepath.Join(memoryDir, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("tfidf: read %s: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	if len(paths) == 0 {
		// Bash exits 0 with no output and leaves any previous index
		// untouched; match that behaviour.
		return nil
	}

	var docs []*doc
	for _, p := range paths {
		d, err := parseBody(p, sw)
		if err != nil {
			// Individual parse failures don't abort the rebuild — bash
			// swallows them via `|| true` on the outer pipeline. Skip
			// and keep going.
			continue
		}
		if d == nil {
			continue
		}
		docs = append(docs, d)
	}

	totalDocs := float64(len(paths))
	// Aggregate doc frequency per term + per-(term, slug) tf ratio.
	// termPostings[term] = []posting{slug, tf} — one per doc the term
	// appears in. Sorted lexicographically on emit to match bash `sort`.
	type posting struct {
		slug string
		tf   float64
	}
	termPostings := map[string][]posting{}
	for _, d := range docs {
		total := float64(d.totalTokens)
		for term, count := range d.termFreq {
			tfRatio := float64(count) / total
			termPostings[term] = append(termPostings[term], posting{d.slug, tfRatio})
		}
	}

	// Deterministic term order so the output file is byte-stable for
	// `make parity`. Bash pipes through `sort` which orders by the
	// whole line — but since the first field (term) is unique, sorting
	// terms alphabetically produces the same ordering.
	terms := make([]string, 0, len(termPostings))
	for t := range termPostings {
		terms = append(terms, t)
	}
	sort.Strings(terms)

	indexPath := filepath.Join(memoryDir, ".tfidf-index")
	tmp, err := os.CreateTemp(memoryDir, ".tfidf-index.*.tmp")
	if err != nil {
		return fmt.Errorf("tfidf: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	// Cleanup on error — successful rename leaves nothing to clean.
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	w := bufio.NewWriter(tmp)
	for _, term := range terms {
		postings := termPostings[term]
		idf := math.Log2(totalDocs / float64(len(postings)))
		if idf < MinIDFKept {
			continue
		}
		// Per-term file ordering — bash iterates `for (i=1; i<=n; i++)`
		// over `term_files[term]` which is filled in input-file order
		// (from `find` which is directory-order, undefined across
		// systems). Sort by slug here for stability — the consumer
		// treats each row as a set of postings anyway.
		sort.Slice(postings, func(i, j int) bool {
			return postings[i].slug < postings[j].slug
		})
		if _, err := fmt.Fprintf(w, "%s", term); err != nil {
			return fmt.Errorf("tfidf: write term %q: %w", term, err)
		}
		for _, p := range postings {
			if _, err := fmt.Fprintf(w, "\t%s:%.4f", p.slug, p.tf*idf); err != nil {
				return fmt.Errorf("tfidf: write posting: %w", err)
			}
		}
		if _, err := w.WriteString("\n"); err != nil {
			return fmt.Errorf("tfidf: newline: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("tfidf: flush: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("tfidf: close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, indexPath); err != nil {
		return fmt.Errorf("tfidf: rename to %s: %w", indexPath, err)
	}
	cleanup = false
	return nil
}

// RebuildWithWriter is used by tests and callers that want the output
// on an io.Writer instead of the canonical on-disk path (handy for
// fixture assertions without a full fs round-trip).
func RebuildWithWriter(memoryDir string, out io.Writer) error {
	// Tiny amount of duplication with Rebuild — factoring the shared
	// body into a helper is strictly worse here because the tmp-file
	// machinery has no meaning when the caller owns the writer. Keep
	// both; the arity difference makes the boundary obvious.
	sw, err := LoadStopwords(filepath.Join(memoryDir, ".stopwords"))
	if err != nil {
		return err
	}

	var paths []string
	for _, sub := range indexSubdirs {
		dir := filepath.Join(memoryDir, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}

	var docs []*doc
	for _, p := range paths {
		d, err := parseBody(p, sw)
		if err == nil && d != nil {
			docs = append(docs, d)
		}
	}

	type posting struct {
		slug string
		tf   float64
	}
	termPostings := map[string][]posting{}
	for _, d := range docs {
		total := float64(d.totalTokens)
		for term, count := range d.termFreq {
			termPostings[term] = append(termPostings[term],
				posting{d.slug, float64(count) / total})
		}
	}
	terms := make([]string, 0, len(termPostings))
	for t := range termPostings {
		terms = append(terms, t)
	}
	sort.Strings(terms)

	totalDocs := float64(len(paths))
	bw := bufio.NewWriter(out)
	for _, term := range terms {
		postings := termPostings[term]
		idf := math.Log2(totalDocs / float64(len(postings)))
		if idf < MinIDFKept {
			continue
		}
		sort.Slice(postings, func(i, j int) bool {
			return postings[i].slug < postings[j].slug
		})
		fmt.Fprintf(bw, "%s", term)
		for _, p := range postings {
			fmt.Fprintf(bw, "\t%s:%.4f", p.slug, p.tf*idf)
		}
		bw.WriteString("\n")
	}
	return bw.Flush()
}
