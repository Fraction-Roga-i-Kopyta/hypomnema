# v2.0 Phase 2a — Ranker + keyword population Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the relevance ranker — the merged injection pipeline's core — as a pure, testable Go function, plus the keyword index it consumes, plus a diagnostic verb to exercise it.

**Architecture:** Three additive units. `internal/tokenize` (Unicode tokenizer, salvaged from `internal/tfidf` so the doomed tfidf-scoring package can later be removed without losing tokenization). `internal/sidecar` gains keyword-table population (folded into Reproject) + an overlap query. `internal/rank` is a pure, I/O-free leaf package: `(Query, []Candidate, k) → top-K Scored`. A `memoryctl rank` diagnostic verb wires them. The A/B null-hypothesis harness is Phase 2b (separate plan, built on the real ranker).

**Tech Stack:** Go 1.25, `modernc.org/sqlite`, stdlib `testing`. Module `github.com/Fraction-Roga-i-Kopyta/hypomnema`.

**Scope boundary:** No per-type quotas in the ranker yet — `Rank` returns top-K by score; quota/flex-pool policy is an inject-time concern (Phase 3). Stopwords are not applied to keyword population in 2a (pass nil) — revisit if overlap proves noisy. Scoring weights are fixed constants here; Phase 2b's A/B harness calibrates them.

---

### Task 1: `internal/tokenize` — salvage the Unicode tokenizer

**Files:**
- Create: `internal/tokenize/tokenize.go`
- Test: `internal/tokenize/tokenize_test.go`

**Context:** `internal/tfidf.Tokenize` is Unicode-aware (uses `unicode.IsLetter`/`IsDigit`, rune-length filter) — critical for the Cyrillic corpus. The TF-IDF *scoring* is dropped in v2, but the *tokenizer* is reused by the ranker (`internal/rank`) and keyword population (`internal/sidecar`). Extract it into a neutral leaf package so neither has to import the to-be-deleted `tfidf` package. Copy the logic verbatim from `internal/tfidf/tfidf.go:35-106`.

- [ ] **Step 1: Write the failing test** `internal/tokenize/tokenize_test.go`

```go
package tokenize

import (
	"path/filepath"
	"os"
	"testing"
)

func TestTokenize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"ascii lowercased", "Hello World", []string{"hello", "world"}},
		{"punctuation splits", "foo,bar.baz", []string{"foo", "bar", "baz"}},
		{"min rune filter drops short", "a ab abc abcd", []string{"abc", "abcd"}},
		{"cyrillic kept (rune length, not bytes)", "база данных", []string{"база", "данных"}},
		{"underscore is part of token", "snake_case", []string{"snake_case"}},
		{"digits kept", "go125 ok", []string{"go125"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Tokenize(tc.in, nil)
			if len(got) != len(tc.want) {
				t.Fatalf("Tokenize(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("Tokenize(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestTokenizeStopwords(t *testing.T) {
	sw := map[string]struct{}{"the": {}, "and": {}}
	got := Tokenize("the quick and brown", sw)
	want := []string{"quick", "brown"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("with stopwords = %v, want %v", got, want)
	}
}

func TestLoadStopwords(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sw")
	if err := os.WriteFile(p, []byte("The\n\nAnd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sw, err := LoadStopwords(p)
	if err != nil {
		t.Fatalf("LoadStopwords: %v", err)
	}
	if _, ok := sw["the"]; !ok {
		t.Error("expected lowercased 'the' in stopwords")
	}
	if _, ok := sw["and"]; !ok {
		t.Error("expected 'and' in stopwords")
	}
	// missing file is not an error
	sw2, err := LoadStopwords(filepath.Join(dir, "nope"))
	if err != nil || len(sw2) != 0 {
		t.Errorf("missing stopword file: got len=%d err=%v, want 0/nil", len(sw2), err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tokenize/ -v`
Expected: FAIL — `undefined: Tokenize`.

- [ ] **Step 3: Write minimal implementation** `internal/tokenize/tokenize.go`

```go
// Package tokenize turns free-form text into canonical lowercase tokens,
// Unicode-aware (unicode.IsLetter/IsDigit) so every script — Cyrillic, CJK,
// Greek — participates, not just Latin. Salvaged from internal/tfidf so the
// ranker and keyword population can tokenize without depending on the
// TF-IDF scoring package (dropped in v2).
package tokenize

import (
	"bufio"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MinTokenRunes is the minimum UTF-8-rune length a token must reach to be
// kept. Compared by runes (not bytes) for consistency across scripts.
const MinTokenRunes = 3

// Tokenize lowercases and splits text on any non-letter/digit/underscore
// rune, dropping tokens shorter than MinTokenRunes and any in stopwords
// (which may be nil).
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

// LoadStopwords reads one-per-line stopwords from path (blank lines skipped,
// lowercased). A missing file is not an error — returns an empty set.
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tokenize/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tokenize/
git commit -m "feat(tokenize): Unicode tokenizer extracted from tfidf for reuse"
```

---

### Task 2: `internal/sidecar` — keyword population + overlap query

**Files:**
- Create: `internal/sidecar/keyword.go`
- Modify: `internal/sidecar/reproject.go` (call PopulateKeywords at the end of Reproject)
- Test: `internal/sidecar/keyword_test.go`

**Context:** The `keyword` table (slug, term, weight) was created empty in Phase 1. Populate it from each native file's name+description+body, weight = term frequency. Fold the call into `Reproject` so a rebuild fills it. Add `OverlapScores` for the ranker: given query terms, return per-slug count of distinct matching terms.

- [ ] **Step 1: Write the failing test** `internal/sidecar/keyword_test.go`

```go
package sidecar

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

func TestPopulateKeywordsAndOverlap(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	if err := os.WriteFile(walPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, ".sidecar.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	files := []native.MemFile{
		{Slug: "a.md", Name: "Docker cache", Description: "buildkit layer", Body: "docker docker layer", ContentSHA: "a"},
		{Slug: "b.md", Name: "SQL tuning", Description: "index plan", Body: "postgres index", ContentSHA: "b"},
	}
	// Reproject must populate keywords as part of a rebuild.
	if err := Reproject(s, files, walPath); err != nil {
		t.Fatalf("Reproject: %v", err)
	}

	// "docker" appears in a.md (multiple times → weight>1, but overlap counts
	// distinct terms). "index" appears in b.md.
	scores, err := s.OverlapScores([]string{"docker", "index", "missing"})
	if err != nil {
		t.Fatalf("OverlapScores: %v", err)
	}
	if scores["a.md"] != 1 {
		t.Errorf("a.md overlap = %d, want 1 (docker)", scores["a.md"])
	}
	if scores["b.md"] != 1 {
		t.Errorf("b.md overlap = %d, want 1 (index)", scores["b.md"])
	}

	// A query term in both → both slugs; query matching two distinct terms in
	// one slug → overlap 2.
	scores2, err := s.OverlapScores([]string{"layer", "docker"})
	if err != nil {
		t.Fatalf("OverlapScores 2: %v", err)
	}
	if scores2["a.md"] != 2 {
		t.Errorf("a.md overlap = %d, want 2 (layer+docker)", scores2["a.md"])
	}

	// Empty query → empty map, no error.
	empty, err := s.OverlapScores(nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("empty query: len=%d err=%v", len(empty), err)
	}
}

func TestPopulateKeywordsIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, ".wal")
	os.WriteFile(walPath, []byte(""), 0o644)
	s, _ := Open(filepath.Join(dir, ".sidecar.db"))
	defer s.Close()
	files := []native.MemFile{{Slug: "a.md", Body: "docker layer cache", ContentSHA: "a"}}
	if err := Reproject(s, files, walPath); err != nil {
		t.Fatal(err)
	}
	if err := Reproject(s, files, walPath); err != nil {
		t.Fatal(err)
	}
	// After two rebuilds, "docker" must map to exactly one row for a.md.
	scores, _ := s.OverlapScores([]string{"docker"})
	if scores["a.md"] != 1 {
		t.Errorf("after 2 rebuilds, a.md overlap for 'docker' = %d, want 1 (no dup rows)", scores["a.md"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sidecar/ -run TestPopulateKeywords -v`
Expected: FAIL — `s.OverlapScores undefined` / `s.PopulateKeywords undefined`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/sidecar/keyword.go`:

```go
package sidecar

import (
	"fmt"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/tokenize"
)

// PopulateKeywords clears and rebuilds the keyword table from the supplied
// files: name + description + body are tokenized; weight is term frequency.
// Called from Reproject so a rebuild keeps keywords in sync.
func (s *Store) PopulateKeywords(files []native.MemFile) error {
	if _, err := s.db.Exec(`DELETE FROM keyword`); err != nil {
		return fmt.Errorf("sidecar.PopulateKeywords: clear: %w", err)
	}
	for _, f := range files {
		text := f.Name + " " + f.Description + " " + f.Body
		freq := map[string]int{}
		for _, tok := range tokenize.Tokenize(text, nil) {
			freq[tok]++
		}
		for term, n := range freq {
			if _, err := s.db.Exec(`INSERT INTO keyword (slug, term, weight) VALUES (?,?,?)`,
				f.Slug, term, float64(n)); err != nil {
				return fmt.Errorf("sidecar.PopulateKeywords: insert %s/%s: %w", f.Slug, term, err)
			}
		}
	}
	return nil
}

// OverlapScores returns, per slug, the count of DISTINCT query terms that
// appear in that slug's keyword set. Empty query → empty map.
func (s *Store) OverlapScores(queryTerms []string) (map[string]int, error) {
	out := map[string]int{}
	// Dedupe query terms so a repeated query token can't inflate the count.
	seen := map[string]bool{}
	var uniq []string
	for _, t := range queryTerms {
		if t != "" && !seen[t] {
			seen[t] = true
			uniq = append(uniq, t)
		}
	}
	if len(uniq) == 0 {
		return out, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(uniq)), ",")
	args := make([]any, len(uniq))
	for i, t := range uniq {
		args[i] = t
	}
	rows, err := s.db.Query(
		`SELECT slug, COUNT(DISTINCT term) FROM keyword WHERE term IN (`+placeholders+`) GROUP BY slug`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("sidecar.OverlapScores: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var slug string
		var n int
		if err := rows.Scan(&slug, &n); err != nil {
			return nil, fmt.Errorf("sidecar.OverlapScores: scan: %w", err)
		}
		out[slug] = n
	}
	return out, rows.Err()
}
```

Modify `internal/sidecar/reproject.go`: at the END of `Reproject` (after `replayOutcomes`), before `return nil`, add:

```go
	if err := s.PopulateKeywords(files); err != nil {
		return fmt.Errorf("sidecar.Reproject: keywords: %w", err)
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sidecar/ -v`
Expected: PASS (new keyword tests + all existing sidecar tests still green).

- [ ] **Step 5: Commit**

```bash
git add internal/sidecar/keyword.go internal/sidecar/reproject.go internal/sidecar/keyword_test.go
git commit -m "feat(sidecar): populate keyword table in Reproject + OverlapScores query"
```

---

### Task 3: `internal/rank` — pure relevance ranker

**Files:**
- Create: `internal/rank/rank.go`
- Test: `internal/rank/rank_test.go`

**Context:** The merged injection pipeline's core. A pure, I/O-free function: given a query (prompt terms + project + domains + today's date) and candidates (each carrying static signals + a precomputed keyword `Overlap`), return the top-K by a zero-safe score. It defines its OWN `Candidate` struct (does NOT import `sidecar`) so it stays a dependency-free leaf — the caller maps `sidecar.Record` → `rank.Candidate`. Zero-safe: no single zero signal annihilates a candidate (spec §3.3); a brand-new fact (overlap 0, ref 0, eff 0.5) is still rankable.

- [ ] **Step 1: Write the failing test** `internal/rank/rank_test.go`

```go
package rank

import "testing"

func base() Candidate {
	return Candidate{Slug: "x", Status: "active", RefCount: 0, Effectiveness: 0.5, Created: "2026-05-01"}
}

func TestRank_OverlapDominates(t *testing.T) {
	q := Query{Terms: []string{"docker"}, Today: "2026-05-29"}
	a := base()
	a.Slug = "a"
	a.Overlap = 2
	b := base()
	b.Slug = "b"
	b.Overlap = 0
	got := Rank(q, []Candidate{b, a}, 0)
	if len(got) != 2 || got[0].Slug != "a" {
		t.Fatalf("higher overlap should rank first; got %v", slugs(got))
	}
}

func TestRank_MonotonicInRefCountAndEffectiveness(t *testing.T) {
	q := Query{Today: "2026-05-29"}
	lo := base()
	lo.Slug = "lo"
	hi := base()
	hi.Slug = "hi"
	hi.RefCount = 100
	got := Rank(q, []Candidate{lo, hi}, 0)
	if got[0].Slug != "hi" {
		t.Errorf("higher ref_count should rank first; got %v", slugs(got))
	}
}

func TestRank_ExcludesStaleAndDeleted(t *testing.T) {
	q := Query{Today: "2026-05-29"}
	act := base()
	act.Slug = "active"
	st := base()
	st.Slug = "stale"
	st.Status = "stale"
	del := base()
	del.Slug = "deleted"
	del.Status = "deleted"
	got := Rank(q, []Candidate{act, st, del}, 0)
	if len(got) != 1 || got[0].Slug != "active" {
		t.Errorf("stale/deleted must be excluded; got %v", slugs(got))
	}
}

func TestRank_ZeroSafe_NewFactStillRanks(t *testing.T) {
	q := Query{Today: "2026-05-29"}
	fresh := base() // overlap 0, ref 0, eff 0.5, no last_injected
	fresh.Slug = "fresh"
	fresh.Created = ""
	fresh.LastInjected = ""
	got := Rank(q, []Candidate{fresh}, 0)
	if len(got) != 1 {
		t.Fatalf("zero-signal fresh fact must still be rankable, got %d", len(got))
	}
	if got[0].Score < 0 {
		t.Errorf("score must not be negative for a zero-signal fact, got %v", got[0].Score)
	}
}

func TestRank_ProjectBoost(t *testing.T) {
	q := Query{Project: "proj", Today: "2026-05-29"}
	in := base()
	in.Slug = "in"
	in.Project = "proj"
	out := base()
	out.Slug = "out"
	out.Project = "other"
	got := Rank(q, []Candidate{out, in}, 0)
	if got[0].Slug != "in" {
		t.Errorf("project match should boost; got %v", slugs(got))
	}
}

func TestRank_DomainFilter(t *testing.T) {
	q := Query{Domains: []string{"backend"}, Today: "2026-05-29"}
	match := base()
	match.Slug = "match"
	match.Domains = []string{"backend", "db"}
	miss := base()
	miss.Slug = "miss"
	miss.Domains = []string{"frontend"}
	untagged := base()
	untagged.Slug = "untagged" // no domains → always eligible
	got := Rank(q, []Candidate{match, miss, untagged}, 0)
	if has(got, "miss") {
		t.Error("domain mismatch must be filtered out")
	}
	if !has(got, "match") || !has(got, "untagged") {
		t.Error("matching + untagged must survive domain filter")
	}
}

func TestRank_CapK(t *testing.T) {
	q := Query{Today: "2026-05-29"}
	var cands []Candidate
	for i, n := range []int{1, 2, 3, 4, 5} {
		c := base()
		c.Slug = string(rune('a' + i))
		c.RefCount = n
		cands = append(cands, c)
	}
	got := Rank(q, cands, 3)
	if len(got) != 3 {
		t.Fatalf("k=3 should cap to 3, got %d", len(got))
	}
	// highest ref_count first
	if got[0].Slug != "e" {
		t.Errorf("expected highest-ref first; got %v", slugs(got))
	}
}

func TestRank_DeterministicTieBreak(t *testing.T) {
	q := Query{Today: "2026-05-29"}
	a := base()
	a.Slug = "b"
	c := base()
	c.Slug = "a"
	// identical signals → tie broken by slug ascending
	got := Rank(q, []Candidate{a, c}, 0)
	if got[0].Slug != "a" {
		t.Errorf("tie must break by slug asc; got %v", slugs(got))
	}
}

func slugs(s []Scored) []string {
	out := make([]string, len(s))
	for i := range s {
		out[i] = s[i].Slug
	}
	return out
}

func has(s []Scored, slug string) bool {
	for _, x := range s {
		if x.Slug == slug {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/rank/ -v`
Expected: FAIL — `undefined: Candidate` / `undefined: Rank`.

- [ ] **Step 3: Write minimal implementation** `internal/rank/rank.go`

```go
// Package rank is the v2 relevance ranker — the merged injection pipeline's
// core. It is a pure, I/O-free leaf: no imports of sidecar/native, so it is
// trivially unit-testable and A/B-able (Phase 2b). The caller maps storage
// records into Candidate. Scoring is zero-safe: no single zero signal
// annihilates a candidate (spec §3.3).
package rank

import (
	"math"
	"sort"
	"time"
)

// Candidate is one rankable memory record with its static signals plus a
// precomputed keyword Overlap (count of query terms it matches).
type Candidate struct {
	Slug          string
	Type          string
	Project       string
	Domains       []string
	RefCount      int
	Effectiveness float64
	Status        string
	Created       string // YYYY-MM-DD, may be ""
	LastInjected  string // YYYY-MM-DD, may be ""
	Overlap       int
}

// Query carries the relevance signal for one ranking call.
type Query struct {
	Terms   []string
	Project string
	Domains []string
	Today   string // YYYY-MM-DD; drives recency decay
}

// Scored is a Candidate with its computed score.
type Scored struct {
	Candidate
	Score float64
}

// Scoring weights — fixed here; Phase 2b's A/B harness calibrates them.
const (
	wOverlap     = 3.0
	wRef         = 1.0
	wRecency     = 2.0
	wEffective   = 2.0
	projectBoost = 1.0
)

// Rank scores every eligible candidate and returns the top k (k<=0 = all),
// sorted by score descending, ties broken by slug ascending for
// determinism. Candidates with status stale/deleted, or failing the domain
// filter, are excluded.
func Rank(q Query, cands []Candidate, k int) []Scored {
	today := parseDay(q.Today)
	out := make([]Scored, 0, len(cands))
	for _, c := range cands {
		if c.Status == "stale" || c.Status == "deleted" {
			continue
		}
		if !domainOK(q.Domains, c.Domains) {
			continue
		}
		out = append(out, Scored{Candidate: c, Score: score(q, c, today)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Slug < out[j].Slug
	})
	if k > 0 && len(out) > k {
		out = out[:k]
	}
	return out
}

func score(q Query, c Candidate, today time.Time) float64 {
	s := wOverlap*float64(c.Overlap) +
		wRef*math.Log10(1+float64(c.RefCount)) +
		wRecency*recency(c, today) +
		wEffective*c.Effectiveness
	if q.Project != "" && c.Project == q.Project {
		s += projectBoost
	}
	return s
}

// recency decays from 1.0 (today) toward 0 with a 30-day half-ish scale,
// using LastInjected (fallback Created). Unknown/zero date → 0 contribution.
func recency(c Candidate, today time.Time) float64 {
	d := c.LastInjected
	if d == "" {
		d = c.Created
	}
	t := parseDay(d)
	if t.IsZero() || today.IsZero() {
		return 0
	}
	days := today.Sub(t).Hours() / 24.0
	if days < 0 {
		days = 0
	}
	return 1.0 / (1.0 + days/30.0)
}

func parseDay(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// domainOK: an untagged candidate (no domains) or an unfiltered query (no
// domains) is always eligible; otherwise require a non-empty intersection.
func domainOK(queryDomains, candDomains []string) bool {
	if len(candDomains) == 0 || len(queryDomains) == 0 {
		return true
	}
	for _, a := range queryDomains {
		for _, b := range candDomains {
			if a == b {
				return true
			}
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/rank/ -v`
Expected: PASS (all eight tests).

- [ ] **Step 5: Commit**

```bash
git add internal/rank/
git commit -m "feat(rank): pure zero-safe relevance ranker"
```

---

### Task 4: `memoryctl rank` diagnostic verb

**Files:**
- Create: `cmd/memoryctl/rank.go`
- Modify: `cmd/memoryctl/main.go` (add `case "rank":`)
- Test: `cmd/memoryctl/rank_test.go`

**Context:** Wire sidecar + ranker into a runnable verb so the ranker can be exercised end-to-end (and Phase 2b's A/B harness has a reference path). `memoryctl rank --query "<words>" [--project P] [--k N]` opens the sidecar, computes overlap for the query terms, maps records → `rank.Candidate`, ranks, prints. Uses `tokenize.Tokenize` on the query string. The sidecar's `Domains` column is a string; split on comma into `[]string` for the candidate.

- [ ] **Step 1: Write the failing test** `cmd/memoryctl/rank_test.go`

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRankVerb(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker cache\ntype: mistake\n---\ndocker layer cache stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "sql.md"),
		[]byte("---\nname: SQL index\ntype: knowledge\n---\npostgres index plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{
		"CLAUDE_HOME":        filepath.Join(home, ".claude"),
		"CLAUDE_MEMORY_DIR":  memDir,
		"CLAUDE_PROJECT_CWD": "/tmp/proj",
	}

	// rebuild first so the sidecar + keyword table exist
	if _, errOut, code := run(t, env, "sidecar", "rebuild"); code != 0 {
		t.Fatalf("rebuild failed: %s", errOut)
	}

	out, errOut, code := run(t, env, "rank", "--query", "docker cache", "--k", "2")
	if code != 0 {
		t.Fatalf("rank exit=%d stderr=%s", code, errOut)
	}
	// docker.md must rank above sql.md for a docker query
	di := strings.Index(out, "docker.md")
	si := strings.Index(out, "sql.md")
	if di == -1 {
		t.Fatalf("docker.md not in output: %q", out)
	}
	if si != -1 && di > si {
		t.Errorf("docker.md should rank above sql.md for 'docker cache'; got:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/memoryctl/ -run TestRankVerb -v`
Expected: FAIL — unknown command `rank` (exit 2) or undefined `runRank`.

- [ ] **Step 3a: Add dispatch in `cmd/memoryctl/main.go`**

In the top-level `switch os.Args[1] {`, add (single-level verb, like the structure of other verbs):

```go
	case "rank":
		runRank(os.Args[2:])
```

- [ ] **Step 3b: Implement** `cmd/memoryctl/rank.go`

```go
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/rank"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/tokenize"
)

// runRank: memoryctl rank --query "<words>" [--project P] [--k N]
// Diagnostic — ranks the sidecar's records against the query and prints them.
func runRank(args []string) {
	var query, project string
	k := 10
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--query":
			i++
			if i < len(args) {
				query = args[i]
			}
		case "--project":
			i++
			if i < len(args) {
				project = args[i]
			}
		case "--k":
			i++
			if i < len(args) {
				fmt.Sscanf(args[i], "%d", &k)
			}
		case "-h", "--help":
			fmt.Print(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "memoryctl rank: unknown flag %q\n", args[i])
			os.Exit(2)
		}
	}

	s, err := sidecar.Open(sidecarPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "rank: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	recs, err := s.All()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rank: %v\n", err)
		os.Exit(1)
	}
	terms := tokenize.Tokenize(query, nil)
	overlap, err := s.OverlapScores(terms)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rank: %v\n", err)
		os.Exit(1)
	}

	cands := make([]rank.Candidate, 0, len(recs))
	for _, r := range recs {
		var domains []string
		if r.Domains != "" {
			domains = strings.Split(r.Domains, ",")
		}
		cands = append(cands, rank.Candidate{
			Slug:          r.Slug,
			Type:          r.Type,
			Project:       r.Project,
			Domains:       domains,
			RefCount:      r.RefCount,
			Effectiveness: r.Effectiveness,
			Status:        r.Status,
			Created:       r.Created,
			LastInjected:  r.LastInjected,
			Overlap:       overlap[r.Slug],
		})
	}

	q := rank.Query{Terms: terms, Project: project, Today: today()}
	for _, sc := range rank.Rank(q, cands, k) {
		fmt.Printf("%-40s score=%.3f overlap=%d ref=%d eff=%.2f\n",
			sc.Slug, sc.Score, sc.Overlap, sc.RefCount, sc.Effectiveness)
	}
}
```

- [ ] **Step 3c: Add a `today()` helper** in `cmd/memoryctl/rank.go` (the ranker needs today's date as `YYYY-MM-DD`; Go's `time.Now()` is fine here — this is the runtime entry point, not a pure/replayable unit):

```go
// today returns the current date as YYYY-MM-DD for recency scoring. Override
// with $HYPOMNEMA_TODAY (used by tests/replay to pin the clock).
func today() string {
	if d := os.Getenv("HYPOMNEMA_TODAY"); d != "" {
		return d
	}
	return timeNow().Format("2006-01-02")
}
```

Add the import `"time"` and a tiny indirection so tests are deterministic:

```go
var timeNow = time.Now
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/memoryctl/ -run TestRankVerb -v`
Expected: PASS.

Then full suite:

Run: `go build ./... && go test -race ./... && go vet ./...`
Expected: PASS, no regressions.

- [ ] **Step 5: Commit**

```bash
git add cmd/memoryctl/rank.go cmd/memoryctl/main.go cmd/memoryctl/rank_test.go
git commit -m "feat(memoryctl): rank verb wiring sidecar + ranker"
```

---

## Phase 2a Definition of Done

- [ ] `internal/tokenize` provides the Unicode tokenizer; `internal/rank` and `internal/sidecar` use it; nothing depends on `internal/tfidf` for tokenization.
- [ ] `Reproject` populates the `keyword` table (idempotently); `OverlapScores` returns per-slug distinct-term match counts.
- [ ] `internal/rank` is pure (no sidecar/native import), zero-safe, excludes stale/deleted, applies project boost + domain filter, deterministic tie-break, caps at k.
- [ ] `memoryctl rank --query` ranks real records end-to-end; docker query surfaces the docker record first.
- [ ] `go test -race ./...` green; `make build` ok; no hook behaviour changed (still additive).

## Self-Review (against spec §3.3 ranker + §1.1 MERGE)

- **Spec §1.1 MERGE (one relevance ranker):** `rank.Rank` is the single scorer; prompt terms drive `Overlap` (reactive relevance replacing substring-triggers). ✓
- **Spec §3.3 zero-safe:** additive score, `log10(1+ref)`, eff defaults 0.5, recency in (0,1] — no annihilation; `TestRank_ZeroSafe` locks it. ✓
- **Spec §3.3 pure & A/B-able:** `rank` imports only math/sort/time — no I/O, no sidecar. ✓ (enables Phase 2b replay)
- **Tokenizer salvage (§1.1):** moved to `internal/tokenize`, tfidf scoring untouched for now (removed in Phase 7 cutover). ✓
- **Type consistency:** `rank.Candidate` fields match the mapping in `runRank` from `sidecar.Record`; `OverlapScores`/`PopulateKeywords` names consistent between Task 2 and Task 4. ✓
- **No placeholders:** every step has complete code + commands. ✓
- **Deferred (noted):** per-type quotas → Phase 3 (inject policy); stopwords in keyword population → revisit; weight calibration → Phase 2b A/B.
