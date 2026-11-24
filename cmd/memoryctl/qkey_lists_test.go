package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// globalFact writes a fact into the global store with explicit frontmatter.
func (f storeFixture) globalFact(t *testing.T, slug, fm, body string) {
	t.Helper()
	dir := native.GlobalMemoryDir(filepath.Dir(f.claude))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, slug+".md"), []byte("---\n"+fm+"\n---\n"+body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func listLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []string
	for _, ln := range strings.Split(string(b), "\n") {
		if ln != "" {
			out = append(out, ln)
		}
	}
	sort.Strings(out)
	return out
}

// Review focus 2: global x.md is in context; a later prompt ranks project
// x.md — a distinct fact. It renders and gets its own inject row; the global
// one is not re-rendered or re-logged.
func TestInject_GlobalThenProjectSameSlug(t *testing.T) {
	f := newStoreFixture(t)
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	proj := native.StoreFor(f.claude, "/tmp/proj").Project
	f.globalFact(t, "x", "name: globalx\ntype: knowledge\nkeywords: [alphaword]", "GLOBAL BODY")
	f.factFM(t, "/tmp/proj", "x", "name: projx\ntype: knowledge\nkeywords: [betaword]", "PROJECT BODY")

	out1, _, _ := runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","prompt":"alphaword"}`, "inject", "--event=UserPromptSubmit")
	if ctx := additionalContext(t, out1); !strings.Contains(ctx, "GLOBAL BODY") || strings.Contains(ctx, "PROJECT BODY") {
		t.Fatalf("first prompt must render the global x.md only: %q", ctx)
	}
	out2, _, _ := runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","prompt":"betaword alphaword"}`, "inject", "--event=UserPromptSubmit")
	ctx2 := additionalContext(t, out2)
	if !strings.Contains(ctx2, "PROJECT BODY") {
		t.Errorf("project x.md is a distinct fact and must render: %q", ctx2)
	}
	if strings.Contains(ctx2, "GLOBAL BODY") {
		t.Errorf("global x.md is still in context and must not re-render: %q", ctx2)
	}
	wal, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	w := string(wal)
	if n := strings.Count(w, "|inject|"+native.QKey(native.GlobalProject, "x.md")+"|s1"); n != 1 {
		t.Errorf("global x.md inject rows = %d, want 1:\n%s", n, w)
	}
	if n := strings.Count(w, "|inject|"+native.QKey(proj, "x.md")+"|s1"); n != 1 {
		t.Errorf("project x.md inject rows = %d, want 1:\n%s", n, w)
	}
	rt := filepath.Join(f.mem, ".runtime")
	want := []string{native.QKey(native.GlobalProject, "x.md"), native.QKey(proj, "x.md")}
	sort.Strings(want)
	for _, name := range []string{"injected-s1.list", "rendered-s1.list"} {
		if got := listLines(t, filepath.Join(rt, name)); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// Review focus 1: lists written by an older binary (bare slugs) mixed with
// QKey lines from the new one. A bare line suppresses that slug in every
// store (as before), nothing is logged twice, and the next write leaves
// only QKeys.
func TestSessionLists_MixedBareAndQualified(t *testing.T) {
	f := newStoreFixture(t)
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	proj := native.StoreFor(f.claude, "/tmp/proj").Project
	f.globalFact(t, "x", "name: globalx\ntype: knowledge\nkeywords: [alphaword]", "GLOBAL X")
	f.factFM(t, "/tmp/proj", "x", "name: projx\ntype: knowledge\nkeywords: [alphaword]", "PROJECT X")
	f.factFM(t, "/tmp/proj", "y", "name: projy\ntype: knowledge\nkeywords: [alphaword]", "PROJECT Y")
	f.factFM(t, "/tmp/proj", "z", "name: projz\ntype: knowledge\nkeywords: [alphaword]", "PROJECT Z")
	rt := filepath.Join(f.mem, ".runtime")
	os.MkdirAll(rt, 0o755)
	seed := "x.md\n" + native.QKey(proj, "y.md") + "\n"
	for _, name := range []string{"injected-s1.list", "rendered-s1.list"} {
		os.WriteFile(filepath.Join(rt, name), []byte(seed), 0o600)
	}
	for i := 0; i < 2; i++ {
		out, _, _ := runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","prompt":"alphaword"}`, "inject", "--event=UserPromptSubmit")
		ctx := additionalContext(t, out)
		if strings.Contains(ctx, "GLOBAL X") || strings.Contains(ctx, "PROJECT X") || strings.Contains(ctx, "PROJECT Y") {
			t.Errorf("prompt %d: a fact already in context re-rendered: %q", i, ctx)
		}
		if i == 0 && !strings.Contains(ctx, "PROJECT Z") {
			t.Errorf("prompt %d: z.md is new and must render: %q", i, ctx)
		}
	}
	wal, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	w := string(wal)
	if n := strings.Count(w, "|inject|"); n != 1 || !strings.Contains(w, "|inject|"+native.QKey(proj, "z.md")+"|s1") {
		t.Errorf("want exactly one inject row (project z.md):\n%s", w)
	}
	want := []string{
		native.QKey(native.GlobalProject, "x.md"), native.QKey(proj, "x.md"),
		native.QKey(proj, "y.md"), native.QKey(proj, "z.md"),
	}
	sort.Strings(want)
	for _, name := range []string{"injected-s1.list", "rendered-s1.list"} {
		if got := listLines(t, filepath.Join(rt, name)); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s = %q, want only QKeys %q", name, got, want)
		}
	}
}

// persistInjected logs one inject row per fact (project-qualified), not per
// file name: project x.md after global x.md gets its own row; a bare line
// from an older binary still suppresses that slug in every store.
func TestPersistInjected_DedupByQKey(t *testing.T) {
	mem := t.TempDir()
	t.Setenv("CLAUDE_MEMORY_DIR", mem)
	t.Setenv("HYPOMNEMA_TODAY", "2026-11-24")
	os.WriteFile(filepath.Join(mem, ".wal"), nil, 0o644)
	g := native.QKey(native.GlobalProject, "x.md")
	p := native.QKey("-tmp-proj", "x.md")
	py := native.QKey("-tmp-proj", "y.md")
	persistInjected([]string{g, "y.md"}, []string{p, g, py}, "s1")
	wal, _ := os.ReadFile(filepath.Join(mem, ".wal"))
	w := string(wal)
	if n := strings.Count(w, "|inject|"+p+"|s1"); n != 1 {
		t.Errorf("project x.md rows = %d, want 1:\n%s", n, w)
	}
	if strings.Contains(w, "|inject|"+g+"|") {
		t.Errorf("global x.md was already logged this session:\n%s", w)
	}
	if strings.Contains(w, "|inject|"+py+"|") {
		t.Errorf("bare y.md from an older binary suppresses y.md in any store:\n%s", w)
	}
	got := listLines(t, sessionListPath("s1"))
	want := []string{g, p, py, "y.md"}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("injected list = %q, want %q", got, want)
	}
}
