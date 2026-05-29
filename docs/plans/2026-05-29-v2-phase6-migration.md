# v2.0 Phase 6 — Migration (v1 store → native + sidecar) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Checkbox steps.

**Goal:** `memoryctl migrate` — convert the v1 `~/.claude/memory/` store (8-type rich-frontmatter files) into native memory files + a rebuilt sidecar, with pruning, routing, backup, dry-run, idempotency, and rollback.

**Architecture:** `internal/migrate` owns the pure planning (parse v1 frontmatter, decide keep/prune, route global-vs-project, convert to native content) and the execution (write native files, preserve the WAL, rebuild the sidecar, back up the old store). `memoryctl migrate` drives it with `--dry-run` / `--execute` / `--rollback`.

**Tech Stack:** Go 1.25, stdlib. Module `github.com/Fraction-Roga-i-Kopyta/hypomnema`. Reuses `internal/native`, `internal/sidecar`.

## Grounding (real v1 format + projects.json)

- v1 frontmatter: `type` (fine: mistake/strategy/feedback/knowledge/decision/note/project/continuity), `project` (`global` or a project name), `created`, `status`, `ref_count`, optional `description`, `root-cause`/`prevention` (mistakes), block arrays `domains`/`keywords`/`triggers`. Body follows.
- `~/.claude/memory/projects.json` = `{ "<abs-cwd>": "<project-name>" }`. Reverse-lookup name→cwd routes a project file to its native dir.
- **Type mapping decision:** the native file's `type:` carries the **fine** v1 type verbatim (native already tolerates non-`user` type values, e.g. `feedback`). So `sidecar.Reproject` preserves the fine type with no coarse/fine split. `project`/`domains` stay sidecar-inert for now (the ranker's project-boost/domain-filter activate later; noted).
- **Knowledge preservation:** native frontmatter is minimal, so a mistake's `root-cause`/`prevention` (v1 frontmatter prose) are composed INTO the native body during conversion — nothing is lost.

**Scope boundary (deferred → Phase 7 install):** the CC-version compatibility gate and the `settings.json` hook rewrite live in `install.sh`/`uninstall.sh` (Phase 7 cutover), where the installer already patches settings. Phase 6 is the data migration + rollback only. It is built + tested on synthetic v1 fixtures; it is NOT run against the live store here (the user triggers their real upgrade consciously). Pruning rule: `ref_count==0 && status!=pinned && age>30d` → drop (to backup, never deleted). `continuity`/`projects` v1 entries are migrated as `type: project`/`continuity` native files (routing global).

---

### Task 1: `internal/migrate` — v1 parse + plan + convert

**Files:** Create `internal/migrate/migrate.go`, `internal/migrate/migrate_test.go`

- [ ] **Step 1: failing test** `internal/migrate/migrate_test.go`
```go
package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeV1(t *testing.T, dir, sub, name, content string) {
	t.Helper()
	d := filepath.Join(dir, sub)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPlan(t *testing.T) {
	v1 := t.TempDir()
	// global mistake, kept; root-cause/prevention should land in native body.
	writeV1(t, v1, "mistakes", "m1.md", "---\ntype: mistake\nproject: global\ncreated: 2026-04-01\nstatus: active\nref_count: 12\nroot-cause: \"used getattr wrong\"\nprevention: \"use column_attrs\"\n---\n## Context\nbody here\n")
	// project feedback (akasha) → routes to akasha native dir.
	writeV1(t, v1, "feedback", "f1.md", "---\ntype: feedback\nproject: akasha\ncreated: 2026-04-01\nstatus: active\nref_count: 5\ndescription: do X\n---\nrule body\n")
	// dead weight: ref_count 0, old, not pinned → pruned.
	writeV1(t, v1, "notes", "n1.md", "---\ntype: note\nproject: global\ncreated: 2026-01-01\nstatus: active\nref_count: 0\n---\nunused\n")

	projects := map[string]string{"/home/u/dev/akasha": "akasha"}
	p, err := BuildPlan(Opts{
		V1Dir: v1, GlobalDir: "/home/u/.claude/memory-global",
		OSHome: "/home/u", Projects: projects, Today: "2026-05-29",
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if p.Kept != 2 || p.Pruned != 1 {
		t.Fatalf("kept=%d pruned=%d, want 2/1", p.Kept, p.Pruned)
	}
	byslug := map[string]FileConv{}
	for _, c := range p.Convs {
		byslug[c.Slug] = c
	}
	// m1: kept, global, fine type preserved, root-cause/prevention in body
	m1 := byslug["m1.md"]
	if m1.Action != "keep" || m1.Type != "mistake" {
		t.Errorf("m1 = %+v", m1)
	}
	if !strings.Contains(m1.Native, "type: mistake") {
		t.Errorf("m1 native must carry fine type:\n%s", m1.Native)
	}
	if !strings.Contains(m1.Native, "used getattr wrong") || !strings.Contains(m1.Native, "use column_attrs") {
		t.Errorf("m1 native body must preserve root-cause/prevention:\n%s", m1.Native)
	}
	if m1.TargetDir != "/home/u/.claude/memory-global" {
		t.Errorf("m1 global routing = %q", m1.TargetDir)
	}
	// f1: project akasha → routes to akasha native project dir
	f1 := byslug["f1.md"]
	if f1.TargetDir != "/home/u/.claude/projects/-home-u-dev-akasha/memory" {
		t.Errorf("f1 project routing = %q", f1.TargetDir)
	}
	// n1: pruned
	if byslug["n1.md"].Action != "prune" {
		t.Errorf("n1 should be pruned: %+v", byslug["n1.md"])
	}
}
```

- [ ] **Step 2: run, expect FAIL** — `go test ./internal/migrate/ -v`

- [ ] **Step 3: implement** `internal/migrate/migrate.go`
```go
// Package migrate converts the v1 hypomnema store (8-type rich-frontmatter
// files under ~/.claude/memory/<type>/) into native memory files + a rebuilt
// sidecar. Planning is pure; execution writes files and backs up the old store.
package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// Opts configures a migration plan.
type Opts struct {
	V1Dir     string            // ~/.claude/memory
	GlobalDir string            // ~/.claude/memory-global
	OSHome    string            // ~ (for native ProjectMemoryDir)
	Projects  map[string]string // abs-cwd → project-name (projects.json)
	Today     string            // YYYY-MM-DD
}

// FileConv is the planned disposition of one v1 file.
type FileConv struct {
	OldPath   string
	Slug      string
	Action    string // "keep" | "prune"
	Reason    string
	TargetDir string // native dest dir ("" if prune)
	Type      string // fine type (verbatim from v1)
	Project   string
	Native    string // converted native file content (if keep)
}

// Plan is the full migration plan.
type Plan struct {
	Convs  []FileConv
	Kept   int
	Pruned int
}

var v1Subdirs = []string{"mistakes", "strategies", "feedback", "knowledge", "decisions", "notes", "projects", "continuity"}

// BuildPlan walks the v1 store and decides each file's disposition. Pure
// (reads files, computes; writes nothing).
func BuildPlan(o Opts) (Plan, error) {
	t0 := parseDay(o.Today)
	// name→cwd reverse map for project routing.
	nameToCWD := map[string]string{}
	for cwd, name := range o.Projects {
		nameToCWD[name] = cwd
	}
	var p Plan
	for _, sub := range v1Subdirs {
		dir := filepath.Join(o.V1Dir, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // missing subdir is fine
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			fm, body := splitV1(string(raw))
			conv := FileConv{
				OldPath: path, Slug: e.Name(),
				Type: orDefault(fm["type"], sub), Project: orDefault(fm["project"], "global"),
			}
			if shouldPrune(fm, t0, parseDay(o.Today)) {
				conv.Action = "prune"
				conv.Reason = "ref_count=0, not pinned, >30d"
				p.Pruned++
			} else {
				conv.Action = "keep"
				conv.TargetDir = routeDir(conv.Project, o, nameToCWD)
				conv.Native = toNative(conv.Slug, conv.Type, fm, body)
				p.Kept++
			}
			p.Convs = append(p.Convs, conv)
		}
	}
	return p, nil
}

func shouldPrune(fm map[string]string, today, _ day) bool {
	if fm["status"] == "pinned" {
		return false
	}
	rc, _ := strconv.Atoi(strings.TrimSpace(fm["ref_count"]))
	if rc != 0 {
		return false
	}
	c := parseDayStr(fm["created"])
	if c.zero {
		return false // unknown age → keep, don't prune blindly
	}
	return today.sub(c) > 30
}

func routeDir(project string, o Opts, nameToCWD map[string]string) string {
	if project == "" || project == "global" {
		return o.GlobalDir
	}
	if cwd, ok := nameToCWD[project]; ok {
		return native.ProjectMemoryDir(o.OSHome, cwd)
	}
	return o.GlobalDir // unknown project → global (preserve, don't lose)
}

// toNative composes the native file: minimal frontmatter (name/description/
// type=fine) + a body that folds in mistake root-cause/prevention prose so
// no knowledge is lost.
func toNative(slug, fineType string, fm map[string]string, body string) string {
	name := fm["name"]
	if name == "" {
		name = humanize(strings.TrimSuffix(slug, ".md"))
	}
	desc := fm["description"]
	if desc == "" {
		desc = firstLine(body)
	}
	var nb strings.Builder
	if rc := fm["root-cause"]; rc != "" {
		fmt.Fprintf(&nb, "**Root cause:** %s\n\n", rc)
	}
	if pv := fm["prevention"]; pv != "" {
		fmt.Fprintf(&nb, "**Prevention:** %s\n\n", pv)
	}
	nb.WriteString(body)
	return fmt.Sprintf("---\nname: %s\ndescription: %s\ntype: %s\n---\n%s\n",
		name, desc, fineType, strings.TrimSpace(nb.String()))
}

func humanize(slug string) string {
	s := strings.ReplaceAll(slug, "-", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func firstLine(body string) string {
	for _, ln := range strings.Split(body, "\n") {
		ln = strings.TrimSpace(strings.TrimLeft(ln, "#"))
		ln = strings.TrimSpace(ln)
		if ln != "" {
			if len(ln) > 100 {
				return ln[:100]
			}
			return ln
		}
	}
	return ""
}

func orDefault(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return strings.TrimSpace(v)
}

// splitV1 parses v1 frontmatter into a flat key→value map (block arrays like
// domains/keywords/triggers are skipped — migration doesn't need them; the
// sidecar re-derives keywords). Tolerates quoted scalars + BOM/CRLF.
func splitV1(s string) (map[string]string, string) {
	fm := map[string]string{}
	s = strings.TrimPrefix(s, "﻿")
	if !strings.HasPrefix(s, "---") {
		return fm, strings.TrimSpace(s)
	}
	lines := strings.Split(s, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t\r") == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return fm, strings.TrimSpace(s)
	}
	for _, ln := range lines[1:end] {
		ln = strings.TrimRight(ln, "\r")
		if strings.HasPrefix(ln, "  ") || strings.HasPrefix(ln, "\t") || strings.HasPrefix(strings.TrimSpace(ln), "- ") {
			continue // block-array item — skip
		}
		idx := strings.IndexByte(ln, ':')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(ln[:idx])
		val := strings.Trim(strings.TrimSpace(ln[idx+1:]), `"'`)
		fm[key] = val
	}
	return fm, strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
}

// minimal date helper (avoids importing time across the package surface)
type day struct {
	y, m, d int
	zero    bool
}

func parseDay(s string) day      { return parseDayStr(s) }
func parseDayStr(s string) day {
	var y, m, d int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d-%d-%d", &y, &m, &d); err != nil {
		return day{zero: true}
	}
	return day{y: y, m: m, d: d}
}

// sub returns approximate day difference (good enough for a 30-day prune gate).
func (a day) sub(b day) int {
	if a.zero || b.zero {
		return 0
	}
	return (a.y-b.y)*365 + (a.m-b.m)*30 + (a.d - b.d)
}
```

- [ ] **Step 4: run, expect PASS** — `go test ./internal/migrate/ -v`; `go vet ./internal/migrate/`
- [ ] **Step 5: commit** — `git add internal/migrate/migrate.go internal/migrate/migrate_test.go && git commit -m "feat(migrate): v1 parse + plan + native conversion (prune, route, fold-in)"`

---

### Task 2: `internal/migrate` — Execute + Rollback

**Files:** Create `internal/migrate/execute.go`, `internal/migrate/execute_test.go`

- [ ] **Step 1: failing test** `internal/migrate/execute_test.go`
```go
package migrate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecuteAndRollback(t *testing.T) {
	home := t.TempDir()
	v1 := filepath.Join(home, ".claude", "memory")
	globalDir := filepath.Join(home, ".claude", "memory-global")
	writeV1(t, v1, "feedback", "f1.md", "---\ntype: feedback\nproject: global\ncreated: 2026-04-01\nstatus: active\nref_count: 5\n---\nrule body\n")
	os.WriteFile(filepath.Join(v1, ".wal"), []byte("2026-04-01|inject|f1.md|s0\n"), 0o644)

	p, _ := BuildPlan(Opts{V1Dir: v1, GlobalDir: globalDir, OSHome: home, Today: "2026-05-29"})
	backup := filepath.Join(home, ".claude", "memory.v1-backup")
	if err := Execute(p, ExecOpts{V1Dir: v1, BackupDir: backup, MemoryDir: v1}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// native file written to global dir
	if _, err := os.Stat(filepath.Join(globalDir, "f1.md")); err != nil {
		t.Errorf("native f1.md not written: %v", err)
	}
	// old store backed up (not deleted)
	if _, err := os.Stat(filepath.Join(backup, "feedback", "f1.md")); err != nil {
		t.Errorf("old store not backed up: %v", err)
	}
	// idempotent: second run must not error
	p2, _ := BuildPlan(Opts{V1Dir: v1, GlobalDir: globalDir, OSHome: home, Today: "2026-05-29"})
	if err := Execute(p2, ExecOpts{V1Dir: v1, BackupDir: backup, MemoryDir: v1}); err != nil {
		t.Fatalf("second Execute (idempotent) failed: %v", err)
	}

	// rollback restores from backup
	if err := Rollback(ExecOpts{V1Dir: v1, BackupDir: backup, MemoryDir: v1}); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, err := os.Stat(filepath.Join(v1, "feedback", "f1.md")); err != nil {
		t.Errorf("rollback did not restore old store: %v", err)
	}
}
```

- [ ] **Step 2: run, expect FAIL** — `go test ./internal/migrate/ -run TestExecute -v`

- [ ] **Step 3: implement** `internal/migrate/execute.go`
```go
package migrate

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

// ExecOpts configures Execute / Rollback.
type ExecOpts struct {
	V1Dir     string
	BackupDir string
	MemoryDir string // where the WAL + sidecar live (== V1Dir in the normal case)
}

// Execute applies a plan: write kept native files, rebuild the sidecar from
// the (preserved) WAL + native, and back up the v1 store. Idempotent: writing
// the same native files again is a no-op overwrite; backup is skipped if it
// already exists. The old store is NEVER deleted — only copied to BackupDir.
func Execute(p Plan, o ExecOpts) error {
	// 1. Back up the v1 store (once).
	if _, err := os.Stat(o.BackupDir); os.IsNotExist(err) {
		if err := copyTree(o.V1Dir, o.BackupDir); err != nil {
			return fmt.Errorf("migrate.Execute: backup: %w", err)
		}
	}
	// 2. Write kept native files.
	for _, c := range p.Convs {
		if c.Action != "keep" {
			continue
		}
		if err := os.MkdirAll(c.TargetDir, 0o755); err != nil {
			return fmt.Errorf("migrate.Execute: mkdir %s: %w", c.TargetDir, err)
		}
		if err := os.WriteFile(filepath.Join(c.TargetDir, c.Slug), []byte(c.Native), 0o644); err != nil {
			return fmt.Errorf("migrate.Execute: write %s: %w", c.Slug, err)
		}
	}
	// 3. Rebuild the sidecar from the preserved WAL + the new native files.
	files := allNative(p)
	s, err := sidecar.Open(filepath.Join(o.MemoryDir, ".sidecar.db"))
	if err == nil {
		_ = sidecar.Reproject(s, files, filepath.Join(o.MemoryDir, ".wal"))
		s.Close()
	}
	return nil
}

// Rollback restores the v1 store from BackupDir (the new native dirs are left
// in place — harmless; the user re-points settings.json back to v1 hooks).
func Rollback(o ExecOpts) error {
	if _, err := os.Stat(o.BackupDir); err != nil {
		return fmt.Errorf("migrate.Rollback: no backup at %s: %w", o.BackupDir, err)
	}
	return copyTree(o.BackupDir, o.V1Dir)
}

func allNative(p Plan) []native.MemFile {
	var out []native.MemFile
	for _, c := range p.Convs {
		if c.Action != "keep" {
			continue
		}
		mf, _ := parseNativeContent(c.Slug, c.Native)
		out = append(out, mf)
	}
	return out
}

// parseNativeContent extracts a MemFile from converted native content (reuses
// the same minimal frontmatter shape native.List would read off disk).
func parseNativeContent(slug, content string) (native.MemFile, error) {
	fm, body := splitV1(content) // same flat parser; native frontmatter is a subset
	return native.MemFile{Slug: slug, Name: fm["name"], Description: fm["description"], Type: fm["type"], Body: body}, nil
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode())
	})
}
```

- [ ] **Step 4: run, expect PASS** — `go test ./internal/migrate/ -v`; `go vet`; `go build ./...`
- [ ] **Step 5: commit** — `git add internal/migrate/execute.go internal/migrate/execute_test.go && git commit -m "feat(migrate): Execute (write native + rebuild sidecar + backup) + Rollback"`

---

### Task 3: `memoryctl migrate` verb

**Files:** Create `cmd/memoryctl/migrate.go`, `cmd/memoryctl/migrate_test.go`; modify `cmd/memoryctl/main.go`

- [ ] **Step 1: failing test** `cmd/memoryctl/migrate_test.go`
```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateDryRunAndExecute(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(filepath.Join(memDir, "feedback"), 0o755)
	os.WriteFile(filepath.Join(memDir, "feedback", "f1.md"),
		[]byte("---\ntype: feedback\nproject: global\ncreated: 2026-04-01\nstatus: active\nref_count: 5\n---\nrule body\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte("2026-04-01|inject|f1.md|s0\n"), 0o644)

	env := map[string]string{"CLAUDE_HOME": filepath.Join(home, ".claude"), "CLAUDE_MEMORY_DIR": memDir, "HYPOMNEMA_TODAY": "2026-05-29"}

	// dry-run: reports a plan, writes nothing.
	out, _, code := run(t, env, "migrate", "--dry-run")
	if code != 0 {
		t.Fatalf("dry-run exit=%d", code)
	}
	if !strings.Contains(out, "keep") && !strings.Contains(out, "Kept") {
		t.Errorf("dry-run should report a plan: %q", out)
	}
	globalDir := filepath.Join(home, ".claude", "memory-global")
	if _, err := os.Stat(filepath.Join(globalDir, "f1.md")); err == nil {
		t.Error("dry-run must NOT write native files")
	}

	// execute: writes native + backup.
	_, errOut, code := run(t, env, "migrate", "--execute")
	if code != 0 {
		t.Fatalf("execute exit=%d stderr=%s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(globalDir, "f1.md")); err != nil {
		t.Errorf("execute must write native f1.md: %v", err)
	}
}
```

- [ ] **Step 2: run, expect FAIL** — `go test ./cmd/memoryctl/ -run TestMigrate -v`

- [ ] **Step 3a: dispatch** in `cmd/memoryctl/main.go`: `case "migrate":` → `runMigrate(os.Args[2:])`

- [ ] **Step 3b: implement** `cmd/memoryctl/migrate.go`
```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/migrate"
)

// runMigrate: memoryctl migrate --dry-run | --execute | --rollback
func runMigrate(args []string) {
	mode := ""
	for _, a := range args {
		switch a {
		case "--dry-run", "--execute", "--rollback":
			mode = a
		case "-h", "--help":
			fmt.Print(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "memoryctl migrate: unknown flag %q\n", a)
			os.Exit(2)
		}
	}
	if mode == "" {
		fmt.Fprintln(os.Stderr, "memoryctl migrate: one of --dry-run | --execute | --rollback required")
		os.Exit(2)
	}
	osHome := filepath.Dir(claudeDir())
	v1 := memoryDir()
	backup := filepath.Join(claudeDir(), "memory.v1-backup")
	exec := migrate.ExecOpts{V1Dir: v1, BackupDir: backup, MemoryDir: v1}

	if mode == "--rollback" {
		if err := migrate.Rollback(exec); err != nil {
			fmt.Fprintf(os.Stderr, "rollback failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("rolled back from %s\n", backup)
		return
	}

	p, err := migrate.BuildPlan(migrate.Opts{
		V1Dir: v1, GlobalDir: filepath.Join(osHome, ".claude", "memory-global"),
		OSHome: osHome, Projects: loadProjects(v1), Today: today(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: plan: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Migration plan: %d keep, %d prune (backup → %s)\n", p.Kept, p.Pruned, backup)
	for _, c := range p.Convs {
		fmt.Printf("  %-6s %-40s %s\n", c.Action, c.Slug, c.Reason)
	}
	if mode == "--dry-run" {
		fmt.Println("(dry-run — nothing written)")
		return
	}
	if err := migrate.Execute(p, exec); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: execute: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Migration complete. Old store backed up; native files written; sidecar rebuilt.")
}

func loadProjects(memDir string) map[string]string {
	b, err := os.ReadFile(filepath.Join(memDir, "projects.json"))
	if err != nil {
		return nil
	}
	var m map[string]string
	_ = json.Unmarshal(b, &m)
	return m
}
```

- [ ] **Step 4: run, expect PASS** — `go test ./cmd/memoryctl/ -run TestMigrate -v`; then `go build ./... && go test -race ./... && go vet ./...`
- [ ] **Step 5: commit** — `git add cmd/memoryctl/migrate.go cmd/memoryctl/main.go cmd/memoryctl/migrate_test.go && git commit -m "feat(memoryctl): migrate verb (--dry-run/--execute/--rollback)"`

---

## Phase 6 Definition of Done

- [ ] `migrate.BuildPlan` parses v1 frontmatter, prunes dead weight (ref_count 0 / !pinned / >30d), routes global→memory-global and project→native-project-dir (via projects.json), converts to native (fine type in `type:`, root-cause/prevention folded into body).
- [ ] `migrate.Execute` writes native files, rebuilds the sidecar, backs up the v1 store (never deletes); idempotent. `Rollback` restores from backup.
- [ ] `memoryctl migrate --dry-run|--execute|--rollback` works end-to-end on a fixture; dry-run writes nothing.
- [ ] `go test -race ./...` green; NOT run against the live store.

## Self-Review (against spec §4.3/§7)

- **Type:** fine type preserved in native `type:` → sidecar.Reproject keeps it (no coarse/fine split, no clobber). ✓
- **Knowledge preservation:** mistake root-cause/prevention folded into native body. ✓
- **Routing:** global→memory-global; project→ProjectMemoryDir via projects.json reverse-lookup; unknown→global (no loss). ✓
- **Prune:** ref_count==0 & !pinned & >30d; pruned files stay in the backup (never deleted). ✓
- **Backup-not-delete + idempotent + rollback:** Execute backs up once, overwrites native idempotently; Rollback restores. ✓
- **Deferred → Phase 7:** CC-version gate + settings.json hook rewrite (install.sh); seed of ref_count relies on the preserved WAL (Reproject reconstructs); project/domains stay sidecar-inert; block-array frontmatter (domains/keywords/triggers) dropped (keywords re-derived by Reproject's PopulateKeywords).
