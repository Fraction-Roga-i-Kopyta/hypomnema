# v2.0 Phase 4 — Close path (outcome measurement + decay) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Checkbox steps.

**Goal:** `memoryctl close` (the Stop hook): classify each injected memory as cited (`trigger-useful`) or not (`trigger-silent`) from the session transcript, emit the closing WAL events, refresh the sidecar, regenerate the self-profile, and decay stale entries.

**Architecture:** `internal/closer` owns citation classification (pure) + the close orchestrator. `internal/sidecar` gains `MarkStale` (per-type age decay). `memoryctl close` parses the Stop stdin and runs it. Reuses `internal/jsonl` (transcript), `internal/profile.Generate` (self-profile), `wal.SanitizeField` (Phase 3).

**Tech Stack:** Go 1.25, stdlib. Module `github.com/Fraction-Roga-i-Kopyta/hypomnema`.

## Grounding (hooks-contract §6, FORMAT §5.2)

- **Stop stdin:** `{"session_id","cwd","transcript_path"}`. transcript may be absent (tolerate, exit 0).
- **Transcript:** `jsonl.ReadSession(path) (Session{ID, Text}, error)` → concatenated assistant text.
- **Citation:** an injected slug is `trigger-useful` if its slug (minus `.md`) or its `name` appears in the assistant text; else `trigger-silent`.
- **Closing WAL events:** `trigger-useful|<slug>|<sid>`, `trigger-silent|<slug>|<sid>`, `session-close|<sid>|<sid>` (sid in canonical $4), `session-metrics|domains:<csv>,error_count:N,tool_calls:M,duration:Ss|<sid>` (v2; readers tolerate absent keys=0).
- **Self-profile:** `profile.Generate(memoryDir) error`.
- **Decay thresholds (stale, days; CLAUDE.md table):** mistake 60, strategy 90, feedback 45, knowledge 90, decision 90, note 30. `pinned` never decays.

**Scope boundary (deferred, noted):** outcome-positive/negative (Bayesian effectiveness numerator — needs mistake-recurrence transcript analysis; effectiveness stays neutral 0.5 meanwhile, which the Phase 2b A/B showed is fine for ranking). clean-session error-detection (needs tool-error parsing). continuity-generation (v1 nicety). session-metrics is emitted minimal (domains + zeroed counters). These are enrichments, not blockers for the measurement loop.

---

### Task 1: `internal/closer` — citation classification

**Files:** Create `internal/closer/classify.go`, `internal/closer/classify_test.go`

- [ ] **Step 1: failing test** `internal/closer/classify_test.go`
```go
package closer

import (
	"sort"
	"testing"
)

func TestClassify(t *testing.T) {
	injected := []string{"css-layout-debugging.md", "docker-stale-cache.md", "sql-index.md"}
	names := map[string]string{
		"css-layout-debugging.md": "CSS layout debugging",
		"docker-stale-cache.md":   "Docker stale cache",
		"sql-index.md":            "SQL index",
	}
	// Assistant cited the css slug verbatim and the docker NAME; never sql.
	text := "I applied css-layout-debugging here. Also the Docker stale cache rule helped."
	useful, silent := Classify(injected, names, text)
	sort.Strings(useful)
	sort.Strings(silent)
	if len(useful) != 2 || useful[0] != "css-layout-debugging.md" || useful[1] != "docker-stale-cache.md" {
		t.Errorf("useful = %v, want css + docker", useful)
	}
	if len(silent) != 1 || silent[0] != "sql-index.md" {
		t.Errorf("silent = %v, want sql", silent)
	}
}

func TestClassify_EmptyText(t *testing.T) {
	injected := []string{"a.md"}
	useful, silent := Classify(injected, map[string]string{"a.md": "A"}, "")
	if len(useful) != 0 || len(silent) != 1 {
		t.Errorf("empty text → all silent; got useful=%v silent=%v", useful, silent)
	}
}
```

- [ ] **Step 2: run, expect FAIL** — `go test ./internal/closer/ -v`

- [ ] **Step 3: implement** `internal/closer/classify.go`
```go
// Package closer implements the Stop-hook close path: it classifies injected
// memories as cited (trigger-useful) or not (trigger-silent) and emits the
// closing WAL events, refreshes the sidecar, and decays stale entries.
package closer

import "strings"

// Classify splits injected slugs into useful (cited in the assistant text) and
// silent (not cited). A slug counts as cited if its base name (slug minus
// ".md") or its frontmatter name appears in the text, case-insensitively.
func Classify(injected []string, names map[string]string, text string) (useful, silent []string) {
	lower := strings.ToLower(text)
	for _, slug := range injected {
		base := strings.ToLower(strings.TrimSuffix(slug, ".md"))
		cited := base != "" && strings.Contains(lower, base)
		if !cited {
			if n := strings.ToLower(strings.TrimSpace(names[slug])); n != "" && strings.Contains(lower, n) {
				cited = true
			}
		}
		if cited {
			useful = append(useful, slug)
		} else {
			silent = append(silent, slug)
		}
	}
	return useful, silent
}
```

- [ ] **Step 4: run, expect PASS** — `go test ./internal/closer/ -v`; `go vet ./internal/closer/`
- [ ] **Step 5: commit** — `git add internal/closer/classify.go internal/closer/classify_test.go && git commit -m "feat(closer): citation classification (trigger-useful/silent)"`

---

### Task 2: `internal/sidecar` — `MarkStale` (per-type age decay)

**Files:** Create `internal/sidecar/decay.go`, `internal/sidecar/decay_test.go`

- [ ] **Step 1: failing test** `internal/sidecar/decay_test.go`
```go
package sidecar

import (
	"path/filepath"
	"testing"
)

func TestMarkStale(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// feedback stale threshold = 45d. created 2026-01-01, today 2026-05-29 → ~148d → stale.
	mustUpsert(t, s, Record{Slug: "old-fb.md", Type: "feedback", Created: "2026-01-01", Status: "active"})
	// mistake created 2026-05-20 → ~9d, threshold 60 → stays active.
	mustUpsert(t, s, Record{Slug: "fresh-mi.md", Type: "mistake", Created: "2026-05-20", Status: "active"})
	// pinned never decays even if ancient.
	mustUpsert(t, s, Record{Slug: "pinned.md", Type: "note", Created: "2020-01-01", Status: "pinned"})

	n, err := s.MarkStale("2026-05-29")
	if err != nil {
		t.Fatalf("MarkStale: %v", err)
	}
	if n != 1 {
		t.Errorf("marked %d stale, want 1 (old feedback only)", n)
	}
	if r, _, _ := s.Get("old-fb.md"); r.Status != "stale" {
		t.Errorf("old-fb status = %q, want stale", r.Status)
	}
	if r, _, _ := s.Get("fresh-mi.md"); r.Status != "active" {
		t.Errorf("fresh-mi status = %q, want active", r.Status)
	}
	if r, _, _ := s.Get("pinned.md"); r.Status != "pinned" {
		t.Errorf("pinned status = %q, want pinned (never decays)", r.Status)
	}
}

func mustUpsert(t *testing.T, s *Store, r Record) {
	t.Helper()
	if err := s.Upsert(r); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: run, expect FAIL** — `go test ./internal/sidecar/ -run TestMarkStale -v`

- [ ] **Step 3: implement** `internal/sidecar/decay.go`
```go
package sidecar

import (
	"fmt"
	"time"
)

// staleDays is the active→stale threshold per fine-grained type, in days
// (CLAUDE.md memory-layout table). Unknown types default to 90.
var staleDays = map[string]int{
	"mistake": 60, "strategy": 90, "feedback": 45,
	"knowledge": 90, "decision": 90, "note": 30,
}

// MarkStale flips active rows older than their type's stale threshold to
// status='stale' (down-rank, not removed). pinned/stale/deleted are left
// alone. Returns the number of rows newly marked. `today` is YYYY-MM-DD.
func (s *Store) MarkStale(today string) (int, error) {
	t0, err := time.Parse("2006-01-02", today)
	if err != nil {
		return 0, fmt.Errorf("sidecar.MarkStale: bad today %q: %w", today, err)
	}
	rows, err := s.db.Query(`SELECT slug, type, created FROM memory WHERE status='active'`)
	if err != nil {
		return 0, fmt.Errorf("sidecar.MarkStale: query: %w", err)
	}
	var stale []string
	for rows.Next() {
		var slug, typ, created string
		if err := rows.Scan(&slug, &typ, &created); err != nil {
			rows.Close()
			return 0, fmt.Errorf("sidecar.MarkStale: scan: %w", err)
		}
		c, perr := time.Parse("2006-01-02", created)
		if perr != nil {
			continue // no/invalid created date → can't age it; leave active
		}
		thr, ok := staleDays[typ]
		if !ok {
			thr = 90
		}
		if int(t0.Sub(c).Hours()/24) > thr {
			stale = append(stale, slug)
		}
	}
	rows.Close()
	for _, slug := range stale {
		if _, err := s.db.Exec(`UPDATE memory SET status='stale' WHERE slug=? AND status='active'`, slug); err != nil {
			return 0, fmt.Errorf("sidecar.MarkStale: update %s: %w", slug, err)
		}
	}
	return len(stale), nil
}
```

- [ ] **Step 4: run, expect PASS** — `go test ./internal/sidecar/ -v`; `go vet ./internal/sidecar/`
- [ ] **Step 5: commit** — `git add internal/sidecar/decay.go internal/sidecar/decay_test.go && git commit -m "feat(sidecar): MarkStale per-type age decay"`

---

### Task 3: `internal/closer` — close orchestrator

**Files:** Create `internal/closer/close.go`, `internal/closer/close_test.go`

- [ ] **Step 1: failing test** `internal/closer/close_test.go`
```go
package closer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_EmitsClosingEvents(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	os.MkdirAll(filepath.Join(memDir, ".runtime"), 0o755)
	os.MkdirAll(projDir, 0o755)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker cache\ntype: mistake\n---\ndocker cache\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "sql.md"),
		[]byte("---\nname: SQL index\ntype: knowledge\n---\nsql index\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	// injected-set: both slugs injected this session.
	os.WriteFile(filepath.Join(memDir, ".runtime", "injected-s1.list"),
		[]byte("docker.md\nsql.md\n"), 0o600)
	// transcript: assistant cited docker, not sql.
	tx := filepath.Join(home, "t.jsonl")
	os.WriteFile(tx, []byte(`{"type":"assistant","sessionId":"s1","message":{"content":[{"type":"text","text":"used docker.md to fix it"}]}}`+"\n"), 0o644)

	res, err := Run(Input{
		SessionID: "s1", CWD: "/tmp/proj", TranscriptPath: tx,
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-05-29",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Useful != 1 || res.Silent != 1 {
		t.Errorf("useful=%d silent=%d, want 1/1", res.Useful, res.Silent)
	}
	wal, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	w := string(wal)
	for _, want := range []string{"|trigger-useful|docker.md|s1", "|trigger-silent|sql.md|s1", "|session-close|s1|s1", "|session-metrics|"} {
		if !strings.Contains(w, want) {
			t.Errorf("WAL missing %q:\n%s", want, w)
		}
	}
	// self-profile regenerated
	if _, err := os.Stat(filepath.Join(memDir, "self-profile.md")); err != nil {
		t.Errorf("self-profile not generated: %v", err)
	}
}

func TestRun_MissingTranscriptIsNotFatal(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(filepath.Join(memDir, ".runtime"), 0o755)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	res, err := Run(Input{
		SessionID: "s1", CWD: "/tmp/proj", TranscriptPath: filepath.Join(home, "nope.jsonl"),
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-05-29",
	})
	if err != nil {
		t.Fatalf("missing transcript must not error: %v", err)
	}
	// no injected-set, no transcript → nothing classified, but session-close still emitted
	wal, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if !strings.Contains(string(wal), "|session-close|s1|s1") {
		t.Errorf("session-close must be emitted even with no transcript:\n%s", wal)
	}
	_ = res
}
```

- [ ] **Step 2: run, expect FAIL** — `go test ./internal/closer/ -run TestRun -v`

- [ ] **Step 3: implement** `internal/closer/close.go`
```go
package closer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/jsonl"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/profile"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/wal"
)

// Input is the parsed Stop-hook envelope plus resolved paths.
type Input struct {
	SessionID      string
	CWD            string
	TranscriptPath string
	ClaudeHome     string
	MemoryDir      string
	Today          string
}

// Result reports what close did (for diagnostics / the verb's reminder).
type Result struct {
	Useful int
	Silent int
	Staled int
}

// Run performs the close path: classify injected memories, emit closing WAL
// events, refresh the sidecar, regenerate the self-profile, decay stale rows.
// Best-effort throughout — a failure in one step never aborts the others.
func Run(in Input) (Result, error) {
	var res Result
	injected := readInjectedSet(in.MemoryDir, in.SessionID)
	names := slugNames(in.ClaudeHome, in.CWD)

	var text string
	if sess, err := jsonl.ReadSession(in.TranscriptPath); err == nil {
		text = sess.Text
	}

	useful, silent := Classify(injected, names, text)
	res.Useful, res.Silent = len(useful), len(silent)
	sid := wal.SanitizeField(in.SessionID)
	for _, slug := range useful {
		appendWAL(in.MemoryDir, in.Today, "trigger-useful", slug, sid)
	}
	for _, slug := range silent {
		appendWAL(in.MemoryDir, in.Today, "trigger-silent", slug, sid)
	}
	// session-metrics (minimal, v2 shape) + session-close (canonical $4).
	metrics := fmt.Sprintf("%s|session-metrics|domains:_global_,error_count:0,tool_calls:0,duration:0s|%s", in.Today, sid)
	wal.Append(in.MemoryDir, metrics, "")
	wal.Append(in.MemoryDir, fmt.Sprintf("%s|session-close|%s|%s", in.Today, sid, sid), "")

	// Refresh sidecar from the freshly-appended WAL + native, then decay.
	if s, err := sidecar.Open(filepath.Join(in.MemoryDir, ".sidecar.db")); err == nil {
		files := collectNative(in.ClaudeHome, in.CWD)
		_ = sidecar.Reproject(s, files, filepath.Join(in.MemoryDir, ".wal"))
		if n, derr := s.MarkStale(in.Today); derr == nil {
			res.Staled = n
		}
		s.Close()
	}
	_ = profile.Generate(in.MemoryDir) // best-effort self-profile regen

	return res, nil
}

func readInjectedSet(memDir, sessionID string) []string {
	b, err := os.ReadFile(filepath.Join(memDir, ".runtime", "injected-"+sessionID+".list"))
	if err != nil {
		return nil
	}
	var out []string
	for _, ln := range strings.Split(string(b), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			out = append(out, ln)
		}
	}
	return out
}

func collectNative(claudeHome, cwd string) []native.MemFile {
	osHome := filepath.Dir(claudeHome)
	proj, _ := native.List(native.ProjectMemoryDir(osHome, cwd))
	glob, _ := native.List(native.GlobalMemoryDir(osHome))
	return append(proj, glob...)
}

func slugNames(claudeHome, cwd string) map[string]string {
	out := map[string]string{}
	for _, f := range collectNative(claudeHome, cwd) {
		out[f.Slug] = f.Name
	}
	return out
}

func appendWAL(memDir, day, event, slug, sid string) {
	line := fmt.Sprintf("%s|%s|%s|%s", day, event, wal.SanitizeField(slug), sid)
	wal.Append(memDir, line, "")
}
```

- [ ] **Step 4: run, expect PASS** — `go test ./internal/closer/ -v`; `go vet ./internal/closer/`; `go build ./...`
- [ ] **Step 5: commit** — `git add internal/closer/close.go internal/closer/close_test.go && git commit -m "feat(closer): close orchestrator — closing events + reproject + profile + decay"`

---

### Task 4: `memoryctl close` verb

**Files:** Create `cmd/memoryctl/close.go`, `cmd/memoryctl/close_test.go`; modify `cmd/memoryctl/main.go`

- [ ] **Step 1: failing test** `cmd/memoryctl/close_test.go`
```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloseVerb(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	os.MkdirAll(filepath.Join(memDir, ".runtime"), 0o755)
	os.MkdirAll(projDir, 0o755)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker\ntype: mistake\n---\ndocker\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(memDir, ".runtime", "injected-s1.list"), []byte("docker.md\n"), 0o600)
	tx := filepath.Join(home, "t.jsonl")
	os.WriteFile(tx, []byte(`{"type":"assistant","sessionId":"s1","message":{"content":[{"type":"text","text":"docker.md helped"}]}}`+"\n"), 0o644)

	env := map[string]string{
		"CLAUDE_HOME": filepath.Join(home, ".claude"), "CLAUDE_MEMORY_DIR": memDir,
		"HYPOMNEMA_TODAY": "2026-05-29",
	}
	stdin := `{"session_id":"s1","cwd":"/tmp/proj","transcript_path":"` + tx + `"}`
	_, errOut, code := runStdin(t, env, stdin, "close")
	if code != 0 {
		t.Fatalf("close exit=%d stderr=%s", code, errOut)
	}
	wal, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if !strings.Contains(string(wal), "|trigger-useful|docker.md|s1") {
		t.Errorf("expected trigger-useful for cited docker.md:\n%s", wal)
	}
	if !strings.Contains(string(wal), "|session-close|s1|s1") {
		t.Errorf("expected session-close:\n%s", wal)
	}
}

func TestCloseVerb_FailSafe(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(memDir, 0o755)
	env := map[string]string{"CLAUDE_HOME": filepath.Join(home, ".claude"), "CLAUDE_MEMORY_DIR": memDir}
	_, _, code := runStdin(t, env, "garbage", "close")
	if code != 0 {
		t.Fatalf("bad stdin must exit 0, got %d", code)
	}
}
```

- [ ] **Step 2: run, expect FAIL** — `go test ./cmd/memoryctl/ -run TestCloseVerb -v`

- [ ] **Step 3a: dispatch in main.go** — add `case "close":` → `runClose(os.Args[2:])`

- [ ] **Step 3b: implement** `cmd/memoryctl/close.go`
```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/closer"
)

type stopStdin struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	TranscriptPath string `json:"transcript_path"`
}

// runClose: memoryctl close — the Stop hook. Reads the Stop envelope from
// stdin, runs the close path, exits 0 always (fail-safe).
func runClose(args []string) {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Print(usage)
			return
		}
		fmt.Fprintf(os.Stderr, "memoryctl close: unknown flag %q\n", a)
		os.Exit(2)
	}
	raw, _ := io.ReadAll(os.Stdin)
	var in stopStdin
	if err := json.Unmarshal(raw, &in); err != nil {
		os.Exit(0) // fail-safe
	}
	_, _ = closer.Run(closer.Input{
		SessionID: in.SessionID, CWD: in.CWD, TranscriptPath: in.TranscriptPath,
		ClaudeHome: claudeDir(), MemoryDir: memoryDir(), Today: today(),
	})
	os.Exit(0)
}
```

- [ ] **Step 4: run, expect PASS** — `go test ./cmd/memoryctl/ -run TestCloseVerb -v`; then `go build ./... && go test -race ./... && go vet ./...`
- [ ] **Step 5: commit** — `git add cmd/memoryctl/close.go cmd/memoryctl/main.go cmd/memoryctl/close_test.go && git commit -m "feat(memoryctl): close verb — Stop hook close path"`

---

## Phase 4 Definition of Done

- [ ] `internal/closer.Classify` splits injected slugs into trigger-useful/silent by citation (slug or name in assistant text).
- [ ] `sidecar.MarkStale` decays active rows past their per-type threshold (pinned exempt).
- [ ] `closer.Run` emits trigger-useful/silent + session-close + session-metrics (sanitized, non-deduped), reprojects, regen self-profile, decays — best-effort, missing transcript tolerated.
- [ ] `memoryctl close` parses Stop stdin, runs it, exit 0 always. `hooks/v2/session-stop.sh` (already created Phase 3) now drives a real verb.
- [ ] `go test -race ./...` green.

## Self-Review (against contract §6, FORMAT §5.2)

- **Closing events:** trigger-useful/silent, session-close (sid in $4), session-metrics (v2 `domains:..,error_count:..`); all 4-col, sanitized via `wal.SanitizeField`, non-deduped (`""` key). ✓
- **Citation:** slug-base or name, case-insensitive (matches v1 grep semantics). ✓
- **Fail-safe:** missing transcript / bad stdin → exit 0, session-close still emitted; every step best-effort. ✓
- **Decay:** per-type thresholds from CLAUDE.md; pinned exempt; down-rank not delete. ✓
- **Reuse:** `jsonl.ReadSession`, `profile.Generate`, `wal.SanitizeField`, `sidecar.Reproject` — no duplication. ✓
- **Deferred (noted):** outcome-positive/negative (effectiveness stays neutral 0.5 — A/B-validated as fine for ranking); clean-session error-detection; continuity-gen; real session-metrics counters. Tracked for a later enrichment.
