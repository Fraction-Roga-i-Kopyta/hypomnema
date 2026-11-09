package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

// sidecarMarks renders the sidecar mark table as sorted
// "project/slug/directive/arg" strings.
func sidecarMarks(t *testing.T, memDir string) []string {
	t.Helper()
	s, err := sidecar.Open(filepath.Join(memDir, ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ms, err := s.Marks()
	if err != nil {
		t.Fatalf("Marks: %v", err)
	}
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Project+"/"+m.Slug+"/"+m.Directive+"/"+m.Arg)
	}
	sort.Strings(out)
	return out
}

func wantSidecarMarks(t *testing.T, memDir string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if got := sidecarMarks(t, memDir); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("mark table:\n got %q\nwant %q", got, want)
	}
}

func lastWALLine(t *testing.T, memDir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(memDir, ".wal"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	return lines[len(lines)-1]
}

func writeFactFile(t *testing.T, dir, stem, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\ntype: note\nname: " + stem + "\ndescription: d\nstatus: active\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, stem+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return content
}

// otherProject adds a second project store beside retireFixture's and
// returns its anchor and store.
func otherProject(t *testing.T, env map[string]string) (anchor string, st native.Store) {
	t.Helper()
	anchor = filepath.Join(filepath.Dir(env["CLAUDE_PROJECT_CWD"]), "other")
	if err := os.MkdirAll(anchor, 0o755); err != nil {
		t.Fatal(err)
	}
	st = native.StoreFor(env["CLAUDE_HOME"], anchor)
	if err := os.MkdirAll(st.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return anchor, st
}

func TestMark_WritesEvent(t *testing.T) {
	env, _, memDir, factPath := retireFixture(t)
	tag := native.StoreFor(env["CLAUDE_HOME"], env["CLAUDE_PROJECT_CWD"]).Project
	raw, _ := os.ReadFile(factPath)
	for _, tc := range []struct{ directive, target string }{
		{"less", native.QKey(tag, "old-rule") + ":less"},
		{"hide", native.QKey(tag, "old-rule") + ":hide:" + tag},
		{"rewrite", native.QKey(tag, "old-rule") + ":rewrite:" + native.BodySHA(string(raw))},
		{"done", native.QKey(tag, "old-rule") + ":done"},
		{"clear", native.QKey(tag, "old-rule") + ":clear"},
	} {
		out, errOut, code := run(t, env, "mark", "old-rule", tc.directive)
		if code != 0 {
			t.Fatalf("mark %s: exit=%d stderr=%s", tc.directive, code, errOut)
		}
		if want := "2026-07-23|mark|" + tc.target + "|cli"; lastWALLine(t, memDir) != want {
			t.Fatalf("mark %s: WAL row\n got %q\nwant %q", tc.directive, lastWALLine(t, memDir), want)
		}
		if !strings.Contains(out, "marked old-rule "+tc.directive) {
			t.Errorf("mark %s: stdout %q", tc.directive, out)
		}
	}
}

func TestMark_HideForeignProjectRefused(t *testing.T) {
	env, _, memDir, _ := retireFixture(t)
	tag := native.StoreFor(env["CLAUDE_HOME"], env["CLAUDE_PROJECT_CWD"]).Project
	_, other := otherProject(t, env)
	writeFactFile(t, other.Dir, "b", "foreign body")
	writeFactFile(t, env["HYPOMNEMA_GLOBAL_DIR"], "g", "global body")
	walPath := filepath.Join(memDir, ".wal")
	_ = os.WriteFile(walPath, nil, 0o644)

	_, errOut, code := run(t, env, "mark", "b", "hide", "--project", other.Project)
	if code != 2 || !strings.Contains(errOut, "hide applies to global facts and facts of this project") {
		t.Fatalf("foreign hide: exit=%d stderr=%s", code, errOut)
	}
	if b, _ := os.ReadFile(walPath); len(b) != 0 {
		t.Fatalf("refused hide wrote the WAL: %q", b)
	}
	// Other directives may target a foreign project's fact.
	if _, e, c := run(t, env, "mark", "b", "less", "--project", other.Project); c != 0 {
		t.Fatalf("foreign less: exit=%d stderr=%s", c, e)
	}
	// hide on a global fact applies to this project.
	if _, e, c := run(t, env, "mark", "g", "hide"); c != 0 {
		t.Fatalf("global hide: exit=%d stderr=%s", c, e)
	}
	if want := "2026-07-23|mark|" + native.QKey(native.GlobalProject, "g") + ":hide:" + tag + "|cli"; lastWALLine(t, memDir) != want {
		t.Fatalf("global hide row\n got %q\nwant %q", lastWALLine(t, memDir), want)
	}
	wantSidecarMarks(t, memDir,
		other.Project+"/b.md/less/",
		"global/g.md/hide/"+tag,
	)
}

func TestMark_UnknownDirective(t *testing.T) {
	env, _, memDir, _ := retireFixture(t)
	walPath := filepath.Join(memDir, ".wal")
	_ = os.WriteFile(walPath, nil, 0o644)
	for _, args := range [][]string{
		{"mark", "old-rule", "demote"},
		{"mark", "old-rule", "task:rewrite"},
		{"mark", "old-rule"},
		{"mark"},
		{"mark", "old-rule", "less", "extra"},
		{"mark", "old-rule", "less", "--bogus"},
		{"mark", "old-rule", "less", "--project"},
	} {
		_, errOut, code := run(t, env, args...)
		if code != 2 {
			t.Errorf("%v: exit=%d stderr=%s, want 2", args, code, errOut)
		}
	}
	if _, errOut, _ := run(t, env, "mark", "old-rule", "demote"); !strings.Contains(errOut, "unknown directive") {
		t.Errorf("stderr should name the problem: %s", errOut)
	}
	if b, _ := os.ReadFile(walPath); len(b) != 0 {
		t.Fatalf("misuse wrote the WAL: %q", b)
	}
}

func TestMark_MissingFact(t *testing.T) {
	env, _, _, _ := retireFixture(t)
	_, errOut, code := run(t, env, "mark", "nope", "less")
	if code != 1 || !strings.Contains(errOut, "no fact named") {
		t.Fatalf("exit=%d stderr=%s", code, errOut)
	}
}

// The verb refreshes the sidecar itself: no close needed before stats or
// inject see the mark.
func TestMark_VisibleWithoutClose(t *testing.T) {
	env, _, memDir, _ := retireFixture(t)
	tag := native.StoreFor(env["CLAUDE_HOME"], env["CLAUDE_PROJECT_CWD"]).Project
	if _, e, c := run(t, env, "mark", "old-rule", "less"); c != 0 {
		t.Fatal(e)
	}
	wantSidecarMarks(t, memDir, tag+"/old-rule.md/less/")
}

func TestMarks_PinKeepsOpenTask(t *testing.T) {
	env, _, memDir, factPath := retireFixture(t)
	tag := native.StoreFor(env["CLAUDE_HOME"], env["CLAUDE_PROJECT_CWD"]).Project
	raw, _ := os.ReadFile(factPath)
	task := tag + "/old-rule.md/rewrite/" + native.BodySHA(string(raw))
	if _, e, c := run(t, env, "mark", "old-rule", "rewrite"); c != 0 {
		t.Fatal(e)
	}
	wantSidecarMarks(t, memDir, task)
	if _, e, c := run(t, env, "pin", "old-rule"); c != 0 {
		t.Fatal(e)
	}
	if after, _ := os.ReadFile(factPath); string(after) == string(raw) {
		t.Fatal("precondition: pin should have rewritten the status line")
	}
	wantSidecarMarks(t, memDir, task)
	if _, e, c := run(t, env, "sidecar", "rebuild"); c != 0 {
		t.Fatal(e)
	}
	wantSidecarMarks(t, memDir, task)
	if _, e, c := run(t, env, "unpin", "old-rule"); c != 0 {
		t.Fatal(e)
	}
	wantSidecarMarks(t, memDir, task)
}

func TestMark_RetireClearsMarks(t *testing.T) {
	env, _, memDir, _ := retireFixture(t)
	for _, d := range []string{"less", "hide", "review"} {
		if _, e, c := run(t, env, "mark", "old-rule", d); c != 0 {
			t.Fatal(e)
		}
	}
	if _, e, c := run(t, env, "retire", "old-rule", "--reason", "dup of x", "--superseded-by", "x"); c != 0 {
		t.Fatal(e)
	}
	if _, e, c := run(t, env, "revive", "old-rule"); c != 0 {
		t.Fatal(e)
	}
	if _, e, c := run(t, env, "sidecar", "rebuild"); c != 0 {
		t.Fatal(e)
	}
	wantSidecarMarks(t, memDir)
}

func removeSidecar(t *testing.T, memDir string) {
	t.Helper()
	p := filepath.Join(memDir, ".sidecar.db")
	for _, f := range []string{p, p + "-wal", p + "-shm"} {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func TestMarks_RebuildSameFromAnyProject(t *testing.T) {
	env, _, memDir, _ := retireFixture(t)
	tagA := native.StoreFor(env["CLAUDE_HOME"], env["CLAUDE_PROJECT_CWD"]).Project
	anchorB, b := otherProject(t, env)
	writeFactFile(t, b.Dir, "b", "foreign body")
	writeFactFile(t, b.Dir, "b2", "foreign body two")
	writeFactFile(t, env["HYPOMNEMA_GLOBAL_DIR"], "g", "global body")
	for _, args := range [][]string{
		{"mark", "old-rule", "less"},
		{"mark", "g", "hide"},
		{"mark", "b", "review", "--project", b.Project},
		{"mark", "b2", "rewrite", "--project", b.Project},
	} {
		if _, e, c := run(t, env, args...); c != 0 {
			t.Fatalf("%v: %s", args, e)
		}
	}
	writeFactFile(t, b.Dir, "b2", "foreign body two, rewritten") // closes b2's task

	removeSidecar(t, memDir)
	if _, e, c := run(t, env, "sidecar", "rebuild"); c != 0 {
		t.Fatal(e)
	}
	fromA := sidecarMarks(t, memDir)

	removeSidecar(t, memDir)
	envB := map[string]string{}
	for k, v := range env {
		envB[k] = v
	}
	envB["CLAUDE_PROJECT_CWD"] = anchorB
	if _, e, c := run(t, envB, "sidecar", "rebuild"); c != 0 {
		t.Fatal(e)
	}
	fromB := sidecarMarks(t, memDir)

	if strings.Join(fromA, "\n") != strings.Join(fromB, "\n") {
		t.Fatalf("rebuild differs by project:\n A %q\n B %q", fromA, fromB)
	}
	bRaw, _ := os.ReadFile(filepath.Join(b.Dir, "b.md"))
	want := []string{
		tagA + "/old-rule.md/less/",
		"global/g.md/hide/" + tagA,
		b.Project + "/b.md/review/" + native.BodySHA(string(bRaw)),
	}
	sort.Strings(want)
	if strings.Join(fromA, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rebuilt table:\n got %q\nwant %q", fromA, want)
	}
}

// CLAUDE_CONFIG_DIR moves projects/ away from CLAUDE_HOME's default: the
// close path must resolve other projects' stores under the config dir, not
// under ~/.claude (where a stale same-tag store here holds nothing).
func TestMarks_ConfigDirOverride(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	mem := filepath.Join(home, ".claude", "memory")
	projA := filepath.Join(home, "work", "a")
	projB := filepath.Join(home, "work", "b")
	for _, d := range []string{mem, projA, projB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stA := native.StoreFor(cfg, projA)
	stB := native.StoreFor(cfg, projB)
	writeFactFile(t, stA.Dir, "a", "project a body")
	writeFactFile(t, stB.Dir, "b", "project b body")
	writeFactFile(t, stB.Dir, "b2", "project b body two")
	// Decoy: a store with B's tag under the default config dir.
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects", stB.Project, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"HOME": home, "CLAUDE_HOME": "", "HYPOMNEMA_GLOBAL_DIR": "", "CLAUDE_CONFIG_DIR": cfg,
		"CLAUDE_MEMORY_DIR": mem, "CLAUDE_PROJECT_DIR": projA, "HYPOMNEMA_TODAY": "2026-11-09",
	}
	for _, args := range [][]string{
		{"mark", "b", "less", "--project", stB.Project},
		{"mark", "b2", "rewrite", "--project", stB.Project},
	} {
		if _, e, c := run(t, env, args...); c != 0 {
			t.Fatalf("%v: %s", args, e)
		}
	}
	writeFactFile(t, stB.Dir, "b2", "project b body two, rewritten") // closes b2's task

	if _, e, c := runStdin(t, env, `{"session_id":"s1","cwd":"`+projA+`"}`, "close"); c != 0 {
		t.Fatalf("close: exit=%d stderr=%s", c, e)
	}
	wantSidecarMarks(t, mem, stB.Project+"/b.md/less/")
}
