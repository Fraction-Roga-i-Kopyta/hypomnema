# v2.0 Phase 5 — guard (secrets gate) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Checkbox steps.

**Goal:** `memoryctl guard` (PreToolUse:Write on the memory path): block writes that embed a plaintext credential, with the v1 escape hatches.

**Architecture:** `internal/secrets` ports the v1 detector (`Scan` + `.secretsignore` `IgnoreMatch`) to Go. `memoryctl guard` reads the PreToolUse stdin, scopes to memory writes, honours the bypasses, and exits 2 on a hit. Fail-open by construction (only a definite match blocks). `hooks/v2/pre-tool-write.sh` (Phase 3) already calls it.

**Tech Stack:** Go 1.25, `regexp` (RE2 supports `\b`), stdlib. Module `github.com/Fraction-Roga-i-Kopyta/hypomnema`.

## Grounding (v1 `hooks/memory-secrets-detect.sh` + hooks-contract §4 + spec §5)

- **PreToolUse stdin:** `{"tool_name","tool_input":{"file_path","content"|"new_string"}}`.
- **Scope:** only scan writes whose `file_path` is under the memory dir; everything else → exit 0.
- **Bypasses:** `HYPOMNEMA_ALLOW_SECRETS=1` (single-invocation) → exit 0; path matched by `.secretsignore.default` or `.secretsignore` (gitignore subset: `#` comments, `!` negate, `**` any-segments, `*` within-segment, relative to memory dir) → exit 0.
- **Detection:** strip fenced (```` ``` ````) + inline (`` `…` ``) code, then per-line regex `\b(api[_-]?key|apikey|aws[_-]?(?:access|secret)[_-]?key|secret|password|token)\s*[:=]\s*[^\s"'`+"`"+`]{8,}` (case-insensitive, value ≥8 chars). Hit → stderr report + exit 2.
- **Fail policy (spec §5):** fail-OPEN — the scanner has no error path that blocks; only a definite match exits 2. Bad stdin → exit 0.

**Scope boundary (deferred, noted):** the dedup half of guard. v1 dedup keys on a mistake's `root-cause` frontmatter field; native v2 files are minimal (name/description/type) and have no root-cause, so dedup-by-root-cause doesn't apply. Dedup for the native schema needs its own design — deferred. Phase 5 guard is the secrets gate only (the security baseline).

---

### Task 1: `internal/secrets` — Scan + IgnoreMatch

**Files:** Create `internal/secrets/secrets.go`, `internal/secrets/secrets_test.go`

- [ ] **Step 1: failing test** `internal/secrets/secrets_test.go`
```go
package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScan(t *testing.T) {
	if hits := Scan("api_key: sk_live_abcd1234efgh"); len(hits) == 0 {
		t.Error("expected a hit for api_key with a long value")
	}
	if hits := Scan("password = hunter2longenough"); len(hits) == 0 {
		t.Error("expected a hit for password")
	}
	// placeholder too short → no hit
	if hits := Scan("password: xxx"); len(hits) != 0 {
		t.Errorf("short placeholder must not hit: %v", hits)
	}
	// inside a fenced code block → ignored
	if hits := Scan("```\napi_key: sk_live_abcd1234efgh\n```"); len(hits) != 0 {
		t.Errorf("fenced secret must be ignored: %v", hits)
	}
	// inside inline code → ignored
	if hits := Scan("the field `api_key: sk_live_abcd1234efgh` is an example"); len(hits) != 0 {
		t.Errorf("inline-code secret must be ignored: %v", hits)
	}
	// plain prose, no credential → clean
	if hits := Scan("this note is about the token bucket algorithm and rate limits"); len(hits) != 0 {
		t.Errorf("'token' without a :/= value must not hit: %v", hits)
	}
}

func TestIgnoreMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".secretsignore.default"), []byte("seeds/**\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".secretsignore"), []byte("# user\ndocs/examples/*\n!docs/examples/real.md\n"), 0o644)

	if !IgnoreMatch(dir, "seeds/hazard.md") {
		t.Error("seeds/** should whitelist seeds/hazard.md")
	}
	if !IgnoreMatch(dir, "docs/examples/demo.md") {
		t.Error("docs/examples/* should whitelist demo.md")
	}
	if IgnoreMatch(dir, "docs/examples/real.md") {
		t.Error("!docs/examples/real.md should UN-whitelist it")
	}
	if IgnoreMatch(dir, "mistakes/x.md") {
		t.Error("unmatched path must not be whitelisted")
	}
}
```

- [ ] **Step 2: run, expect FAIL** — `go test ./internal/secrets/ -v`

- [ ] **Step 3: implement** `internal/secrets/secrets.go`
```go
// Package secrets is the v2 credential gate: it detects plaintext secrets in
// memory-file content (outside code blocks) and matches .secretsignore
// whitelists. Ported from hooks/memory-secrets-detect.sh.
package secrets

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var secretRe = regexp.MustCompile(`(?i)\b(api[_-]?key|apikey|aws[_-]?(?:access|secret)[_-]?key|secret|password|token)\s*[:=]\s*[^\s"'` + "`" + `]{8,}`)
var inlineCodeRe = regexp.MustCompile("`[^`]*`")

// Scan returns "line: fragment" hits for secret-looking tokens in content,
// outside fenced and inline code. An empty result means clean.
func Scan(content string) []string {
	var hits []string
	sc := bufio.NewScanner(strings.NewReader(content))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	inFence := false
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		line = inlineCodeRe.ReplaceAllString(line, "")
		if m := secretRe.FindString(line); m != "" {
			hits = append(hits, fmt.Sprintf("%d: %s", n, m))
		}
	}
	return hits
}

// IgnoreMatch reports whether relPath (relative to memoryDir) is whitelisted
// by .secretsignore.default or .secretsignore.
func IgnoreMatch(memoryDir, relPath string) bool {
	for _, name := range []string{".secretsignore.default", ".secretsignore"} {
		if matchIgnoreFile(filepath.Join(memoryDir, name), relPath) {
			return true
		}
	}
	return false
}

func matchIgnoreFile(path, rel string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	matched := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		negate := false
		if strings.HasPrefix(line, "!") {
			negate = true
			line = line[1:]
		}
		if globToRe(line).MatchString(rel) {
			matched = !negate
		}
	}
	return matched
}

// globToRe converts a gitignore-subset pattern to an anchored regexp: ** →
// any chars (incl. /), * → any chars except /, everything else literal.
func globToRe(glob string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		if strings.HasPrefix(glob[i:], "**") {
			b.WriteString(".*")
			i++ // consume the second '*'
			continue
		}
		if glob[i] == '*' {
			b.WriteString("[^/]*")
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(glob[i])))
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return regexp.MustCompile(`$^`) // matches nothing
	}
	return re
}
```

- [ ] **Step 4: run, expect PASS** — `go test ./internal/secrets/ -v`; `go vet ./internal/secrets/`
- [ ] **Step 5: commit** — `git add internal/secrets/ && git commit -m "feat(secrets): credential Scan + .secretsignore IgnoreMatch (ported from bash)"`

---

### Task 2: `memoryctl guard` verb

**Files:** Create `cmd/memoryctl/guard.go`, `cmd/memoryctl/guard_test.go`; modify `cmd/memoryctl/main.go`

- [ ] **Step 1: failing test** `cmd/memoryctl/guard_test.go`
```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func guardStdin(memDir, name, content string) string {
	fp := filepath.Join(memDir, name)
	// JSON-escape content minimally for the test (no quotes/newlines in our fixtures).
	return `{"tool_name":"Write","tool_input":{"file_path":"` + fp + `","content":"` + content + `"}}`
}

func TestGuard_BlocksSecret(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(memDir, 0o755)
	env := map[string]string{"CLAUDE_MEMORY_DIR": memDir}
	_, errOut, code := runStdin(t, env, guardStdin(memDir, "mistakes/x.md", "api_key: sk_live_abcd1234efgh"), "guard")
	if code != 2 {
		t.Fatalf("secret in memory write must block (exit 2), got %d", code)
	}
	if !strings.Contains(errOut, "Blocked") {
		t.Errorf("expected a Blocked message on stderr, got %q", errOut)
	}
}

func TestGuard_AllowsClean(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(memDir, 0o755)
	env := map[string]string{"CLAUDE_MEMORY_DIR": memDir}
	_, _, code := runStdin(t, env, guardStdin(memDir, "mistakes/x.md", "a clean note about rate limits"), "guard")
	if code != 0 {
		t.Errorf("clean content must allow (exit 0), got %d", code)
	}
}

func TestGuard_OutsideMemoryDirAllowed(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(memDir, 0o755)
	env := map[string]string{"CLAUDE_MEMORY_DIR": memDir}
	// secret, but path is NOT under the memory dir → out of scope → allow
	stdin := `{"tool_name":"Write","tool_input":{"file_path":"/tmp/code.py","content":"api_key: sk_live_abcd1234efgh"}}`
	_, _, code := runStdin(t, env, stdin, "guard")
	if code != 0 {
		t.Errorf("non-memory write must be out of scope (exit 0), got %d", code)
	}
}

func TestGuard_AllowEnvBypass(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(memDir, 0o755)
	env := map[string]string{"CLAUDE_MEMORY_DIR": memDir, "HYPOMNEMA_ALLOW_SECRETS": "1"}
	_, _, code := runStdin(t, env, guardStdin(memDir, "seeds/hazard.md", "api_key: sk_live_abcd1234efgh"), "guard")
	if code != 0 {
		t.Errorf("HYPOMNEMA_ALLOW_SECRETS=1 must bypass (exit 0), got %d", code)
	}
}

func TestGuard_SecretsignoreWhitelist(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(memDir, 0o755)
	os.WriteFile(filepath.Join(memDir, ".secretsignore"), []byte("seeds/**\n"), 0o644)
	env := map[string]string{"CLAUDE_MEMORY_DIR": memDir}
	_, _, code := runStdin(t, env, guardStdin(memDir, "seeds/hazard.md", "api_key: sk_live_abcd1234efgh"), "guard")
	if code != 0 {
		t.Errorf("seeds/** whitelist must allow (exit 0), got %d", code)
	}
}

func TestGuard_FailSafeBadStdin(t *testing.T) {
	env := map[string]string{}
	_, _, code := runStdin(t, env, "not json", "guard")
	if code != 0 {
		t.Errorf("bad stdin must exit 0, got %d", code)
	}
}
```

- [ ] **Step 2: run, expect FAIL** — `go test ./cmd/memoryctl/ -run TestGuard -v`

- [ ] **Step 3a: dispatch** in `cmd/memoryctl/main.go`: `case "guard":` → `runGuard(os.Args[2:])`

- [ ] **Step 3b: implement** `cmd/memoryctl/guard.go`
```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/secrets"
)

// runGuard: memoryctl guard — PreToolUse(Write). Blocks (exit 2) a memory
// write that embeds a plaintext credential. Fail-open: anything uncertain
// → exit 0 (allow). The only non-zero exit is a definite secret hit.
func runGuard(args []string) {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Print(usage)
			return
		}
		fmt.Fprintf(os.Stderr, "memoryctl guard: unknown flag %q\n", a)
		os.Exit(2)
	}
	raw, _ := io.ReadAll(os.Stdin)
	var in struct {
		ToolInput struct {
			FilePath  string `json:"file_path"`
			Content   string `json:"content"`
			NewString string `json:"new_string"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		os.Exit(0) // fail-open
	}
	if os.Getenv("HYPOMNEMA_ALLOW_SECRETS") == "1" {
		os.Exit(0)
	}
	fp := in.ToolInput.FilePath
	if fp == "" {
		os.Exit(0)
	}
	memDir := memoryDir()
	if fp != memDir && !strings.HasPrefix(fp, memDir+"/") {
		os.Exit(0) // out of scope — not a memory write
	}
	rel := strings.TrimPrefix(fp, memDir+"/")
	if secrets.IgnoreMatch(memDir, rel) {
		os.Exit(0)
	}
	content := in.ToolInput.Content
	if content == "" {
		content = in.ToolInput.NewString
	}
	if content == "" {
		os.Exit(0)
	}
	hits := secrets.Scan(content)
	if len(hits) == 0 {
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "Blocked: %s would write a plaintext secret-looking token.\n", fp)
	fmt.Fprintln(os.Stderr, "Matches (line : fragment):")
	for _, h := range hits {
		fmt.Fprintf(os.Stderr, "  %s\n", h)
	}
	fmt.Fprintln(os.Stderr, "If intentional, set HYPOMNEMA_ALLOW_SECRETS=1 for this single invocation and retry.")
	os.Exit(2)
}
```

- [ ] **Step 4: run, expect PASS** — `go test ./cmd/memoryctl/ -run TestGuard -v`; then `go build ./... && go test -race ./... && go vet ./...`
- [ ] **Step 5: commit** — `git add cmd/memoryctl/guard.go cmd/memoryctl/main.go cmd/memoryctl/guard_test.go && git commit -m "feat(memoryctl): guard verb — secrets gate (PreToolUse)"`

---

## Phase 5 Definition of Done

- [ ] `internal/secrets.Scan` detects credential patterns outside code fences/inline code (value ≥8); `IgnoreMatch` honours `.secretsignore[.default]` (`**`/`!` subset).
- [ ] `memoryctl guard` blocks (exit 2 + stderr) a memory-dir write with a secret; allows (exit 0) clean / out-of-scope / bypassed / whitelisted / bad-stdin. Fail-open.
- [ ] `hooks/v2/pre-tool-write.sh` (Phase 3) drives a real verb (exit 2 preserved).
- [ ] `go test -race ./...` green.

## Self-Review (against v1 detector + contract §4 + spec §5)

- **Detection parity:** same regex classes (api_key/secret/password/token/aws), fence + inline-code stripping, ≥8-char value floor — ported faithfully from `hooks/memory-secrets-detect.sh:98-111`. ✓
- **Scope + bypasses:** memory-dir-only; `HYPOMNEMA_ALLOW_SECRETS=1`; `.secretsignore[.default]` with `**`/`!`. ✓
- **Fail-open (spec §5):** only a definite hit exits 2; bad stdin / no path / no content / scanner non-match → exit 0. No panic surface (RE2, no MustCompile on dynamic input except globToRe which falls back to a never-match regex). ✓
- **Contract §4:** exit 2 blocks + stderr surfaced; PreToolUse envelope `tool_input.{file_path,content|new_string}`. ✓
- **Deferred (noted):** dedup half of guard (native files lack root-cause; needs redesign). RE2 has no `\b`-at-unicode subtlety here (ASCII keywords).
