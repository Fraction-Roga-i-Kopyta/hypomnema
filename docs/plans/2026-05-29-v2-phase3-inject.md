# v2.0 Phase 3 — Inject path + bash shims Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Wire the ranker into a live injection path — `memoryctl inject` reads the hook stdin JSON, ranks native memory, and emits the `hookSpecificOutput` envelope — plus four thin bash shims that the installer will register.

**Architecture:** `internal/inject` owns the orchestration (keyword extraction → native.List → sidecar/rank → markdown), with sidecar corruption-recovery + a degraded fallback (spec §5). `memoryctl inject --event=<E>` parses stdin JSON, runs the orchestrator, writes `inject` WAL events + the session injected-set, and prints the envelope (fail-safe: exit 0, empty envelope on any error). Shims are ~3-line `exec` wrappers. **Nothing here touches the user's live `~/.claude/settings.json`** — registration is Phase 6 (install/upgrade).

**Tech Stack:** Go 1.25, `encoding/json` for the hook envelope, stdlib testing. Module `github.com/Fraction-Roga-i-Kopyta/hypomnema`.

## Grounding (from `docs/hooks-contract.md`)

- **stdin** (SessionStart): `{"session_id","cwd"}`; (UserPromptSubmit): `{"session_id","prompt","cwd"}`.
- **stdout:** `{"hookSpecificOutput":{"hookEventName":"<Event>","additionalContext":"<markdown>"}}`. SessionStart markdown starts `# Memory Context`.
- **Exit 0 always** (inject never blocks). Empty `additionalContext` is valid.
- **Side effects:** `inject` WAL events; `.runtime/injected-<session_id>.list` (mode 0600).

**Scope boundary:** Keywords this phase = prompt tokens + cwd-basename tokens (git-signal enrichment — branch/diff/commits — is an additive refinement, noted, not built now). Project/domain ranking signals stay inert until migration (Phase 6) populates them. The sidecar is opened as-is (kept fresh by Stop/rebuild); inject reprojects only when the sidecar is missing/corrupt, never on every call (latency).

---

### Task 1: `internal/inject` — keyword extraction

**Files:**
- Create: `internal/inject/keywords.go`
- Test: `internal/inject/keywords_test.go`

- [ ] **Step 1: Write the failing test** `internal/inject/keywords_test.go`

```go
package inject

import (
	"sort"
	"testing"
)

func TestKeywords(t *testing.T) {
	got := Keywords("/home/user/dev/my-project", "help with docker cache layer")
	set := map[string]bool{}
	for _, k := range got {
		set[k] = true
	}
	// prompt tokens present
	for _, want := range []string{"docker", "cache", "layer", "help", "with"} {
		if !set[want] {
			t.Errorf("expected prompt token %q in %v", want, got)
		}
	}
	// cwd basename tokenized ("my-project" → "project"; "my" is < 3 runes? "my"=2 dropped)
	if !set["project"] {
		t.Errorf("expected basename token 'project' in %v", got)
	}
	// deduped
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			t.Errorf("duplicate token %q", sorted[i])
		}
	}
}

func TestKeywords_EmptyPrompt(t *testing.T) {
	got := Keywords("/tmp/alpha-svc", "")
	set := map[string]bool{}
	for _, k := range got {
		set[k] = true
	}
	if !set["alpha"] && !set["svc"] {
		t.Errorf("expected basename tokens from /tmp/alpha-svc, got %v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/inject/ -v`
Expected: FAIL — `undefined: Keywords`.

- [ ] **Step 3: Write minimal implementation** `internal/inject/keywords.go`

```go
// Package inject is the v2 injection orchestrator: it turns a hook's
// (cwd, prompt) into a ranked Markdown context block over native memory.
package inject

import (
	"path/filepath"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/tokenize"
)

// Keywords derives the ranking query terms from the prompt (the reactive
// signal) and the cwd basename (a weak project hint). Deduped, lowercased,
// Unicode-tokenized. Git-signal enrichment (branch/diff/commits) is a future
// additive refinement and intentionally not included here.
func Keywords(cwd, prompt string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(toks []string) {
		for _, t := range toks {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	add(tokenize.Tokenize(prompt, nil))
	add(tokenize.Tokenize(filepath.Base(cwd), nil))
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/inject/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/inject/keywords.go internal/inject/keywords_test.go
git commit -m "feat(inject): keyword extraction (prompt + cwd basename)"
```

---

### Task 2: `internal/inject` — orchestrator with corruption-recovery + degraded fallback

**Files:**
- Create: `internal/inject/inject.go`
- Test: `internal/inject/inject_test.go`

**Context:** `Run` is the orchestrator. It opens the sidecar (recovering from corruption by rebuilding; falling back to a degraded native-only rank if the sidecar is unusable), lists native files (project + global dirs), ranks, and renders the `# Memory Context` Markdown. It returns the markdown + the list of injected slugs (the verb persists WAL + runtime state). Resolves spec §5's corruption-recovery deferred from Phase 1.

- [ ] **Step 1: Write the failing test** `internal/inject/inject_test.go`

```go
package inject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T) (memDir, projDir, home string) {
	t.Helper()
	home = t.TempDir()
	memDir = filepath.Join(home, ".claude", "memory")
	projDir = filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return memDir, projDir, home
}

func TestRun_RanksAndRenders(t *testing.T) {
	memDir, projDir, home := setup(t)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker cache\ntype: mistake\n---\ndocker layer cache stale\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "sql.md"),
		[]byte("---\nname: SQL index\ntype: knowledge\n---\npostgres index plan\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)

	res, err := Run(Input{
		Event: "UserPromptSubmit", SessionID: "s1",
		CWD: "/tmp/proj", Prompt: "fix the docker cache",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 5,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasPrefix(res.Markdown, "# Memory Context") {
		t.Errorf("markdown must start with the context header, got:\n%s", res.Markdown)
	}
	if !strings.Contains(res.Markdown, "docker") {
		t.Errorf("docker memory should be injected for a docker prompt:\n%s", res.Markdown)
	}
	// docker.md is more relevant → injected; assert it's in the injected set
	var hasDocker bool
	for _, s := range res.Injected {
		if s == "docker.md" {
			hasDocker = true
		}
	}
	if !hasDocker {
		t.Errorf("docker.md should be in Injected: %v", res.Injected)
	}
}

func TestRun_EmptyCorpus(t *testing.T) {
	memDir, _, home := setup(t)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	res, err := Run(Input{
		Event: "SessionStart", SessionID: "s1", CWD: "/tmp/proj",
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir,
		Today: "2026-05-29", MaxK: 5,
	})
	if err != nil {
		t.Fatalf("Run on empty corpus should not error: %v", err)
	}
	// empty corpus → header only (or empty), no panic
	if res.Markdown != "" && !strings.HasPrefix(res.Markdown, "# Memory Context") {
		t.Errorf("empty corpus markdown unexpected: %q", res.Markdown)
	}
	if len(res.Injected) != 0 {
		t.Errorf("empty corpus should inject nothing, got %v", res.Injected)
	}
}

func TestRun_DegradedOnCorruptSidecar(t *testing.T) {
	memDir, projDir, home := setup(t)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker\ntype: mistake\n---\ndocker cache\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	// Plant a corrupt sidecar DB file.
	if err := os.WriteFile(filepath.Join(memDir, ".sidecar.db"), []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Run(Input{
		Event: "UserPromptSubmit", SessionID: "s1", CWD: "/tmp/proj",
		Prompt: "docker", ClaudeHome: filepath.Join(home, ".claude"),
		MemoryDir: memDir, Today: "2026-05-29", MaxK: 5,
	})
	if err != nil {
		t.Fatalf("Run must recover from a corrupt sidecar, not error: %v", err)
	}
	if !strings.Contains(res.Markdown, "docker") {
		t.Errorf("after recovery, docker memory should still inject:\n%s", res.Markdown)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/inject/ -run TestRun -v`
Expected: FAIL — `undefined: Run` / `undefined: Input`.

- [ ] **Step 3: Write minimal implementation** `internal/inject/inject.go`

```go
package inject

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/rank"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/tokenize"
)

// Input is everything Run needs (parsed by the memoryctl inject verb).
type Input struct {
	Event      string
	SessionID  string
	CWD        string
	Prompt     string
	ClaudeHome string // <home>/.claude
	MemoryDir  string
	Today      string
	MaxK       int
}

// Result is the rendered context plus the slugs injected (for WAL + runtime).
type Result struct {
	Markdown string
	Injected []string
}

// Run orchestrates a single injection: keywords → native files → ranked
// top-K → Markdown. Never panics on a bad corpus; recovers from a corrupt
// sidecar by rebuilding, and falls back to a native-only degraded rank if
// the sidecar is unusable.
func Run(in Input) (Result, error) {
	osHome := filepath.Dir(in.ClaudeHome) // ClaudeHome == <home>/.claude
	files, err := collectNative(osHome, in.CWD)
	if err != nil {
		return Result{}, fmt.Errorf("inject.Run: native: %w", err)
	}
	if len(files) == 0 {
		return Result{}, nil // empty corpus → no context
	}
	bySlug := map[string]native.MemFile{}
	for _, f := range files {
		bySlug[f.Slug] = f
	}
	terms := Keywords(in.CWD, in.Prompt)

	cands := candidates(in, files, terms)

	if in.MaxK <= 0 {
		in.MaxK = 8
	}
	ranked := rank.Rank(rank.Query{Terms: terms, Today: in.Today}, cands, in.MaxK)

	injected := make([]string, 0, len(ranked))
	for _, sc := range ranked {
		injected = append(injected, sc.Slug)
	}
	return Result{Markdown: render(ranked, bySlug), Injected: injected}, nil
}

func collectNative(osHome, cwd string) ([]native.MemFile, error) {
	proj, err := native.List(native.ProjectMemoryDir(osHome, cwd))
	if err != nil {
		return nil, err
	}
	glob, err := native.List(native.GlobalMemoryDir(osHome))
	if err != nil {
		return nil, err
	}
	return append(proj, glob...), nil
}

// candidates builds the rank candidates, preferring sidecar metadata but
// degrading to native-only (overlap computed in-memory) if the sidecar is
// unusable. Corrupt sidecar files are unlinked and rebuilt.
func candidates(in Input, files []native.MemFile, terms []string) []rank.Candidate {
	sidePath := filepath.Join(in.MemoryDir, ".sidecar.db")
	walPath := filepath.Join(in.MemoryDir, ".wal")

	s, err := sidecar.Open(sidePath)
	if err != nil {
		// Corrupt/foreign DB — remove and retry once.
		_ = os.Remove(sidePath)
		s, err = sidecar.Open(sidePath)
	}
	if err == nil {
		defer s.Close()
		// Ensure it has content; rebuild if empty (fresh install / wiped).
		recs, lerr := s.All()
		if lerr == nil && len(recs) == 0 {
			_ = sidecar.Reproject(s, files, walPath)
			recs, lerr = s.All()
		}
		if lerr == nil {
			overlap, _ := s.OverlapScores(terms)
			out := make([]rank.Candidate, 0, len(recs))
			for _, r := range recs {
				out = append(out, rank.Candidate{
					Slug: r.Slug, Type: r.Type, Project: r.Project,
					Domains: splitCSV(r.Domains), RefCount: r.RefCount,
					Effectiveness: r.Effectiveness, Status: r.Status,
					Created: r.Created, LastInjected: r.LastInjected,
					Overlap: overlap[r.Slug],
				})
			}
			return out
		}
	}
	// Degraded: native-only, overlap computed in memory, neutral priors.
	return degradedCandidates(files, terms)
}

func degradedCandidates(files []native.MemFile, terms []string) []rank.Candidate {
	want := map[string]bool{}
	for _, t := range terms {
		want[t] = true
	}
	out := make([]rank.Candidate, 0, len(files))
	for _, f := range files {
		seen := map[string]bool{}
		overlap := 0
		for _, tok := range tokenize.Tokenize(f.Name+" "+f.Description+" "+f.Body, nil) {
			if want[tok] && !seen[tok] {
				seen[tok] = true
				overlap++
			}
		}
		out = append(out, rank.Candidate{
			Slug: f.Slug, Type: f.Type, Status: "active",
			Effectiveness: 0.5, Overlap: overlap,
		})
	}
	return out
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// render produces the SessionStart/UserPromptSubmit Markdown context block.
func render(ranked []rank.Scored, bySlug map[string]native.MemFile) string {
	if len(ranked) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Memory Context\n")
	for _, sc := range ranked {
		f := bySlug[sc.Slug]
		title := f.Name
		if title == "" {
			title = sc.Slug
		}
		fmt.Fprintf(&b, "\n## %s\n", title)
		if f.Body != "" {
			b.WriteString(f.Body)
			b.WriteString("\n")
		}
	}
	return b.String()
}

var _ = sort.Strings // reserved for future stable ordering needs
```

NOTE: drop the `var _ = sort.Strings` line and the `"sort"` import if the implementer finds them unused after writing — they are only a placeholder to avoid an unused-import error if `sort` ends up referenced. Prefer removing both if `sort` is not used.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/inject/ -v`
Expected: PASS (keywords + Run + degraded + empty-corpus).

- [ ] **Step 5: Commit**

```bash
git add internal/inject/inject.go internal/inject/inject_test.go
git commit -m "feat(inject): orchestrator with sidecar corruption-recovery + degraded fallback"
```

---

### Task 3: `memoryctl inject` verb

**Files:**
- Create: `cmd/memoryctl/inject.go`
- Modify: `cmd/memoryctl/main.go` (add `case "inject":`)
- Test: `cmd/memoryctl/inject_test.go`

**Context:** Reads the hook stdin JSON, runs `inject.Run`, writes `inject` WAL events + the session injected-set, and prints the `hookSpecificOutput` envelope. Fail-safe: any internal error → print an empty envelope and exit 0 (never break the session).

- [ ] **Step 1: Write the failing test** `cmd/memoryctl/inject_test.go`

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectVerb(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	os.MkdirAll(projDir, 0o755)
	os.MkdirAll(memDir, 0o755)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker cache\ntype: mistake\n---\ndocker layer cache\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)

	env := map[string]string{
		"CLAUDE_HOME": filepath.Join(home, ".claude"), "CLAUDE_MEMORY_DIR": memDir,
		"HYPOMNEMA_TODAY": "2026-05-29",
	}
	stdin := `{"session_id":"s1","cwd":"/tmp/proj","prompt":"fix docker cache"}`
	out, errOut, code := runStdin(t, env, stdin, "inject", "--event=UserPromptSubmit")
	if code != 0 {
		t.Fatalf("inject exit=%d stderr=%s", code, errOut)
	}
	// stdout must be a valid hookSpecificOutput envelope
	var env2 struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &env2); err != nil {
		t.Fatalf("stdout is not valid envelope JSON: %v\n%s", err, out)
	}
	if env2.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Errorf("hookEventName = %q", env2.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(env2.HookSpecificOutput.AdditionalContext, "docker") {
		t.Errorf("expected docker memory in context: %q", env2.HookSpecificOutput.AdditionalContext)
	}
	// WAL got an inject event for docker.md
	wal, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if !strings.Contains(string(wal), "|inject|docker.md|s1") {
		t.Errorf("expected inject WAL event, got:\n%s", wal)
	}
	// runtime injected-set written
	if _, err := os.Stat(filepath.Join(memDir, ".runtime", "injected-s1.list")); err != nil {
		t.Errorf("injected-set file missing: %v", err)
	}
}

func TestInjectVerb_FailSafeOnBadStdin(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(memDir, 0o755)
	env := map[string]string{"CLAUDE_HOME": filepath.Join(home, ".claude"), "CLAUDE_MEMORY_DIR": memDir}
	out, _, code := runStdin(t, env, "not json at all", "inject", "--event=SessionStart")
	if code != 0 {
		t.Fatalf("bad stdin must exit 0 (fail-safe), got %d", code)
	}
	// Must still print a valid (empty-context) envelope or nothing — not garbage.
	if strings.TrimSpace(out) != "" {
		var v map[string]any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Errorf("fail-safe output must be empty or valid JSON, got: %q", out)
		}
	}
}
```

This test needs a stdin-capable runner. Add it to `cmd/memoryctl/main_test.go` (or a shared test helper file):

```go
// runStdin is like run() but feeds `stdin` to the process.
func runStdin(t *testing.T, env map[string]string, stdin string, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	cmd.Env = append(os.Environ(), "GOCOVERDIR="+coverDir)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("runStdin: %v\nstderr: %s", err, errBuf.String())
	}
	return outBuf.String(), errBuf.String(), exit
}
```

(If `main_test.go` already imports `bytes`/`strings`/`exec`/`os`, reuse them; otherwise add to its import block. Place `runStdin` next to the existing `run` helper.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/memoryctl/ -run TestInjectVerb -v`
Expected: FAIL — unknown command `inject`.

- [ ] **Step 3a: Add dispatch in `cmd/memoryctl/main.go`**

```go
	case "inject":
		runInject(os.Args[2:])
```

- [ ] **Step 3b: Implement** `cmd/memoryctl/inject.go`

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/inject"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/wal"
)

type hookStdin struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	Prompt    string `json:"prompt"`
}

// runInject: memoryctl inject --event=SessionStart|UserPromptSubmit
// Reads the hook envelope from stdin, ranks memory, prints the
// hookSpecificOutput envelope. Fail-safe: exit 0 on any error.
func runInject(args []string) {
	event := "SessionStart"
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--event="):
			event = strings.TrimPrefix(a, "--event=")
		case a == "-h" || a == "--help":
			fmt.Print(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "memoryctl inject: unknown flag %q\n", a)
			os.Exit(2)
		}
	}

	raw, _ := io.ReadAll(os.Stdin)
	var in hookStdin
	if err := json.Unmarshal(raw, &in); err != nil {
		// Fail-safe: bad stdin → no context, exit 0.
		os.Exit(0)
	}

	res, err := inject.Run(inject.Input{
		Event: event, SessionID: in.SessionID, CWD: in.CWD, Prompt: in.Prompt,
		ClaudeHome: claudeDir(), MemoryDir: memoryDir(), Today: today(), MaxK: 8,
	})
	if err != nil {
		os.Exit(0) // fail-safe
	}

	// Persist WAL inject events + the session injected-set (best-effort).
	if len(res.Injected) > 0 && in.SessionID != "" {
		persistInjected(res.Injected, in.SessionID)
	}

	emitEnvelope(event, res.Markdown)
	os.Exit(0)
}

func persistInjected(slugs []string, sessionID string) {
	day := today()
	for _, slug := range slugs {
		line := fmt.Sprintf("%s|inject|%s|%s", day, slug, sessionID)
		wal.Append(memoryDir(), line, line)
	}
	runtimeDir := filepath.Join(memoryDir(), ".runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return
	}
	listPath := filepath.Join(runtimeDir, "injected-"+sessionID+".list")
	_ = os.WriteFile(listPath, []byte(strings.Join(slugs, "\n")+"\n"), 0o600)
}

func emitEnvelope(event, markdown string) {
	type hookOut struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	}
	env := struct {
		HookSpecificOutput hookOut `json:"hookSpecificOutput"`
	}{HookSpecificOutput: hookOut{HookEventName: event, AdditionalContext: markdown}}
	b, err := json.Marshal(env)
	if err != nil {
		return
	}
	fmt.Println(string(b))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/memoryctl/ -run TestInjectVerb -v`
Then full suite: `go build ./... && go test -race ./... && go vet ./...`
Expected: PASS, no regressions.

- [ ] **Step 5: Commit**

```bash
git add cmd/memoryctl/inject.go cmd/memoryctl/main.go cmd/memoryctl/inject_test.go cmd/memoryctl/main_test.go
git commit -m "feat(memoryctl): inject verb — stdin JSON to hookSpecificOutput envelope"
```

---

### Task 4: thin bash shims

**Files:**
- Create: `hooks/v2/session-start.sh`, `hooks/v2/user-prompt-submit.sh`, `hooks/v2/pre-tool-write.sh`, `hooks/v2/session-stop.sh`
- Test: `hooks/v2/shims_test.sh` (bash) + a Go wrapper test `cmd/memoryctl/shim_test.go` is NOT needed; use a bash smoke test invoked from the Makefile later.

**Context:** Each shim is a tiny `sh` wrapper that pipes stdin to `memoryctl` and relays stdout/exit. They reference `${HYPOMNEMA_MEMORYCTL:-memoryctl}` so tests can point at a built binary, and fail-safe (exit 0) if the binary is absent. `pre-tool-write` and `session-stop` call verbs built in later phases (`guard`, `close`) — their shims are created now but those verbs land in Phase 4/5; the shim for an unbuilt verb still must exit 0 (memoryctl will print an unknown-command error to stderr and exit 2, so the shim maps any inject/guard/close failure to exit 0 EXCEPT pre-tool-write which must preserve exit 2 for blocking).

- [ ] **Step 1: Write the failing test** `hooks/v2/shims_test.sh`

```sh
#!/usr/bin/env bash
# Smoke test for the v2 thin shims. Requires a built memoryctl; the test
# points HYPOMNEMA_MEMORYCTL at it. Run: bash hooks/v2/shims_test.sh <memoryctl-path>
set -u
MCTL="${1:?usage: shims_test.sh <memoryctl-path>}"
DIR="$(cd "$(dirname "$0")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/.claude/memory" "$TMP/.claude/projects/-tmp-proj/memory"
printf '%s' '---
name: Docker cache
type: mistake
---
docker cache layer
' > "$TMP/.claude/projects/-tmp-proj/memory/docker.md"
: > "$TMP/.claude/memory/.wal"

out=$(printf '%s' '{"session_id":"s1","cwd":"/tmp/proj","prompt":"docker"}' \
  | HYPOMNEMA_MEMORYCTL="$MCTL" CLAUDE_HOME="$TMP/.claude" CLAUDE_MEMORY_DIR="$TMP/.claude/memory" \
    bash "$DIR/session-start.sh")
echo "$out" | grep -q 'hookSpecificOutput' || { echo "FAIL: session-start.sh no envelope: $out"; exit 1; }

# Missing binary → fail-safe exit 0, no output
printf '%s' '{"session_id":"s1","cwd":"/tmp"}' \
  | HYPOMNEMA_MEMORYCTL=/nonexistent/memoryctl bash "$DIR/session-start.sh"
[ $? -eq 0 ] || { echo "FAIL: missing binary must exit 0"; exit 1; }

echo "PASS"
```

- [ ] **Step 2: Run to verify it fails**

Run: `bash hooks/v2/shims_test.sh /tmp/memoryctl-v2` (build first: `go build -o /tmp/memoryctl-v2 ./cmd/memoryctl`)
Expected: FAIL — shim files don't exist yet.

- [ ] **Step 3: Write the shims**

`hooks/v2/session-start.sh`:
```sh
#!/usr/bin/env sh
# v2 thin shim: SessionStart. Pipes the hook envelope to memoryctl inject
# and relays its stdout. Fail-safe: if memoryctl is absent, exit 0 silently.
MCTL="${HYPOMNEMA_MEMORYCTL:-memoryctl}"
command -v "$MCTL" >/dev/null 2>&1 || exit 0
exec "$MCTL" inject --event=SessionStart
```

`hooks/v2/user-prompt-submit.sh`:
```sh
#!/usr/bin/env sh
# v2 thin shim: UserPromptSubmit.
MCTL="${HYPOMNEMA_MEMORYCTL:-memoryctl}"
command -v "$MCTL" >/dev/null 2>&1 || exit 0
exec "$MCTL" inject --event=UserPromptSubmit
```

`hooks/v2/pre-tool-write.sh`:
```sh
#!/usr/bin/env sh
# v2 thin shim: PreToolUse(Write). Delegates to `memoryctl guard` (Phase 5).
# Preserves guard's exit code (2 = block); fail-safe to 0 if absent.
MCTL="${HYPOMNEMA_MEMORYCTL:-memoryctl}"
command -v "$MCTL" >/dev/null 2>&1 || exit 0
exec "$MCTL" guard
```

`hooks/v2/session-stop.sh`:
```sh
#!/usr/bin/env sh
# v2 thin shim: Stop. Delegates to `memoryctl close` (Phase 4).
MCTL="${HYPOMNEMA_MEMORYCTL:-memoryctl}"
command -v "$MCTL" >/dev/null 2>&1 || exit 0
exec "$MCTL" close
```

Make them executable: `chmod +x hooks/v2/*.sh`.

- [ ] **Step 4: Run to verify it passes**

Run: `go build -o /tmp/memoryctl-v2 ./cmd/memoryctl && bash hooks/v2/shims_test.sh /tmp/memoryctl-v2`
Expected: `PASS`.
(`pre-tool-write.sh`/`session-stop.sh` call not-yet-built verbs; that's fine — they're not exercised by this smoke test, which only drives session-start.sh. They will be covered when guard/close land.)

- [ ] **Step 5: Commit**

```bash
git add hooks/v2/
git commit -m "feat(hooks): v2 thin shims (session-start, user-prompt-submit, pre-tool-write, session-stop)"
```

---

## Phase 3 Definition of Done

- [ ] `internal/inject` extracts keywords (prompt + basename) and orchestrates native→sidecar→rank→Markdown, with corruption-recovery (rebuild on corrupt sidecar) + degraded native-only fallback (spec §5).
- [ ] `memoryctl inject --event=<E>` parses stdin JSON, prints a valid `hookSpecificOutput` envelope, writes `inject` WAL events + `.runtime/injected-<session>.list`, exit 0 always (fail-safe on bad stdin).
- [ ] Four `hooks/v2/*.sh` thin shims pipe stdin→memoryctl, relay stdout/exit, fail-safe when the binary is absent.
- [ ] `go test -race ./...` green; the user's live `~/.claude/settings.json` is NOT modified.

## Self-Review (against hooks-contract.md + spec §3.4/§4.1/§5)

- **Contract envelope:** verb prints `{"hookSpecificOutput":{"hookEventName","additionalContext"}}`; SessionStart markdown starts `# Memory Context`. ✓
- **Fail-safe (contract §1.3):** bad stdin / Run error → exit 0, empty/no envelope; `TestInjectVerb_FailSafeOnBadStdin` locks it. ✓
- **Corruption-recovery (spec §5):** `candidates` unlinks + reopens a corrupt sidecar, then degrades to native-only; `TestRun_DegradedOnCorruptSidecar` locks it. ✓
- **Side effects (contract §2.3):** `inject` WAL events + `.runtime/injected-<session>.list` mode 0600. ✓
- **No live settings mutation:** shims are files; registration deferred to Phase 6. ✓
- **Type consistency:** `inject.Input`/`Result` used by the verb; `rank.Candidate` mapping matches Phase 2a; `sidecar`/`native` APIs consumed unchanged. ✓
- **Deferred:** git-signal keywords; per-type quotas (Phase 3 caps at MaxK=8 flat); project/domain boosts inert until migration; UserPromptSubmit dedup-vs-SessionStart-set (the `.runtime` list is written here; the dedup READ is wired when both events run live — note for Phase 6 integration testing).
