package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/inject"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// factHead is store fixture fact() plus a description: the head gate reads
// name, description, keywords and domains, never the body.
func (f storeFixture) factHead(t *testing.T, projectDir, name, desc, body string) {
	t.Helper()
	dir := native.StoreFor(f.claude, projectDir).Dir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fm := "---\nname: " + name + "\ntype: mistake\ndescription: " + desc + "\nkeywords: [" + name + "]\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func agentCallLine(id, subagentType, description, prompt string) string {
	in := map[string]any{"description": description, "prompt": prompt}
	if subagentType != "" {
		in["subagent_type"] = subagentType
	}
	b, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{
		"content": []map[string]any{{"type": "tool_use", "id": id, "name": "Agent", "input": in}}}})
	return string(b)
}

func userPromptLine(text string) string {
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"content": text}})
	return string(b)
}

func toolResultLine(id string) string {
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{
		"content": []map[string]any{{"type": "tool_result", "tool_use_id": id, "content": "ok"}}}})
	return string(b)
}

func subagentEnv(sessionID, agentID, agentType, cwd, parentTranscript, agentTranscript string) string {
	b, _ := json.Marshal(map[string]string{
		"session_id": sessionID, "agent_id": agentID, "agent_type": agentType, "cwd": cwd,
		"transcript_path": parentTranscript, "agent_transcript_path": agentTranscript,
	})
	return string(b)
}

func hookEventName(t *testing.T, out string) string {
	t.Helper()
	var env struct {
		HookSpecificOutput struct {
			HookEventName string `json:"hookEventName"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("bad envelope %v: %s", err, out)
	}
	return env.HookSpecificOutput.HookEventName
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// hasRow reports a WAL row with this event, a target ending in slug (the
// project qualifier in front of it is not under test here) and this session.
func hasRow(wal, event, slug, session string) bool {
	for _, ln := range strings.Split(wal, "\n") {
		f := strings.Split(ln, "|")
		if len(f) == 4 && f[1] == event && strings.HasSuffix(f[2], slug) && f[3] == session {
			return true
		}
	}
	return false
}

func TestSubagentStart_RanksByPendingTask(t *testing.T) {
	f := newStoreFixture(t)
	f.factHead(t, "/tmp/proj", "dockercache", "docker layer cache", "docker layer cache")
	f.factHead(t, "/tmp/proj", "pgmigration", "postgres migration lock", "postgres migration lock")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	parent := writeTranscript(t,
		userPromptLine("the dockercache layer is slow"),
		agentCallLine("t1", "general-purpose", "Fix migration", "Investigate the pgmigration postgres lock timeout"),
	)
	out, _, code := runStdin(t, f.env, subagentEnv("s1", "a1", "general-purpose", "/tmp/proj", parent, ""),
		"inject", "--event=SubagentStart")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if ev := hookEventName(t, out); ev != "SubagentStart" {
		t.Fatalf("hookEventName = %q, want SubagentStart", ev)
	}
	ctx := additionalContext(t, out)
	p, d := strings.Index(ctx, "postgres migration lock"), strings.Index(ctx, "docker layer cache")
	if p < 0 || (d >= 0 && d < p) {
		t.Fatalf("the task-matching fact must rank first; ctx=%q", ctx)
	}
	wal, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	if !hasRow(string(wal), "inject", "pgmigration.md", "s1:a1") {
		t.Errorf("missing inject row under the subagent key:\n%s", wal)
	}
	rt := filepath.Join(f.mem, ".runtime")
	if !exists(filepath.Join(rt, "injected-s1_a1.list")) || !exists(filepath.Join(rt, "rendered-s1_a1.list")) {
		t.Errorf("subagent lists missing in %s", rt)
	}
	if exists(filepath.Join(rt, "injected-s1.list")) || exists(filepath.Join(rt, "rendered-s1.list")) {
		t.Errorf("parent session lists must not be written")
	}
	if !strings.Contains(ctx, inject.SubagentCiteInstruction) || strings.Contains(ctx, inject.CiteInstruction) {
		t.Errorf("SubagentStart context must carry the report instruction only; ctx=%q", ctx)
	}
}

// A subagent with no Agent call of its own has no task brief: the parent's
// recent prompts still shape the ranking query, but nothing is injected.
func TestSubagentStart_FallsBackToParentPrompts(t *testing.T) {
	f := newStoreFixture(t)
	f.factHead(t, "/tmp/proj", "dockercache", "docker layer cache", "docker layer cache")
	f.factHead(t, "/tmp/proj", "pgmigration", "postgres migration lock", "postgres migration lock")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	parent := writeTranscript(t,
		agentCallLine("t1", "general-purpose", "Fix docker", "dockercache layer"),
		toolResultLine("t1"),
		userPromptLine("now look at the pgmigration postgres lock"),
		userPromptLine("ok"),
		userPromptLine("да"),
	)
	out, _, code := runStdin(t, f.env, subagentEnv("s1", "a2", "general-purpose", "/tmp/proj", parent, ""),
		"inject", "--event=SubagentStart")
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("no own call -> no injection; code=%d out=%q", code, out)
	}
	// The first-start marker is written anyway.
	if !exists(filepath.Join(f.mem, ".runtime", "rendered-s1_a2.list")) {
		t.Errorf("rendered-list marker must be written even when nothing rendered")
	}
}

func TestSubagentStart_TopFiveFacts(t *testing.T) {
	f := newStoreFixture(t)
	// Each head carries two unique terms (gizmoN sprocketN); the shared
	// "widget rendering note" is corpus-common and does not count.
	var brief []string
	for i := 1; i <= 7; i++ {
		f.factHead(t, "/tmp/proj", fmt.Sprintf("widget%d", i), fmt.Sprintf("gizmo%d sprocket%d widget rendering note", i, i), "widget rendering note")
		brief = append(brief, fmt.Sprintf("gizmo%d sprocket%d", i, i))
	}
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	parent := writeTranscript(t, agentCallLine("t1", "general-purpose", "widgets", "widget rendering "+strings.Join(brief, " ")))
	out, _, _ := runStdin(t, f.env, subagentEnv("s1", "a1", "general-purpose", "/tmp/proj", parent, ""),
		"inject", "--event=SubagentStart")
	if n := strings.Count(additionalContext(t, out), "\n## "); n != 5 {
		t.Errorf("want exactly 5 fact headers for 7 short facts, got %d", n)
	}
}

func TestSubagentStart_ByteBudget(t *testing.T) {
	f := newStoreFixture(t)
	var brief []string
	for i := 1; i <= 5; i++ {
		f.factHead(t, "/tmp/proj", fmt.Sprintf("widget%d", i), fmt.Sprintf("gizmo%d sprocket%d", i, i), "widget rendering note "+strings.Repeat("detail ", 300))
		brief = append(brief, fmt.Sprintf("gizmo%d sprocket%d", i, i))
	}
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	parent := writeTranscript(t, agentCallLine("t1", "general-purpose", "widgets", "widget rendering "+strings.Join(brief, " ")))
	out, _, _ := runStdin(t, f.env, subagentEnv("s1", "a1", "general-purpose", "/tmp/proj", parent, ""),
		"inject", "--event=SubagentStart")
	if ctx := additionalContext(t, out); ctx == "" || len(ctx) > 5000 {
		t.Errorf("render is %d bytes, want 1..5000", len(ctx))
	}
}

func TestSubagentStart_SkipList(t *testing.T) {
	f := newStoreFixture(t)
	f.factHead(t, "/tmp/proj", "dockercache", "docker layer cache", "docker layer cache")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	parent := writeTranscript(t,
		agentCallLine("t1", "Explore", "docker", "dockercache layer"),
		agentCallLine("t2", "Explore", "docker", "dockercache layer"))
	rt := filepath.Join(f.mem, ".runtime")
	for _, typ := range []string{"fork", "Explore", "claude-code-guide", "statusline-setup"} {
		out, _, code := runStdin(t, f.env, subagentEnv("s1", "x-"+typ, typ, "/tmp/proj", parent, ""),
			"inject", "--event=SubagentStart")
		if code != 0 || strings.TrimSpace(out) != "" {
			t.Errorf("%s must be skipped; code=%d out=%q", typ, code, out)
		}
		if exists(filepath.Join(rt, "injected-s1_x-"+typ+".list")) || exists(filepath.Join(rt, "rendered-s1_x-"+typ+".list")) {
			t.Errorf("%s: skipped agent must leave no runtime files", typ)
		}
	}
	wal, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	if strings.Contains(string(wal), "|inject|") {
		t.Errorf("skipped types must not write inject rows:\n%s", wal)
	}
	f.env["HYPOMNEMA_SUBAGENT_SKIP"] = ""
	out, _, _ := runStdin(t, f.env, subagentEnv("s1", "e1", "Explore", "/tmp/proj", parent, ""),
		"inject", "--event=SubagentStart")
	if !strings.Contains(additionalContext(t, out), "docker layer cache") {
		t.Errorf("empty override must let Explore through; out=%q", out)
	}
	f.env["HYPOMNEMA_SUBAGENT_SKIP"] = " reviewer , "
	out, _, _ = runStdin(t, f.env, subagentEnv("s1", "r1", "reviewer", "/tmp/proj", parent, ""),
		"inject", "--event=SubagentStart")
	if strings.TrimSpace(out) != "" {
		t.Errorf("reviewer is in the override list; out=%q", out)
	}
	out, _, _ = runStdin(t, f.env, subagentEnv("s1", "e2", "Explore", "/tmp/proj", parent, ""),
		"inject", "--event=SubagentStart")
	if !strings.Contains(additionalContext(t, out), "docker layer cache") {
		t.Errorf("Explore is not in the override list; out=%q", out)
	}
}

func TestSubagentStart_WildcardSkipsEveryType(t *testing.T) {
	f := newStoreFixture(t)
	f.factHead(t, "/tmp/proj", "dockercache", "docker layer cache", "docker layer cache")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	f.env["HYPOMNEMA_SUBAGENT_SKIP"] = "*"
	parent := writeTranscript(t, userPromptLine("dockercache"))
	rt := filepath.Join(f.mem, ".runtime")
	out, _, code := runStdin(t, f.env, subagentEnv("s1", "a1", "general-purpose", "/tmp/proj", parent, ""),
		"inject", "--event=SubagentStart")
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Errorf("wildcard skip: want silent exit 0; code=%d out=%q", code, out)
	}
	if exists(filepath.Join(rt, "injected-s1_a1.list")) || exists(filepath.Join(rt, "rendered-s1_a1.list")) {
		t.Errorf("wildcard skip: must leave no key files")
	}
	wal, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	if strings.Contains(string(wal), "|inject|") {
		t.Errorf("wildcard skip: must not write inject rows:\n%s", wal)
	}
}

func TestSubagentStart_ResumeIsNoop(t *testing.T) {
	f := newStoreFixture(t)
	f.factHead(t, "/tmp/proj", "dockercache", "docker layer cache", "docker layer cache")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	parent := writeTranscript(t, agentCallLine("t1", "general-purpose", "docker", "dockercache"))
	in := subagentEnv("s1", "a1", "general-purpose", "/tmp/proj", parent, "")
	first, _, _ := runStdin(t, f.env, in, "inject", "--event=SubagentStart")
	if !strings.Contains(additionalContext(t, first), "docker layer cache") ||
		!exists(filepath.Join(f.mem, ".runtime", "rendered-s1_a1.list")) {
		t.Fatalf("first start must deliver and write the marker; out=%q", first)
	}
	before, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	out, _, code := runStdin(t, f.env, in, "inject", "--event=SubagentStart")
	after, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Errorf("second start of the same agent must emit nothing; out=%q", out)
	}
	if string(after) != string(before) {
		t.Errorf("second start must not write WAL rows:\nbefore=%q\nafter=%q", before, after)
	}
}

func TestSubagentQuery_FromTaskFlag(t *testing.T) {
	// subagentQuery calls pickAgentCall, which claims the call it picks under
	// memoryDir()/.runtime/ — isolate that or an in-process call (unlike
	// runStdin's subprocess, which gets a hermetic environment) falls back to
	// the real ~/.claude/memory.
	t.Setenv("CLAUDE_MEMORY_DIR", t.TempDir())
	own := writeTranscript(t, agentCallLine("t1", "general-purpose", "Fix migration", "Investigate the pgmigration lock"))
	query, brief, fromTask := subagentQuery(own, "general-purpose", "a1")
	if !fromTask {
		t.Errorf("want fromTask=true for this agent's own pending call; query=%q", query)
	}
	if !strings.Contains(query, "Fix migration") {
		t.Errorf("query must contain the call's description; got %q", query)
	}

	fallback := writeTranscript(t,
		agentCallLine("t1", "general-purpose", "Fix docker", "dockercache layer"),
		toolResultLine("t1"),
		userPromptLine("now look at the pgmigration lock"),
	)
	_, brief2, fromTask2 := subagentQuery(fallback, "general-purpose", "a2")
	if fromTask2 || brief2 != "" {
		t.Errorf("want fromTask=false and an empty brief on the parent-prompts fallback; brief=%q", brief2)
	}
	if !strings.Contains(brief, "Fix migration") || !strings.Contains(brief, "Investigate the pgmigration lock") {
		t.Errorf("brief must carry the call's description and prompt; got %q", brief)
	}
}

func agentAckLine(id, agent string) string {
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{
		"content": []map[string]any{{"type": "tool_result", "tool_use_id": id,
			"content": []map[string]any{{"type": "text", "text": "Async agent launched successfully.\nagentId: " + agent + " (internal ID)"}}}}}})
	return string(b)
}

func TestSubagentStart_ParallelLaunchesGetDistinctCalls(t *testing.T) {
	f := newStoreFixture(t)
	f.factHead(t, "/tmp/proj", "f1", "alpha widget content", "alpha widget content")
	f.factHead(t, "/tmp/proj", "f2", "bravo widget content", "bravo widget content")
	f.factHead(t, "/tmp/proj", "f3", "charlie widget content", "charlie widget content")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	parent := writeTranscript(t,
		agentCallLine("t1", "general-purpose", "task one", "alpha widget content"),
		agentCallLine("t2", "general-purpose", "task two", "bravo widget content"),
		agentCallLine("t3", "general-purpose", "task three", "charlie widget content"),
	)
	ranksFirst := func(agentID, want string, others ...string) {
		t.Helper()
		out, _, code := runStdin(t, f.env, subagentEnv("s1", agentID, "general-purpose", "/tmp/proj", parent, ""),
			"inject", "--event=SubagentStart")
		if code != 0 {
			t.Fatalf("%s: exit %d", agentID, code)
		}
		ctx := additionalContext(t, out)
		wi := strings.Index(ctx, want)
		if wi < 0 {
			t.Fatalf("%s: want %q in ctx=%q", agentID, want, ctx)
		}
		for _, o := range others {
			if oi := strings.Index(ctx, o); oi >= 0 && oi < wi {
				t.Fatalf("%s: %q ranked before %q; ctx=%q", agentID, o, want, ctx)
			}
		}
	}
	// Started in launch order: a1 claims t1 (f1), a2 claims t2 (f2), a3
	// claims t3 (f3) — none of the three parallel launches ranks against a
	// sibling's task.
	ranksFirst("a1", "alpha widget content", "bravo widget content", "charlie widget content")
	ranksFirst("a2", "bravo widget content", "alpha widget content", "charlie widget content")
	ranksFirst("a3", "charlie widget content", "alpha widget content", "bravo widget content")
}

func TestSubagentStart_OwnAckBeatsOlderUnclaimedCall(t *testing.T) {
	f := newStoreFixture(t)
	f.factHead(t, "/tmp/proj", "f1", "alpha widget content", "alpha widget content")
	f.factHead(t, "/tmp/proj", "f2", "bravo widget content", "bravo widget content")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	parent := writeTranscript(t,
		agentCallLine("t1", "general-purpose", "task one", "alpha widget content"),
		agentCallLine("t2", "general-purpose", "task two", "bravo widget content"),
		agentAckLine("t2", "ab2"),
	)
	// ab2 is the acknowledged owner of t2 — even though t1 is the older
	// unclaimed call, ab2's own acknowledgement wins.
	out, _, code := runStdin(t, f.env, subagentEnv("s1", "ab2", "general-purpose", "/tmp/proj", parent, ""),
		"inject", "--event=SubagentStart")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	ctx := additionalContext(t, out)
	bi, ai := strings.Index(ctx, "bravo widget content"), strings.Index(ctx, "alpha widget content")
	if bi < 0 || (ai >= 0 && ai < bi) {
		t.Fatalf("ab2 (own ack for t2) must rank t2's fact first; ctx=%q", ctx)
	}
	// ax1 has no acknowledgement; t2 is already answered (by ab2's own ack)
	// so the only pending call left is t1.
	out2, _, code2 := runStdin(t, f.env, subagentEnv("s1", "ax1", "general-purpose", "/tmp/proj", parent, ""),
		"inject", "--event=SubagentStart")
	if code2 != 0 {
		t.Fatalf("exit %d", code2)
	}
	ctx2 := additionalContext(t, out2)
	ai2, bi2 := strings.Index(ctx2, "alpha widget content"), strings.Index(ctx2, "bravo widget content")
	if ai2 < 0 || (bi2 >= 0 && bi2 < ai2) {
		t.Fatalf("ax1 (no ack, t2 already claimed) must fall back to t1's fact; ctx=%q", ctx2)
	}
}

func TestSubagentStart_ConcurrentSameKeyRendersOnce(t *testing.T) {
	f := newStoreFixture(t)
	f.factHead(t, "/tmp/proj", "dockercache", "docker layer cache", "docker layer cache")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	parent := writeTranscript(t, agentCallLine("t1", "general-purpose", "docker", "dockercache"))
	in := subagentEnv("s1", "a1", "general-purpose", "/tmp/proj", parent, "")

	const n = 10
	outs := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			out, _, _ := runStdin(t, f.env, in, "inject", "--event=SubagentStart")
			outs[i] = out
		}(i)
	}
	wg.Wait()

	nonEmpty := 0
	for _, o := range outs {
		if strings.TrimSpace(o) != "" {
			nonEmpty++
		}
	}
	if nonEmpty != 1 {
		t.Errorf("want exactly 1 non-empty output across %d concurrent starts, got %d", n, nonEmpty)
	}

	wal, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	if !hasRow(string(wal), "inject", "dockercache.md", "s1:a1") {
		t.Errorf("missing inject row under the subagent key:\n%s", wal)
	}
	count := 0
	for _, ln := range strings.Split(string(wal), "\n") {
		if fld := strings.Split(ln, "|"); len(fld) == 4 && fld[1] == "inject" &&
			strings.HasSuffix(fld[2], "dockercache.md") && fld[3] == "s1:a1" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("want exactly 1 inject row for dockercache under s1:a1, got %d:\n%s", count, wal)
	}
}

func TestSubagentStart_MissingIDsAndBadStdin(t *testing.T) {
	f := newStoreFixture(t)
	f.factHead(t, "/tmp/proj", "dockercache", "docker layer cache", "docker layer cache")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	for _, in := range []string{
		subagentEnv("s1", "", "general-purpose", "/tmp/proj", "", ""),
		subagentEnv("", "a1", "general-purpose", "/tmp/proj", "", ""),
		"not json",
	} {
		out, stderr, code := runStdin(t, f.env, in, "inject", "--event=SubagentStart")
		if code != 0 || strings.TrimSpace(out) != "" || stderr != "" {
			t.Errorf("input %q: want silent exit 0; code=%d out=%q stderr=%q", in, code, out, stderr)
		}
	}
}

func assistantCiteLine(text string) string {
	b, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}}}})
	return string(b)
}

func handbackLine(message string) string {
	b, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{
		"content": []map[string]any{{"type": "tool_use", "id": "h1", "name": "SubagentHandback",
			"input": map[string]any{"message": message}}}}})
	return string(b)
}

func TestSubagentStop_ClassifiesUnderSubagentKey(t *testing.T) {
	f := newStoreFixture(t)
	f.factHead(t, "/tmp/proj", "dockercache", "docker layer cache", "docker layer cache")
	f.factHead(t, "/tmp/proj", "pgmigration", "postgres migration lock", "postgres migration lock")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	parent := writeTranscript(t, agentCallLine("t1", "general-purpose", "Fix migration", "pgmigration postgres lock dockercache docker layer"))
	runStdin(t, f.env, subagentEnv("s1", "a1", "general-purpose", "/tmp/proj", parent, ""),
		"inject", "--event=SubagentStart")
	agent := writeTranscript(t, assistantCiteLine(`<cc-memory filenames="pgmigration.md">released the lock first</cc-memory>`))
	out, stderr, code := runStdin(t, f.env, subagentEnv("s1", "a1", "general-purpose", "/tmp/proj", parent, agent),
		"close", "--subagent")
	if code != 0 || strings.TrimSpace(out) != "" || stderr != "" {
		t.Fatalf("close --subagent: code=%d out=%q stderr=%q", code, out, stderr)
	}
	w, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	wal := string(w)
	for _, want := range [][2]string{
		{"cite-useful", "pgmigration.md"},
		{"cite-silent", "dockercache.md"},
		{"session-close", "s1:a1"},
	} {
		if !hasRow(wal, want[0], want[1], "s1:a1") {
			t.Errorf("missing %s row for %s under s1:a1:\n%s", want[0], want[1], wal)
		}
	}
	if strings.Contains(wal, "|s1\n") {
		t.Errorf("no parent-session rows expected:\n%s", wal)
	}
	if exists(filepath.Join(f.mem, ".runtime", "injected-s1.list")) {
		t.Error("parent session list must not exist")
	}
}

func TestSubagentStop_CitationInHandbackReport(t *testing.T) {
	f := newStoreFixture(t)
	f.factHead(t, "/tmp/proj", "pgmigration", "postgres migration lock", "postgres migration lock")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	parent := writeTranscript(t, agentCallLine("t1", "general-purpose", "Fix migration", "pgmigration postgres lock"))
	runStdin(t, f.env, subagentEnv("s1", "a1", "general-purpose", "/tmp/proj", parent, ""),
		"inject", "--event=SubagentStart")
	agent := writeTranscript(t, handbackLine(`Done. <cc-memory filenames="pgmigration.md">released the lock first</cc-memory>`))
	runStdin(t, f.env, subagentEnv("s1", "a1", "general-purpose", "/tmp/proj", parent, agent), "close", "--subagent")
	w, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	if !hasRow(string(w), "cite-useful", "pgmigration.md", "s1:a1") {
		t.Errorf("a citation in the SubagentHandback report must count:\n%s", w)
	}
}

func TestSubagentStop_WildcardSkipIsNoop(t *testing.T) {
	f := newStoreFixture(t)
	f.factHead(t, "/tmp/proj", "dockercache", "docker layer cache", "docker layer cache")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	f.env["HYPOMNEMA_SUBAGENT_SKIP"] = "*"
	agent := writeTranscript(t, assistantCiteLine(`<cc-memory filenames="dockercache.md">x</cc-memory>`))
	out, stderr, code := runStdin(t, f.env, subagentEnv("s1", "a1", "general-purpose", "/tmp/proj", "", agent),
		"close", "--subagent")
	if code != 0 || strings.TrimSpace(out) != "" || stderr != "" {
		t.Errorf("wildcard skip: want silent exit 0; code=%d out=%q stderr=%q", code, out, stderr)
	}
	w, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	if strings.TrimSpace(string(w)) != "" {
		t.Errorf("wildcard skip: close --subagent must not write the WAL:\n%s", w)
	}
}

func TestSubagentStop_NoopCases(t *testing.T) {
	f := newStoreFixture(t)
	f.factHead(t, "/tmp/proj", "dockercache", "docker layer cache", "docker layer cache")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	agent := writeTranscript(t, assistantCiteLine(`<cc-memory filenames="dockercache.md">x</cc-memory>`))
	for _, in := range []string{
		subagentEnv("s1", "e1", "Explore", "/tmp/proj", "", agent),
		subagentEnv("s1", "a1", "general-purpose", "/tmp/proj", "", ""),
		subagentEnv("s1", "", "general-purpose", "/tmp/proj", "", agent),
		"not json",
	} {
		out, stderr, code := runStdin(t, f.env, in, "close", "--subagent")
		if code != 0 || strings.TrimSpace(out) != "" || stderr != "" {
			t.Errorf("input %q: want silent exit 0; code=%d out=%q stderr=%q", in, code, out, stderr)
		}
	}
	w, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	if strings.TrimSpace(string(w)) != "" {
		t.Errorf("no-op cases must not write the WAL:\n%s", w)
	}
}

func TestClose_UnknownFlagStillExitsTwo(t *testing.T) {
	f := newStoreFixture(t)
	if _, _, code := runStdin(t, f.env, "{}", "close", "--bogus"); code != 2 {
		t.Fatalf("unknown flag: exit %d, want 2", code)
	}
}

func TestInject_SubagentFlagActsAsSubagentStart(t *testing.T) {
	f := newStoreFixture(t)
	f.factHead(t, "/tmp/proj", "dockercache", "docker layer cache", "docker layer cache")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	parent := writeTranscript(t, agentCallLine("t1", "general-purpose", "docker", "dockercache"))
	out, _, code := runStdin(t, f.env, subagentEnv("s1", "a1", "general-purpose", "/tmp/proj", parent, ""),
		"inject", "--subagent")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if ev := hookEventName(t, out); ev != "SubagentStart" {
		t.Fatalf("hookEventName = %q, want SubagentStart", ev)
	}
	if !strings.Contains(additionalContext(t, out), "docker layer cache") {
		t.Errorf("want the fact rendered; out=%q", out)
	}
	wal, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	if !hasRow(string(wal), "inject", "dockercache.md", "s1:a1") {
		t.Errorf("missing inject row under the subagent key:\n%s", wal)
	}
	rt := filepath.Join(f.mem, ".runtime")
	if !exists(filepath.Join(rt, "injected-s1_a1.list")) || !exists(filepath.Join(rt, "rendered-s1_a1.list")) {
		t.Errorf("subagent lists missing in %s", rt)
	}
	if exists(filepath.Join(rt, "injected-s1.list")) || exists(filepath.Join(rt, "rendered-s1.list")) {
		t.Errorf("parent session lists must not be written")
	}
}

func TestInject_UnknownFlagStillExitsTwo(t *testing.T) {
	f := newStoreFixture(t)
	if _, _, code := runStdin(t, f.env, "{}", "inject", "--bogus"); code != 2 {
		t.Fatalf("unknown flag: exit %d, want 2", code)
	}
}

// A subagent started in a worktree below the project root: the root's own
// path components are excluded from the gate, the worktree's name is not.
func TestSubagentStart_WorktreeAnchorKeepsTaskTerms(t *testing.T) {
	f := newStoreFixture(t)
	repo := t.TempDir()
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo,
			"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(repo, ".claude", "worktrees", "memboard-tails")
	run("worktree", "add", "-q", wt)
	f.factHead(t, repo, "tails", "memboard tails card lock", "body")
	f.env["CLAUDE_PROJECT_DIR"] = wt
	parent := writeTranscript(t, agentCallLine("t1", "general-purpose", "Fix tails", "Finish the memboard tails work"))
	out, _, code := runStdin(t, f.env, subagentEnv("s1", "a1", "general-purpose", wt, parent, ""),
		"inject", "--event=SubagentStart")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if ctx := additionalContext(t, out); !strings.Contains(ctx, "tails.md") {
		t.Errorf("worktree name terms must keep counting; ctx=%q", ctx)
	}
}
