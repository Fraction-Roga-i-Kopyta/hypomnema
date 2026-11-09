package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/stats"
)

// statsRoot is a fresh temp dir in its symlink-resolved form (macOS
// t.TempDir lives under /var → /private/var), so every path the binary
// derives from it has one spelling.
func statsRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// statsEnv is the hermetic install under root: HOME, CLAUDE_HOME, the memory
// dir, the global store and the project anchor all live there. Nothing is
// created on disk.
func statsEnv(root string) map[string]string {
	claude := filepath.Join(root, ".claude")
	return map[string]string{
		"HOME":                 root,
		"CLAUDE_HOME":          claude,
		"CLAUDE_MEMORY_DIR":    filepath.Join(claude, "memory"),
		"HYPOMNEMA_GLOBAL_DIR": filepath.Join(claude, "memory-global"),
		"CLAUDE_PROJECT_CWD":   filepath.Join(root, "work", "proj"),
		"HYPOMNEMA_TODAY":      "2026-11-09",
	}
}

func writeStatsFact(t *testing.T, dir, file, frontmatter string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\n" + frontmatter + "\n---\nBody of " + file + ".\n"
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// statsWorld seeds root: a project store with alpha (feedback), beta
// (candidate mistake) and delta (recalled only), a global store with gamma
// (pinned strategy), a WAL with session s1 (parent + subagent agent1) and a
// second session s2, and gone.md injected without a file. The sidecar is
// built by `sidecar rebuild`. Returns the env and the project store tag.
func statsWorld(t *testing.T, root string) (env map[string]string, tag string) {
	t.Helper()
	env = statsEnv(root)
	if err := os.MkdirAll(env["CLAUDE_PROJECT_CWD"], 0o755); err != nil {
		t.Fatal(err)
	}
	st := native.StoreFor(env["CLAUDE_HOME"], env["CLAUDE_PROJECT_CWD"])
	tag = st.Project
	writeStatsFact(t, st.Dir, "alpha.md", "type: feedback\nname: alpha-rule\ndescription: Alpha rule for stats\ncreated: 2026-11-01\nstatus: active\nkeywords: [alpha, stats]")
	writeStatsFact(t, st.Dir, "beta.md", "type: mistake\nname: beta-bug\ndescription: Beta bug\ncreated: 2026-10-20\nstatus: candidate")
	writeStatsFact(t, st.Dir, "delta.md", "type: knowledge\nname: delta-fact\ndescription: Delta fact\ncreated: 2026-09-01")
	writeStatsFact(t, env["HYPOMNEMA_GLOBAL_DIR"], "gamma.md", "type: strategy\nname: gamma-way\ndescription: Gamma way\ncreated: 2026-08-15\nstatus: pinned")

	p := func(slug string) string { return native.QKey(tag, slug) }
	g := func(slug string) string { return native.QKey(native.GlobalProject, slug) }
	wal := strings.Join([]string{
		"2026-11-08|inject|" + p("alpha.md") + "|s1",
		"2026-11-08|inject|" + p("beta.md") + "|s1",
		"2026-11-08|inject|" + p("gone.md") + "|s1",
		"2026-11-09|inject|" + p("alpha.md") + "|s1",
		"2026-11-09|inject|" + g("gamma.md") + "|s1:agent1",
		"2026-11-09|recall|" + p("delta.md") + "|s1",
		"2026-11-09|cite-useful|" + p("alpha.md") + "|s1",
		"2026-11-09|cite-silent|" + p("beta.md") + "|s1",
		"2026-11-09|cite-silent|" + g("gamma.md") + "|s1:agent1",
		"2026-11-09|cite-undelivered|" + p("zeta.md") + "|s1",
		"2026-11-09|inject|" + p("alpha.md") + "|s2",
		"2026-11-09|cite-useful|" + g("gamma.md") + "|s2",
	}, "\n") + "\n"
	mem := env["CLAUDE_MEMORY_DIR"]
	if err := os.MkdirAll(mem, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mem, ".wal"), []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := run(t, env, "sidecar", "rebuild"); code != 0 {
		t.Fatalf("sidecar rebuild: exit=%d stderr=%s", code, errOut)
	}
	return env, tag
}

// withEnv returns a copy of env with extra keys set.
func withEnv(env map[string]string, kv ...string) map[string]string {
	out := make(map[string]string, len(env)+len(kv)/2)
	for k, v := range env {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func runStatsJSON(t *testing.T, env map[string]string, args ...string) (stats.Report, string) {
	t.Helper()
	out, errOut, code := run(t, env, append([]string{"stats", "--json"}, args...)...)
	if code != 0 {
		t.Fatalf("stats %v: exit=%d stderr=%s", args, code, errOut)
	}
	var rep stats.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("stats %v: bad JSON %v\n%s", args, err, out)
	}
	if strings.Contains(out, "null") || strings.Contains(out, `\u001f`) {
		t.Fatalf("stats %v: null slice or QKey separator in JSON:\n%s", args, out)
	}
	return rep, out
}

func TestStats_RequiresJSON(t *testing.T) {
	env := statsEnv(statsRoot(t))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"stats", "--scope", "session", "--session", "s1"}, "--json"},
		{[]string{"stats", "--json"}, "--scope"},
		{[]string{"stats", "--json", "--scope", "bogus"}, "scope"},
		{[]string{"stats", "--json", "--scope"}, "--scope needs a value"},
		{[]string{"stats", "--json", "--scope", "session", "--bogus"}, "unknown flag"},
		{[]string{"stats", "--json", "--scope", "session", "--session", "s1", "extra"}, "unexpected argument"},
		{[]string{"stats", "--json", "--scope", "fact"}, "slug"},
		{[]string{"stats", "--json", "--scope", "fact", "a", "b"}, "unexpected argument"},
		{[]string{"stats", "--json", "--scope", "session", "--session", "s|1"}, "session id"},
		{[]string{"stats", "--json", "--scope", "session", "--session", "s1", "--project", "global"}, "--project"},
	} {
		_, errOut, code := run(t, env, tc.args...)
		if code != 2 || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: exit=%d stderr=%q (want exit 2 mentioning %q)", tc.args, code, errOut, tc.want)
		}
	}
}

func TestStats_SessionScope(t *testing.T) {
	env, tag := statsWorld(t, statsRoot(t))
	rep, _ := runStatsJSON(t, env, "--scope", "session", "--session", "s1")
	if rep.SchemaVersion != 1 || rep.Scope != "session" || rep.Today != "2026-11-09" || rep.Project != tag {
		t.Fatalf("header: %+v", rep)
	}
	if rep.Fact != nil || rep.ProjectStats != nil || rep.All != nil {
		t.Fatalf("only the session section is set: %+v", rep)
	}
	s := rep.Session
	if s == nil {
		t.Fatal("no session section")
	}
	if s.SessionID != "s1" || s.Injected != 5 || s.InjectedFacts != 4 || s.Useful != 1 || s.Silent != 2 ||
		s.Undelivered != 1 || s.Recalled != 1 || s.UsefulShare != 1.0/3 || !s.PromptInject {
		t.Fatalf("counts: %+v", s)
	}
	fk := func(project, slug string) stats.FactKey { return stats.FactKey{Project: project, Slug: slug} }
	wantDelivered := []stats.FactKey{
		fk(tag, "delta.md"), fk(native.GlobalProject, "gamma.md"), fk(tag, "alpha.md"),
		fk(tag, "gone.md"), fk(tag, "beta.md"),
	}
	if !reflect.DeepEqual(s.Delivered, wantDelivered) {
		t.Fatalf("delivered:\n got %+v\nwant %+v", s.Delivered, wantDelivered)
	}
	if !reflect.DeepEqual(s.UsefulFacts, []stats.FactKey{fk(tag, "alpha.md")}) {
		t.Fatalf("useful_facts: %+v", s.UsefulFacts)
	}
	var order []string
	for _, f := range s.Top {
		order = append(order, f.Slug)
	}
	if strings.Join(order, ",") != "alpha.md,beta.md,gamma.md,gone.md" {
		t.Fatalf("top order: %v", order)
	}
	alpha := s.Top[0]
	if alpha.Injected != 2 || alpha.Useful != 1 || alpha.Name != "alpha-rule" || alpha.Type != "feedback" ||
		alpha.Display != "work-proj" || alpha.RefCount != 3 || alpha.Status != "active" ||
		alpha.Path != filepath.Join(native.StoreFor(env["CLAUDE_HOME"], env["CLAUDE_PROJECT_CWD"]).Dir, "alpha.md") {
		t.Fatalf("alpha enrichment: %+v", alpha)
	}
	if beta := s.Top[1]; beta.Status != "candidate" || beta.Useful != 0 {
		t.Fatalf("beta: %+v", beta)
	}
	if gamma := s.Top[2]; gamma.Project != native.GlobalProject || gamma.Display != "global" ||
		gamma.Status != "pinned" || gamma.Name != "gamma-way" {
		t.Fatalf("gamma: %+v", gamma)
	}
	if gone := s.Top[3]; gone.Path != "" || gone.Status != "" || gone.Injected != 1 {
		t.Fatalf("gone: %+v", gone)
	}

	// Session id from the environment: HYPOMNEMA_SESSION_ID wins over
	// CLAUDE_CODE_SESSION_ID, which is the fallback.
	rep, _ = runStatsJSON(t, withEnv(env, "CLAUDE_CODE_SESSION_ID", "s2"), "--scope", "session")
	if rep.Session.SessionID != "s2" || rep.Session.Injected != 1 || rep.Session.Useful != 1 {
		t.Fatalf("CLAUDE_CODE_SESSION_ID fallback: %+v", rep.Session)
	}
	rep, _ = runStatsJSON(t, withEnv(env, "HYPOMNEMA_SESSION_ID", "s1", "CLAUDE_CODE_SESSION_ID", "s2"), "--scope", "session")
	if rep.Session.SessionID != "s1" {
		t.Fatalf("HYPOMNEMA_SESSION_ID precedence: %+v", rep.Session)
	}
	rep, _ = runStatsJSON(t, withEnv(env, "HYPOMNEMA_PROMPT_INJECT", "off"), "--scope", "session", "--session", "s1")
	if rep.Session.PromptInject {
		t.Fatal("prompt_inject must be false in pull-only mode")
	}
}

func TestStats_FactScope(t *testing.T) {
	env, tag := statsWorld(t, statsRoot(t))
	if _, errOut, code := run(t, env, "mark", "alpha", "less"); code != 0 {
		t.Fatalf("mark less: exit=%d stderr=%s", code, errOut)
	}
	rep, _ := runStatsJSON(t, withEnv(env, "HYPOMNEMA_SESSION_ID", "s1"), "--scope", "fact", "alpha")
	if rep.Scope != "fact" || rep.Project != tag || rep.Session != nil || rep.Fact == nil {
		t.Fatalf("header: %+v", rep)
	}
	f := rep.Fact
	storeDir := native.StoreFor(env["CLAUDE_HOME"], env["CLAUDE_PROJECT_CWD"]).Dir
	want := stats.FactCount{
		FactRef: stats.FactRef{
			Project: tag, Display: "work-proj", Slug: "alpha.md", Name: "alpha-rule", Type: "feedback",
			Status: "active", Description: "Alpha rule for stats", Path: filepath.Join(storeDir, "alpha.md"),
			Created: "2026-11-01", RefCount: 3, Effectiveness: f.Effectiveness,
			LastInjected: "2026-11-09", LastUseful: "2026-11-09", Injected30d: 3, Useful30d: 1,
			Marks: []string{"less"},
		},
		Injected: 2, Useful: 1,
	}
	if !reflect.DeepEqual(*f, want) {
		t.Fatalf("card:\n got %+v\nwant %+v", *f, want)
	}
	if f.Effectiveness <= 0.5 {
		t.Fatalf("alpha was cited useful, effectiveness %v", f.Effectiveness)
	}

	// Without a session id the session counts are zero; --project picks the store.
	rep, _ = runStatsJSON(t, env, "--scope", "fact", "--project", "global", "gamma.md")
	if g := rep.Fact; g.Project != native.GlobalProject || g.Status != "pinned" || g.Injected != 0 ||
		g.Useful != 0 || g.Injected30d != 1 || g.Useful30d != 1 || len(g.Marks) != 0 {
		t.Fatalf("gamma: %+v", g)
	}
	_, errOut, code := run(t, env, "stats", "--json", "--scope", "fact", "--project", "global", "alpha")
	if code != 1 || !strings.Contains(errOut, "no fact named") {
		t.Fatalf("alpha is not global: exit=%d stderr=%s", code, errOut)
	}
}

func TestStats_EmptyWorld(t *testing.T) {
	root := statsRoot(t)
	env := statsEnv(root)
	rep, _ := runStatsJSON(t, env, "--scope", "session", "--session", "s1")
	s := rep.Session
	if s == nil || s.Injected != 0 || s.Useful != 0 || s.UsefulShare != 0 || len(s.Top) != 0 ||
		len(s.Delivered) != 0 || len(s.UsefulFacts) != 0 || rep.Project == "" {
		t.Fatalf("empty world: %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(env["CLAUDE_MEMORY_DIR"], ".sidecar.db")); !os.IsNotExist(err) {
		t.Fatalf("stats created a sidecar: %v", err)
	}

	_, errOut, code := run(t, env, "stats", "--json", "--scope", "session")
	if code != 2 || !strings.Contains(errOut, "session id") {
		t.Fatalf("no session id: exit=%d stderr=%s", code, errOut)
	}
	_, errOut, code = run(t, env, "stats", "--json", "--scope", "fact", "x.md")
	if code != 1 || !strings.Contains(errOut, "no fact named") {
		t.Fatalf("missing fact: exit=%d stderr=%s", code, errOut)
	}
}

// TestStats_UnreadableSidecar: a corrupt sidecar is not fatal — the report
// falls back to the projection defaults.
func TestStats_CorruptSidecar(t *testing.T) {
	env, _ := statsWorld(t, statsRoot(t))
	db := filepath.Join(env["CLAUDE_MEMORY_DIR"], ".sidecar.db")
	for _, p := range []string{db, db + "-wal", db + "-shm"} {
		_ = os.Remove(p)
	}
	if err := os.WriteFile(db, []byte("not a sqlite database at all, just junk bytes......"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, _ := runStatsJSON(t, env, "--scope", "fact", "alpha")
	if rep.Fact.Effectiveness != 0.5 || rep.Fact.RefCount != 0 || rep.Fact.Injected30d != 3 {
		t.Fatalf("corrupt sidecar: %+v", rep.Fact)
	}
}

func TestStats_ProjectScopeCLI(t *testing.T) {
	root := statsRoot(t)
	env, tag := statsWorld(t, root)
	// Transcripts of s1 only: days count s1 (and its subagent), not s2.
	projDir := filepath.Join(env["CLAUDE_HOME"], "projects", tag)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "s1.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := run(t, env, "mark", "alpha", "rewrite"); code != 0 {
		t.Fatalf("mark: %s", errOut)
	}
	rep, _ := runStatsJSON(t, env, "--scope", "project")
	p := rep.ProjectStats
	if p == nil || rep.Scope != "project" || p.Project != tag || p.Visible != 4 || p.Local != 3 || p.Global != 1 ||
		p.Candidates != 1 || p.Pinned != 1 {
		t.Fatalf("project_stats: %+v", p)
	}
	if len(p.Days) != 7 || p.Days[6].Date != "2026-11-09" || p.Days[6].Injected != 2 || p.Days[6].Silent != 2 || p.Days[6].Useful != 1 {
		t.Fatalf("days: %+v", p.Days)
	}
	if len(p.Queue) != 1 || p.Queue[0].Slug != "alpha.md" {
		t.Fatalf("queue: %+v", p.Queue)
	}
	if p.Health.OK+p.Health.Warn+p.Health.Fail == 0 {
		t.Fatalf("health empty: %+v", p.Health)
	}
	for _, pr := range p.Health.Problems {
		if strings.Contains(pr, root) || len([]rune(pr)) > 120 {
			t.Fatalf("problem leaks root or too long: %q", pr)
		}
	}

	for _, args := range [][]string{
		{"--scope", "project", "extra"},
		{"--scope", "project", "--session", "s1"},
	} {
		_, errOut, code := run(t, env, append([]string{"stats", "--json"}, args...)...)
		if code != 2 {
			t.Errorf("%v: exit=%d stderr=%s", args, code, errOut)
		}
	}
	_, _, code := run(t, env, "stats", "--json", "--scope", "project", "--project", "nope")
	if code != 1 {
		t.Errorf("unknown project: exit=%d", code)
	}
	rep2, _ := runStatsJSON(t, env, "--scope", "project", "--project", tag)
	if rep2.ProjectStats.Project != tag {
		t.Fatalf("--project tag: %+v", rep2.ProjectStats)
	}
}

func TestScrubPaths_RootIndependent(t *testing.T) {
	var got []string
	for _, root := range []string{"/tmp/x", "/private/var/folders/zz/abcdefghijklmnop/T/TestLong123/001"} {
		claude := root + "/.claude"
		d := claude + "/settings.json unreadable: open " + claude + "/settings.json: no such file"
		got = append(got, stats.Problem(scrubPaths(d, claude, root)))
	}
	if got[0] != got[1] || strings.Contains(got[0], "/tmp") {
		t.Fatalf("root-dependent problems:\n%q\n%q", got[0], got[1])
	}
}

func TestStats_ProjectEmptyWorld(t *testing.T) {
	env := statsEnv(statsRoot(t))
	rep, _ := runStatsJSON(t, env, "--scope", "project")
	p := rep.ProjectStats
	if p == nil || p.Visible != 0 || p.Local != 0 || p.Global != 0 || len(p.Days) != 7 || len(p.Queue) != 0 ||
		len(p.Triage.Noisy) != 0 || p.Health.OK+p.Health.Warn+p.Health.Fail == 0 {
		t.Fatalf("empty world: %+v", p)
	}
	for _, d := range p.Days {
		if d.Injected != 0 || d.Useful != 0 || d.Silent != 0 {
			t.Fatalf("days: %+v", p.Days)
		}
	}
	if _, err := os.Stat(filepath.Join(env["CLAUDE_MEMORY_DIR"], ".sidecar.db")); !os.IsNotExist(err) {
		t.Fatalf("stats created a sidecar: %v", err)
	}
}
