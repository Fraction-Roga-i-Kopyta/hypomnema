package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// Performance budgets of `memoryctl stats` on a generated corpus (1000 facts
// over 10 project stores + global, 60 000 WAL rows). Opt-in: wall-clock
// budgets flake under -race / -cover on CI, so they run only with
// HYPOMNEMA_PERF=1 (median of 3 CLI runs).
const (
	perfFiles   = 1000
	perfStores  = 10
	perfWALRows = 60000
)

func perfWorld(t *testing.T) (env map[string]string, tag string) {
	t.Helper()
	if os.Getenv("HYPOMNEMA_PERF") != "1" {
		t.Skip("set HYPOMNEMA_PERF=1 to run the stats performance budgets")
	}
	root := statsRoot(t)
	env = statsEnv(root)
	if err := os.MkdirAll(env["CLAUDE_PROJECT_CWD"], 0o755); err != nil {
		t.Fatal(err)
	}
	cur := native.StoreFor(env["CLAUDE_HOME"], env["CLAUDE_PROJECT_CWD"])
	tag = cur.Project
	dirs := []string{cur.Dir}
	tags := []string{tag}
	for i := 1; i < perfStores; i++ {
		tg := fmt.Sprintf("-perf-proj%02d", i)
		tags = append(tags, tg)
		dirs = append(dirs, filepath.Join(env["CLAUDE_HOME"], "projects", tg, "memory"))
	}
	dirs = append(dirs, env["HYPOMNEMA_GLOBAL_DIR"])
	tags = append(tags, native.GlobalProject)
	types := []string{"feedback", "mistake", "strategy", "knowledge", "decision", "note"}
	var keys []string
	for i := 0; i < perfFiles; i++ {
		s := i % len(dirs)
		slug := fmt.Sprintf("fact%04d.md", i)
		fm := fmt.Sprintf("type: %s\nname: fact-%04d\ndescription: Generated fact %d\ncreated: 2026-10-%02d\nstatus: active\nkeywords: [perf, k%d]",
			types[i%len(types)], i, i, 1+i%28, i%37)
		writeStatsFact(t, dirs[s], slug, fm)
		keys = append(keys, native.QKey(tags[s], slug))
	}
	var b strings.Builder
	events := []string{"inject", "inject", "inject", "cite-useful", "cite-silent", "recall"}
	for i := 0; i < perfWALRows; i++ {
		fmt.Fprintf(&b, "2026-11-%02d|%s|%s|sess%d\n", 1+i%9, events[i%len(events)], keys[(i*7)%len(keys)], i%400)
	}
	mem := env["CLAUDE_MEMORY_DIR"]
	if err := os.MkdirAll(mem, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mem, ".wal"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := run(t, env, "sidecar", "rebuild"); code != 0 {
		t.Fatalf("sidecar rebuild: exit=%d stderr=%s", code, errOut)
	}
	return env, tag
}

func medianRun(t *testing.T, env map[string]string, args ...string) time.Duration {
	t.Helper()
	var ds []time.Duration
	for i := 0; i < 3; i++ {
		start := time.Now()
		if _, errOut, code := run(t, env, append([]string{"stats", "--json"}, args...)...); code != 0 {
			t.Fatalf("stats %v: exit=%d stderr=%s", args, code, errOut)
		}
		ds = append(ds, time.Since(start))
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	t.Logf("stats %v: runs %v, median %v", args, ds, ds[1])
	return ds[1]
}

func TestStatsBudget_Session(t *testing.T) {
	env, _ := perfWorld(t)
	if d := medianRun(t, env, "--scope", "session", "--session", "sess1"); d > 100*time.Millisecond {
		t.Fatalf("session scope %v > 100ms", d)
	}
}

func TestStatsBudget_Project(t *testing.T) {
	env, _ := perfWorld(t)
	if d := medianRun(t, env, "--scope", "project"); d > 300*time.Millisecond {
		t.Fatalf("project scope %v > 300ms", d)
	}
}

func TestStatsBudget_All(t *testing.T) {
	env, _ := perfWorld(t)
	if d := medianRun(t, env, "--scope", "all"); d > 500*time.Millisecond {
		t.Fatalf("all scope %v > 500ms", d)
	}
}
