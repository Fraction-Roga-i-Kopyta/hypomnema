# v2.0 Phase 2b — A/B null-hypothesis harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the experiment that finally tests whether ranking earns its keep — replay the historical WAL and compare the ranker's top-K against a random-K baseline on a usefulness proxy (recall@K of each session's `trigger-useful` slugs).

**Architecture:** A pure-logic `internal/ab` package (WAL event model, eval-session extraction, temporal-holdout signal aggregation, replay + recall) consuming `internal/rank`, plus a `memoryctl ab` verb that runs it over the live WAL and prints a recall table.

**Tech Stack:** Go 1.25, stdlib (`math/rand` seeded for determinism). Module `github.com/Fraction-Roga-i-Kopyta/hypomnema`.

## Methodology (the load-bearing decisions — read before coding)

- **Ground truth:** for each historical session S, the *relevant* set `U_S` = slugs that received a `trigger-useful|slug|S` event. Sessions with no `trigger-useful` are skipped (no ground truth).
- **Temporal holdout (leakage-free):** to rank session S (date `d_S`), signals are aggregated from WAL events with date **strictly before `d_S`**. This avoids lookahead bias — a slug's *current* ref_count is partly caused by the very session we're scoring; using only the past is both honest and realistic (you only know the past at session start).
- **Candidate pool:** all distinct slugs appearing anywhere in the WAL (a proxy for "the corpus that existed"). `U_S` ⊆ pool. A slug with no pre-`d_S` events scores on zero-safe priors (ref 0, eff 0.5, recency 0) — honest: static signals genuinely can't predict a first-time-useful slug.
- **No keyword overlap:** historical prompts/keywords were never stored, so `Overlap=0` for every candidate. The harness therefore tests the **static signals** (ref_count/effectiveness/recency); the keyword-overlap component is validated live in later phases. This is a real scope limit, stated in the output.
- **Strategies @ budget K:** `ranked` = `rank.Rank` top-K; `random` = K uniformly-random pool slugs (seeded → reproducible). The null hypothesis is "ranking is no better than random at concentrating useful slugs into a small budget." K ∈ {3, 5, 8, 12}.
- **Metric:** recall@K = |selected ∩ U_S| / |U_S|, averaged over eval sessions.
- **Small-n is expected** (~39–49 usable sessions; compacted `inject-agg` lost per-session inject linkage). The verb prints n and labels the result a directional signal, not a verdict.

---

### Task 1: `internal/ab` — WAL model, eval sessions, temporal-holdout signals

**Files:**
- Create: `internal/ab/ab.go`
- Test: `internal/ab/ab_test.go`

- [ ] **Step 1: Write the failing test** `internal/ab/ab_test.go`

```go
package ab

import (
	"os"
	"path/filepath"
	"testing"
)

func writeWAL(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".wal")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseWALAndEvalSessions(t *testing.T) {
	p := writeWAL(t, ""+
		"2026-04-01|inject|a.md|s1\n"+
		"# comment skipped\n"+
		"2026-04-02|trigger-useful|a.md|s1\n"+
		"2026-04-02|trigger-useful|b.md|s1\n"+
		"2026-04-05|inject|a.md|s2\n"+
		"2026-04-05|trigger-useful|a.md|s2\n")
	events, err := ParseWAL(p)
	if err != nil {
		t.Fatalf("ParseWAL: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("expected 5 events (comment skipped), got %d", len(events))
	}
	sessions := EvalSessions(events)
	if len(sessions) != 2 {
		t.Fatalf("expected 2 eval sessions, got %d", len(sessions))
	}
	// sessions sorted by date for determinism; s1 first.
	if sessions[0].Date != "2026-04-02" {
		t.Errorf("s1 date = %q, want 2026-04-02 (earliest trigger-useful)", sessions[0].Date)
	}
	if len(sessions[0].Useful) != 2 || !sessions[0].Useful["a.md"] || !sessions[0].Useful["b.md"] {
		t.Errorf("s1 useful set wrong: %v", sessions[0].Useful)
	}
}

func TestSignalsBefore_TemporalHoldout(t *testing.T) {
	events, _ := ParseWAL(writeWAL(t, ""+
		"2026-04-01|inject|a.md|s1\n"+
		"2026-04-02|inject-agg|a.md|10\n"+
		"2026-04-02|outcome-positive|a.md|s1\n"+
		"2026-04-10|inject|a.md|s2\n"+ // on/after cutoff — must be EXCLUDED
		"2026-04-10|outcome-negative|a.md|s2\n"))
	sig := SignalsBefore(events, "2026-04-10")
	a := sig["a.md"]
	// before 2026-04-10: 1 inject + inject-agg(10) = 11; 1 positive, 0 negative
	if a.RefCount != 11 {
		t.Errorf("RefCount = %d, want 11 (1 inject + agg 10; the 2026-04-10 inject excluded)", a.RefCount)
	}
	if a.Pos != 1 || a.Neg != 0 {
		t.Errorf("Pos/Neg = %d/%d, want 1/0 (the 2026-04-10 negative excluded)", a.Pos, a.Neg)
	}
	if a.LastInject != "2026-04-02" {
		t.Errorf("LastInject = %q, want 2026-04-02 (latest inject before cutoff)", a.LastInject)
	}
}

func TestCandidatePool(t *testing.T) {
	events, _ := ParseWAL(writeWAL(t, ""+
		"2026-04-01|inject|a.md|s1\n"+
		"2026-04-01|inject|b.md|s1\n"+
		"2026-04-02|trigger-useful|a.md|s1\n"))
	pool := CandidatePool(events)
	if len(pool) != 2 {
		t.Fatalf("pool = %v, want 2 distinct slugs", pool)
	}
	// sorted for determinism
	if pool[0] != "a.md" || pool[1] != "b.md" {
		t.Errorf("pool not sorted: %v", pool)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ab/ -v`
Expected: FAIL — `undefined: ParseWAL`.

- [ ] **Step 3: Write minimal implementation** `internal/ab/ab.go`

```go
// Package ab is the A/B null-hypothesis harness: it replays the historical
// WAL and measures whether the ranker concentrates each session's
// trigger-useful slugs into a small budget better than random selection.
// Signals are aggregated under a strict temporal holdout (events before the
// session's date only) so the replay is leakage-free.
package ab

import (
	"bufio"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Event is one parsed WAL row: DATE|KIND|SLUG|FIELD4.
type Event struct {
	Date  string
	Kind  string
	Slug  string
	Field string // session id for most events; aggregated count for inject-agg
}

// EvalSession is a historical session usable as ground truth: its date and
// the set of slugs that proved useful in it.
type EvalSession struct {
	Date   string
	Useful map[string]bool
}

// Signal holds the temporal-holdout aggregates for one slug.
type Signal struct {
	RefCount   int
	Pos        int
	Neg        int
	LastInject string
}

// ParseWAL reads a WAL file into events (file order). Blank/comment/malformed
// lines are skipped. A missing file yields (nil, nil).
func ParseWAL(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}
		out = append(out, Event{Date: parts[0], Kind: parts[1], Slug: parts[2], Field: parts[3]})
	}
	return out, sc.Err()
}

// EvalSessions groups trigger-useful events by session id (Field), attaching
// each session's earliest trigger-useful date. Returned sorted by date then
// session id for determinism.
func EvalSessions(events []Event) []EvalSession {
	type acc struct {
		date   string
		useful map[string]bool
	}
	bySession := map[string]*acc{}
	for _, e := range events {
		if e.Kind != "trigger-useful" {
			continue
		}
		a := bySession[e.Field]
		if a == nil {
			a = &acc{date: e.Date, useful: map[string]bool{}}
			bySession[e.Field] = a
		}
		if e.Date < a.date {
			a.date = e.Date
		}
		a.useful[e.Slug] = true
	}
	out := make([]EvalSession, 0, len(bySession))
	for _, a := range bySession {
		out = append(out, EvalSession{Date: a.date, Useful: a.useful})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return len(out[i].Useful) < len(out[j].Useful)
	})
	return out
}

// SignalsBefore aggregates per-slug signals from events with Date strictly
// before `date`. inject-agg's Field is its aggregated count (wal-compact.sh).
func SignalsBefore(events []Event, date string) map[string]Signal {
	out := map[string]Signal{}
	for _, e := range events {
		if e.Date >= date {
			continue
		}
		s := out[e.Slug]
		switch e.Kind {
		case "inject":
			s.RefCount++
			if e.Date > s.LastInject {
				s.LastInject = e.Date
			}
		case "inject-agg":
			n, err := strconv.Atoi(e.Field)
			if err != nil || n < 1 {
				n = 1
			}
			s.RefCount += n
			if e.Date > s.LastInject {
				s.LastInject = e.Date
			}
		case "outcome-positive":
			s.Pos++
		case "outcome-negative":
			s.Neg++
		}
		out[e.Slug] = s
	}
	return out
}

// CandidatePool returns the distinct slugs appearing in events, sorted.
func CandidatePool(events []Event) []string {
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.Slug] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ab/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ab/ab.go internal/ab/ab_test.go
git commit -m "feat(ab): WAL model, eval sessions, temporal-holdout signals"
```

---

### Task 2: `internal/ab` — replay (ranked vs random recall@K)

**Files:**
- Create: `internal/ab/replay.go`
- Test: `internal/ab/replay_test.go`

**Context:** For each eval session, build zero-safe candidates from the temporal-holdout signals, rank them (Overlap 0 — static signals only), and measure recall@K of the session's useful set against `ranked top-K` vs `random K`. Random uses a seeded `*rand.Rand` so results are reproducible. Effectiveness = `(Pos+1)/(Pos+Neg+2)`.

- [ ] **Step 1: Write the failing test** `internal/ab/replay_test.go`

```go
package ab

import (
	"os"
	"path/filepath"
	"testing"
)

// Construct a WAL where one slug ("hot") is strongly used before the eval
// session and is the useful one — ranked should beat random at small K.
func TestReplay_RankedBeatsRandomWhenSignalPredicts(t *testing.T) {
	var b strings.Builder
	// 20 cold slugs with a single early inject (low ref), none useful.
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "2026-04-01|inject|cold%02d.md|s0\n", i)
	}
	// hot.md: heavy pre-session ref + positive outcomes.
	b.WriteString("2026-04-01|inject-agg|hot.md|50\n")
	b.WriteString("2026-04-01|outcome-positive|hot.md|s0\n")
	// eval session s1 on 2026-04-10: hot.md is the useful one.
	b.WriteString("2026-04-10|inject|hot.md|s1\n")
	b.WriteString("2026-04-10|trigger-useful|hot.md|s1\n")

	p := filepath.Join(t.TempDir(), ".wal")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	events, _ := ParseWAL(p)

	results := Replay(events, []int{1, 3}, 42)
	if len(results) != 2 {
		t.Fatalf("expected 2 K-rows, got %d", len(results))
	}
	// At K=1, ranked must capture hot.md (highest pre-session signal) → recall 1.0,
	// while random over 21 slugs almost certainly misses → < 1.0.
	r1 := results[0]
	if r1.K != 1 {
		t.Fatalf("first row K=%d, want 1", r1.K)
	}
	if r1.RankedRecall != 1.0 {
		t.Errorf("ranked recall@1 = %.2f, want 1.0 (hot.md ranks first)", r1.RankedRecall)
	}
	if r1.RandomRecall >= r1.RankedRecall {
		t.Errorf("random recall@1 (%.2f) should be < ranked (%.2f)", r1.RandomRecall, r1.RankedRecall)
	}
	if r1.Sessions != 1 {
		t.Errorf("sessions = %d, want 1", r1.Sessions)
	}
}

func TestReplay_Deterministic(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".wal")
	os.WriteFile(p, []byte("2026-04-01|inject|a.md|s0\n2026-04-05|trigger-useful|a.md|s1\n2026-04-05|inject|b.md|s1\n"), 0o644)
	events, _ := ParseWAL(p)
	a := Replay(events, []int{1, 2}, 7)
	b := Replay(events, []int{1, 2}, 7)
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("Replay not deterministic at K=%d: %+v vs %+v", a[i].K, a[i], b[i])
		}
	}
}
```

NOTE: this test file needs imports `"fmt"` and `"strings"` in addition to the existing ones — add them to the import block.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ab/ -run TestReplay -v`
Expected: FAIL — `undefined: Replay`.

- [ ] **Step 3: Write minimal implementation** `internal/ab/replay.go`

```go
package ab

import (
	"math/rand"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/rank"
)

// Result is the mean recall@K for one budget K across all eval sessions.
type Result struct {
	K            int
	RankedRecall float64
	RandomRecall float64
	Sessions     int
}

// Replay runs the temporal-holdout replay over every eval session and returns
// one Result per K (in the order of `ks`). `seed` makes the random baseline
// reproducible.
func Replay(events []Event, ks []int, seed int64) []Result {
	pool := CandidatePool(events)
	sessions := EvalSessions(events)
	rng := rand.New(rand.NewSource(seed))

	results := make([]Result, len(ks))
	for i, k := range ks {
		results[i] = Result{K: k}
	}

	for _, s := range sessions {
		if len(s.Useful) == 0 {
			continue
		}
		sig := SignalsBefore(events, s.Date)
		cands := make([]rank.Candidate, 0, len(pool))
		for _, slug := range pool {
			g := sig[slug]
			cands = append(cands, rank.Candidate{
				Slug:          slug,
				Status:        "active",
				RefCount:      g.RefCount,
				Effectiveness: float64(g.Pos+1) / float64(g.Pos+g.Neg+2),
				LastInjected:  g.LastInject,
				Overlap:       0, // historical prompt keywords unavailable
			})
		}
		q := rank.Query{Today: s.Date}
		for i, k := range ks {
			rankedSlugs := topSlugs(rank.Rank(q, cands, k))
			randomSlugs := randomPick(pool, k, rng)
			results[i].RankedRecall += recall(rankedSlugs, s.Useful)
			results[i].RandomRecall += recall(randomSlugs, s.Useful)
			results[i].Sessions++
		}
	}
	for i := range results {
		if results[i].Sessions > 0 {
			n := float64(results[i].Sessions)
			results[i].RankedRecall /= n
			results[i].RandomRecall /= n
		}
	}
	return results
}

func topSlugs(scored []rank.Scored) []string {
	out := make([]string, len(scored))
	for i := range scored {
		out[i] = scored[i].Slug
	}
	return out
}

// randomPick returns up to k distinct slugs from pool using rng (Fisher-Yates
// on a copy so the source pool order is preserved across sessions).
func randomPick(pool []string, k int, rng *rand.Rand) []string {
	cp := make([]string, len(pool))
	copy(cp, pool)
	rng.Shuffle(len(cp), func(i, j int) { cp[i], cp[j] = cp[j], cp[i] })
	if k < len(cp) {
		cp = cp[:k]
	}
	return cp
}

// recall = |selected ∩ useful| / |useful|.
func recall(selected []string, useful map[string]bool) float64 {
	if len(useful) == 0 {
		return 0
	}
	hit := 0
	for _, s := range selected {
		if useful[s] {
			hit++
		}
	}
	return float64(hit) / float64(len(useful))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ab/ -v`
Expected: PASS (Task 1 + Task 2 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/ab/replay.go internal/ab/replay_test.go
git commit -m "feat(ab): temporal-holdout replay, ranked vs random recall@K"
```

---

### Task 3: `memoryctl ab` verb

**Files:**
- Create: `cmd/memoryctl/ab.go`
- Modify: `cmd/memoryctl/main.go` (add `case "ab":`)
- Test: `cmd/memoryctl/ab_test.go`

**Context:** Run the harness over the live WAL (`<memoryDir>/.wal`) and print a recall table with the small-n caveat. Default K set {3,5,8,12}, default seed 1.

- [ ] **Step 1: Write the failing test** `cmd/memoryctl/ab_test.go`

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestABVerb(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wal := "2026-04-01|inject-agg|hot.md|50\n2026-04-01|outcome-positive|hot.md|s0\n"
	for i := 0; i < 10; i++ {
		wal += "2026-04-01|inject|cold" + string(rune('0'+i)) + ".md|s0\n"
	}
	wal += "2026-04-10|trigger-useful|hot.md|s1\n"
	if err := os.WriteFile(filepath.Join(memDir, ".wal"), []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"CLAUDE_MEMORY_DIR": memDir}

	out, errOut, code := run(t, env, "ab")
	if code != 0 {
		t.Fatalf("ab exit=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "recall") || !strings.Contains(out, "ranked") {
		t.Errorf("expected a recall table mentioning ranked; got:\n%s", out)
	}
	if !strings.Contains(out, "K=") {
		t.Errorf("expected per-K rows; got:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/memoryctl/ -run TestABVerb -v`
Expected: FAIL — unknown command `ab`.

- [ ] **Step 3a: Add dispatch in `cmd/memoryctl/main.go`**

```go
	case "ab":
		runAB(os.Args[2:])
```

- [ ] **Step 3b: Implement** `cmd/memoryctl/ab.go`

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/ab"
)

// runAB: memoryctl ab [--seed N]
// Replays the historical WAL and prints ranked-vs-random recall@K. A
// directional signal on whether ranking concentrates useful slugs — small-n
// by nature (compaction drops per-session inject linkage).
func runAB(args []string) {
	var seed int64 = 1
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--seed":
			i++
			if i < len(args) {
				fmt.Sscanf(args[i], "%d", &seed)
			}
		case "-h", "--help":
			fmt.Print(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "memoryctl ab: unknown flag %q\n", args[i])
			os.Exit(2)
		}
	}

	events, err := ab.ParseWAL(filepath.Join(memoryDir(), ".wal"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ab: %v\n", err)
		os.Exit(1)
	}
	results := ab.Replay(events, []int{3, 5, 8, 12}, seed)

	fmt.Println("A/B replay — recall@K, ranked (static signals) vs random baseline")
	fmt.Println("(temporal-holdout; Overlap=0 — prompt keywords unavailable historically;")
	fmt.Println(" small-n directional signal, not a verdict)")
	for _, r := range results {
		fmt.Printf("  K=%-3d ranked=%.3f  random=%.3f  (n=%d sessions)\n",
			r.K, r.RankedRecall, r.RandomRecall, r.Sessions)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/memoryctl/ -run TestABVerb -v`
Then full suite: `go build ./... && go test -race ./... && go vet ./...`
Expected: PASS, no regressions.

- [ ] **Step 5: Commit**

```bash
git add cmd/memoryctl/ab.go cmd/memoryctl/main.go cmd/memoryctl/ab_test.go
git commit -m "feat(memoryctl): ab verb — ranked vs random recall@K replay"
```

---

## Phase 2b Definition of Done

- [ ] `internal/ab` parses the WAL, extracts eval sessions (trigger-useful grouped by session, earliest date), aggregates temporal-holdout signals (strictly-before-date; inject-agg counted), builds the candidate pool.
- [ ] `Replay` ranks each session by static signals and reports mean recall@K for ranked vs seeded-random; deterministic for a fixed seed.
- [ ] `memoryctl ab` prints the recall table over the live WAL with the small-n / no-keywords caveat.
- [ ] `go test -race ./...` green; no hook behaviour changed.
- [ ] Running `memoryctl ab` on the maintainer's real WAL produces a directional read on whether static-signal ranking beats random (record the numbers in continuity; if ranked ≈ random, that is itself a finding worth the spec's attention).

## Self-Review (against spec §6.1 + methodology)

- **Spec §6.1 (A/B harness, first-class):** `internal/ab` + `memoryctl ab` replay the WAL, compare ranked vs baseline on the trigger-useful proxy. ✓
- **Leakage-free:** `SignalsBefore` uses `Date < cutoff` strictly; `TestSignalsBefore_TemporalHoldout` locks that on/after-date events are excluded. ✓
- **Determinism:** seeded `*rand.Rand`; `TestReplay_Deterministic` + sorted pool/sessions. ✓
- **Honest scope:** Overlap 0 documented in code + verb output; small-n caveat printed. ✓
- **Reuses real APIs:** `rank.Rank` consumed unchanged; inject-agg count handled identically to sidecar. ✓
- **Type consistency:** `Event`/`Signal`/`EvalSession`/`Result` used consistently across ab.go/replay.go; `rank.Candidate` mapping matches the verb mapping from Phase 2a. ✓
- **No placeholders:** complete code + commands throughout. ✓
- **Deferred:** keyword-overlap component not exercised historically (validated live, later phase); weight calibration consumes these numbers in a follow-up.
