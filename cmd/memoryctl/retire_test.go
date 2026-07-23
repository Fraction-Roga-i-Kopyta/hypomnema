package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/inject"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// retireFixture builds a ClaudeHome with one project-store fact and returns
// the env map plus the paths the assertions need. memDir must exist BEFORE
// any wal append — the WAL open and its lock need the parent directory.
func retireFixture(t *testing.T) (env map[string]string, store, memDir, factPath string) {
	t.Helper()
	home := t.TempDir()
	claude := filepath.Join(home, ".claude")
	cwd := filepath.Join(home, "work", "proj")
	memDir = filepath.Join(claude, "memory")
	store = filepath.Join(claude, "projects", native.SlugFromCWD(cwd), "memory")
	globalDir := filepath.Join(home, "memory-global")
	for _, d := range []string{store, memDir, globalDir, cwd} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	factPath = filepath.Join(store, "old-rule.md")
	content := "---\ntype: mistake\nname: old-rule\ndescription: d\nstatus: active\nkeywords: [docker]\n---\nold body text\n"
	if err := os.WriteFile(factPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	env = map[string]string{
		"CLAUDE_HOME":          claude,
		"CLAUDE_MEMORY_DIR":    memDir,
		"CLAUDE_PROJECT_CWD":   cwd,
		"HYPOMNEMA_TODAY":      "2026-07-23",
		"HYPOMNEMA_GLOBAL_DIR": globalDir,
	}
	return env, store, memDir, factPath
}

func TestRetireAndRevive(t *testing.T) {
	env, store, memDir, factPath := retireFixture(t)

	_, errOut, code := run(t, env, "retire", "old-rule",
		"--reason", "promoted to hook", "--superseded-by", "pretool-hook-x")
	if code != 0 {
		t.Fatalf("retire exit=%d stderr=%s", code, errOut)
	}
	if _, err := os.Stat(factPath); !os.IsNotExist(err) {
		t.Fatalf("original still present: %v", err)
	}
	archived := filepath.Join(store, ".archive", "old-rule.md")
	b, err := os.ReadFile(archived)
	if err != nil {
		t.Fatalf("archived file: %v", err)
	}
	for _, want := range []string{"retired: 2026-07-23", "retire-reason: promoted to hook", "superseded-by: pretool-hook-x"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("tombstone frontmatter missing %q in:\n%s", want, b)
		}
	}
	walB, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if !strings.Contains(string(walB), "|retire|") ||
		!strings.Contains(string(walB), ">pretool-hook-x") {
		t.Fatalf("retire WAL event missing: %s", walB)
	}

	_, errOut, code = run(t, env, "revive", "old-rule")
	if code != 0 {
		t.Fatalf("revive exit=%d stderr=%s", code, errOut)
	}
	if _, err := os.Stat(factPath); err != nil {
		t.Fatalf("revive did not restore: %v", err)
	}
	b, _ = os.ReadFile(factPath)
	if strings.Contains(string(b), "retired:") || strings.Contains(string(b), "superseded-by:") {
		t.Fatalf("tombstone keys not stripped:\n%s", b)
	}
	if !strings.Contains(string(b), "keywords: [docker]") {
		t.Fatalf("original frontmatter lost on revive:\n%s", b)
	}
	walB, _ = os.ReadFile(filepath.Join(memDir, ".wal"))
	if !strings.Contains(string(walB), "|revive|") {
		t.Fatalf("revive WAL event missing: %s", walB)
	}
}

func TestRetire_UnknownSlug(t *testing.T) {
	env, _, _, _ := retireFixture(t)
	_, errOut, code := run(t, env, "retire", "no-such-fact")
	if code != 1 {
		t.Fatalf("exit=%d, want 1; stderr=%s", code, errOut)
	}
	if !strings.Contains(errOut, "no-such-fact") {
		t.Fatalf("error must name the slug: %s", errOut)
	}
}

func TestRetire_AmbiguousAcrossStores(t *testing.T) {
	env, _, _, _ := retireFixture(t)
	// Same basename in the global store → ambiguity is fatal (exit 2).
	if err := os.WriteFile(filepath.Join(env["HYPOMNEMA_GLOBAL_DIR"], "old-rule.md"),
		[]byte("---\nname: old-rule\ntype: note\n---\nglobal twin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := run(t, env, "retire", "old-rule")
	if code != 2 {
		t.Fatalf("exit=%d, want 2; stderr=%s", code, errOut)
	}
	if !strings.Contains(errOut, "more than one store") {
		t.Fatalf("expected ambiguity error listing stores: %s", errOut)
	}
}

// TestStripTombstone_BlockScalarSurvives: an indented "superseded-by:" line
// inside a block scalar is continuation content, not a top-level key —
// stripTombstone must leave it alone (mirrors splitFrontmatter's G2 rule).
func TestStripTombstone_BlockScalarSurvives(t *testing.T) {
	in := "---\nname: x\nroot-cause: |\n  superseded-by: not-a-key\nretired: 2026-07-23\nsuperseded-by: real\n---\nbody\n"
	got := stripTombstone(in)
	if !strings.Contains(got, "  superseded-by: not-a-key") {
		t.Fatalf("block-scalar content was stripped:\n%s", got)
	}
	if strings.Contains(got, "retired: 2026-07-23") || strings.Contains(got, "superseded-by: real") {
		t.Fatalf("top-level tombstone keys survived:\n%s", got)
	}
}

// TestPersistHoldoutSkips: one holdout-skip per fact per session (repeat
// prompts dedup via the runtime list); the final observation session
// (remaining==1) also emits ablate-stop:expired.
func TestPersistHoldoutSkips(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_MEMORY_DIR", memDir)
	t.Setenv("HYPOMNEMA_TODAY", "2026-07-23")

	res := inject.Result{
		HoldoutSkipped:   []string{"fading.md"},
		HoldoutRemaining: map[string]int{"fading.md": 1},
		ProjectBySlug:    map[string]string{"fading.md": "projA"},
	}
	persistHoldoutSkips(res, "sess-1")
	persistHoldoutSkips(res, "sess-1") // second prompt, same session — no-op
	walB, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if got := strings.Count(string(walB), "|holdout-skip|"); got != 1 {
		t.Fatalf("holdout-skip lines = %d, want 1 (session dedup):\n%s", got, walB)
	}
	if !strings.Contains(string(walB), "|ablate-stop|") ||
		!strings.Contains(string(walB), ":expired") {
		t.Fatalf("missing ablate-stop:expired on final session:\n%s", walB)
	}
}

func TestAblateReport(t *testing.T) {
	env, _, memDir, _ := retireFixture(t)
	q := "-tmp\x1ffact"
	_ = q // qualified target built inline below for readability
	proj := "projA"
	qs := proj + "\x1f" + "fact"
	lines := []string{
		"2026-07-01|ablate-start|" + qs + ":3|cli",
		"2026-07-02|holdout-skip|" + qs + "|s1",
		"2026-07-02|holdout-hit|" + qs + "|s1",
		"2026-07-03|holdout-skip|" + qs + "|s2",
		"2026-07-03|holdout-hit|" + qs + "|s2",
		"2026-07-04|holdout-skip|" + qs + "|s3",
		"2026-07-04|holdout-miss|" + qs + "|s3",
		"2026-07-04|ablate-stop|" + qs + ":expired|s3",
	}
	if err := os.WriteFile(filepath.Join(memDir, ".wal"),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := run(t, env, "ablate", "report", "fact")
	if code != 0 {
		t.Fatalf("report exit=%d stderr=%s", code, errOut)
	}
	for _, want := range []string{"3 sessions requested", "3 observed", "applied 2/3", "inconclusive"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
}

func TestAblateStartAndStop(t *testing.T) {
	env, _, memDir, _ := retireFixture(t)
	_, errOut, code := run(t, env, "ablate", "old-rule", "--sessions", "2")
	if code != 0 {
		t.Fatalf("ablate exit=%d stderr=%s", code, errOut)
	}
	walB, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if !strings.Contains(string(walB), "|ablate-start|") || !strings.Contains(string(walB), ":2|") {
		t.Fatalf("ablate-start missing:\n%s", walB)
	}
	_, errOut, code = run(t, env, "ablate", "stop", "old-rule")
	if code != 0 {
		t.Fatalf("stop exit=%d stderr=%s", code, errOut)
	}
	walB, _ = os.ReadFile(filepath.Join(memDir, ".wal"))
	if !strings.Contains(string(walB), "|ablate-stop|") || !strings.Contains(string(walB), ":manual|") {
		t.Fatalf("ablate-stop missing:\n%s", walB)
	}
}

func TestPromoteVerb(t *testing.T) {
	env, store, _, _ := retireFixture(t)
	// A high-recurrence mistake → mechanical suggestion.
	if err := os.WriteFile(filepath.Join(store, "repeat.md"),
		[]byte("---\ntype: mistake\nname: repeat\nrecurrence: 3\nstatus: active\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := run(t, env, "promote")
	if code != 0 {
		t.Fatalf("promote exit=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "mechanical") || !strings.Contains(out, "memoryctl retire repeat") {
		t.Fatalf("promote output missing mechanical suggestion:\n%s", out)
	}
}

func TestPromoteVerb_EmptyCorpus(t *testing.T) {
	env, store, _, factPath := retireFixture(t)
	_ = store
	os.Remove(factPath)
	out, _, code := run(t, env, "promote")
	if code != 0 || !strings.Contains(out, "no promotion candidates") {
		t.Fatalf("exit=%d out=%s", code, out)
	}
}
