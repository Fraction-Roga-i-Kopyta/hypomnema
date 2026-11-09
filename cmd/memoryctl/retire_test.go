package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	store = native.StoreFor(claude, cwd).Dir
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

// retire and revive refresh the sidecar like pin and mark do: the row's
// status and the open-task queue (SessionStart's queue line, doctor's
// memory_queue) change at once, not at the next Stop hook.
func TestRetireRevive_RefreshSidecar(t *testing.T) {
	env, _, memDir, _ := retireFixture(t)
	if _, e, c := run(t, env, "mark", "old-rule", "rewrite"); c != 0 {
		t.Fatalf("mark: %s", e)
	}
	if len(sidecarMarks(t, memDir)) != 1 {
		t.Fatalf("precondition: one open task, got %q", sidecarMarks(t, memDir))
	}

	if _, e, c := run(t, env, "retire", "old-rule"); c != 0 {
		t.Fatalf("retire: %s", e)
	}
	if r := sidecarRec(t, memDir, "old-rule.md"); r.Status != "retired" {
		t.Fatalf("after retire: sidecar status=%q, want retired", r.Status)
	}
	wantSidecarMarks(t, memDir)

	if _, e, c := run(t, env, "revive", "old-rule"); c != 0 {
		t.Fatalf("revive: %s", e)
	}
	if r := sidecarRec(t, memDir, "old-rule.md"); r.Status != "active" {
		t.Fatalf("after revive: sidecar status=%q, want active", r.Status)
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

// foreignStore creates a second project store under the fixture's config dir
// with one fact and a MEMORY.md linking it.
func foreignStore(t *testing.T, env map[string]string, slug string) (dir string) {
	t.Helper()
	dir = filepath.Join(env["CLAUDE_HOME"], "projects", "other-proj", "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\ntype: note\nname: " + slug + "\ndescription: foreign fact\nstatus: active\nkeywords: [x]\n---\nforeign body\n"
	if err := os.WriteFile(filepath.Join(dir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := "# Memory Index\n- [" + slug + "](" + slug + ".md) — foreign fact\n"
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(idx), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRetire_ProjectFlagOtherStore(t *testing.T) {
	env, _, memDir, _ := retireFixture(t)
	dir := foreignStore(t, env, "foreign")

	_, errOut, code := run(t, env, "retire", "foreign")
	if code != 1 {
		t.Fatalf("bare slug exit=%d, want 1; stderr=%s", code, errOut)
	}
	_, errOut, code = run(t, env, "retire", "foreign", "--project", "other-proj")
	if code != 0 {
		t.Fatalf("--project exit=%d stderr=%s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, ".archive", "foreign.md")); err != nil {
		t.Fatalf("not archived in foreign store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "foreign.md")); !os.IsNotExist(err) {
		t.Fatalf("live file remains: %v", err)
	}
	idx, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(idx), "foreign.md") {
		t.Fatalf("foreign MEMORY.md still links the fact:\n%s", idx)
	}
	wal, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if !strings.Contains(string(wal), "|retire|") {
		t.Fatalf("no retire event in WAL:\n%s", wal)
	}
}

func TestRetire_UnknownProject(t *testing.T) {
	env, _, _, _ := retireFixture(t)
	_, errOut, code := run(t, env, "retire", "old-rule", "--project", "nope")
	if code != 1 || !strings.Contains(errOut, `no store "nope"`) {
		t.Fatalf("exit=%d stderr=%s", code, errOut)
	}
}

func TestRetire_AmbiguousNeedsProject(t *testing.T) {
	env, store, _, factPath := retireFixture(t)
	globalFact := filepath.Join(env["HYPOMNEMA_GLOBAL_DIR"], "old-rule.md")
	if err := os.WriteFile(globalFact, []byte("---\nname: old-rule\ntype: note\n---\nglobal twin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := run(t, env, "retire", "old-rule")
	if code != 2 {
		t.Fatalf("exit=%d, want 2; stderr=%s", code, errOut)
	}
	if !strings.Contains(errOut, factPath) || !strings.Contains(errOut, globalFact) {
		t.Fatalf("stderr must name both copies: %s", errOut)
	}
	_, errOut, code = run(t, env, "retire", "old-rule", "--project", "global")
	if code != 0 {
		t.Fatalf("--project global exit=%d stderr=%s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(env["HYPOMNEMA_GLOBAL_DIR"], ".archive", "old-rule.md")); err != nil {
		t.Fatalf("global copy not archived: %v", err)
	}
	if _, err := os.Stat(factPath); err != nil {
		t.Fatalf("project copy touched: %v", err)
	}
	_ = store
}

func TestRevive_ProjectFlag(t *testing.T) {
	env, _, _, _ := retireFixture(t)
	dir := foreignStore(t, env, "foreign")
	if _, errOut, code := run(t, env, "retire", "foreign", "--project", "other-proj"); code != 0 {
		t.Fatalf("retire exit=%d stderr=%s", code, errOut)
	}
	if _, _, code := run(t, env, "revive", "foreign"); code != 1 {
		t.Fatalf("bare revive exit=%d, want 1", code)
	}
	if _, errOut, code := run(t, env, "revive", "foreign", "--project", "other-proj"); code != 0 {
		t.Fatalf("revive --project exit=%d stderr=%s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "foreign.md")); err != nil {
		t.Fatalf("not restored: %v", err)
	}
	idx, _ := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if !strings.Contains(string(idx), "foreign.md") {
		t.Fatalf("foreign index not refreshed after revive:\n%s", idx)
	}
}

func TestRevive_PicksNewestArchivedCopy(t *testing.T) {
	env, store, _, _ := retireFixture(t)
	wave := filepath.Join(store, "wave.md")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(wave, []byte("---\ntype: note\nname: wave\ndescription: d\n---\n"+body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("first")
	env["HYPOMNEMA_TODAY"] = "2026-07-23"
	if _, e, c := run(t, env, "retire", "wave"); c != 0 {
		t.Fatalf("retire 1: %d %s", c, e)
	}
	write("second")
	env["HYPOMNEMA_TODAY"] = "2026-07-24"
	if _, e, c := run(t, env, "retire", "wave"); c != 0 {
		t.Fatalf("retire 2: %d %s", c, e)
	}
	if _, e, c := run(t, env, "revive", "wave"); c != 0 {
		t.Fatalf("revive: %d %s", c, e)
	}
	b, err := os.ReadFile(wave)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "second") || strings.Contains(string(b), "first") {
		t.Fatalf("revived the wrong copy:\n%s", b)
	}
}

func TestRevive_IgnoresLookalikeName(t *testing.T) {
	env, store, _, _ := retireFixture(t)
	mk := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(store, name+".md"),
			[]byte("---\ntype: note\nname: "+name+"\ndescription: d\n---\nbody of "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("wave")
	env["HYPOMNEMA_TODAY"] = "2026-07-23"
	if _, e, c := run(t, env, "retire", "wave"); c != 0 {
		t.Fatalf("retire wave: %d %s", c, e)
	}
	mk("wave-4")
	env["HYPOMNEMA_TODAY"] = "2026-07-24"
	if _, e, c := run(t, env, "retire", "wave-4"); c != 0 {
		t.Fatalf("retire wave-4: %d %s", c, e)
	}
	if _, e, c := run(t, env, "revive", "wave"); c != 0 {
		t.Fatalf("revive: %d %s", c, e)
	}
	b, err := os.ReadFile(filepath.Join(store, "wave.md"))
	if err != nil || !strings.Contains(string(b), "body of wave\n") {
		t.Fatalf("wrong content restored: %v\n%s", err, b)
	}
	if _, err := os.Stat(filepath.Join(store, ".archive", "wave-4.md")); err != nil {
		t.Fatalf("wave-4 must stay archived: %v", err)
	}
}

func TestRetireRevive_ByteIdentical(t *testing.T) {
	env, _, _, factPath := retireFixture(t)
	orig, err := os.ReadFile(factPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, e, c := run(t, env, "retire", "old-rule", "--reason", "r", "--superseded-by", "x"); c != 0 {
		t.Fatalf("retire: %d %s", c, e)
	}
	arch, _ := os.ReadFile(filepath.Join(filepath.Dir(factPath), ".archive", "old-rule.md"))
	if !strings.Contains(string(arch), "archived-from: old-rule") {
		t.Fatalf("tombstone lacks archived-from:\n%s", arch)
	}
	if _, e, c := run(t, env, "revive", "old-rule"); c != 0 {
		t.Fatalf("revive: %d %s", c, e)
	}
	got, _ := os.ReadFile(factPath)
	if string(got) != string(orig) {
		t.Fatalf("not byte-identical:\n%q\nvs\n%q", got, orig)
	}
}
