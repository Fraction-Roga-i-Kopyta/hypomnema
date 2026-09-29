package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

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
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
	f.fact(t, "/tmp/proj", "pgmigration", "postgres migration lock")
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
}

func TestSubagentStart_FallsBackToParentPrompts(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
	f.fact(t, "/tmp/proj", "pgmigration", "postgres migration lock")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	// The only Agent call is answered and not this agent's → the parent's
	// three most recent prompts drive ranking. The meaningful prompt is the
	// third-newest and names the alphabetically LATER fact, so neither a
	// disabled fallback nor n<3 can pass by the ranker's name tie-break.
	parent := writeTranscript(t,
		agentCallLine("t1", "general-purpose", "Fix docker", "dockercache layer"),
		toolResultLine("t1"),
		userPromptLine("now look at the pgmigration lock"),
		userPromptLine("ok"),
		userPromptLine("да"),
	)
	out, _, _ := runStdin(t, f.env, subagentEnv("s1", "a2", "general-purpose", "/tmp/proj", parent, ""),
		"inject", "--event=SubagentStart")
	ctx := additionalContext(t, out)
	p, d := strings.Index(ctx, "postgres migration lock"), strings.Index(ctx, "docker layer cache")
	if p < 0 || (d >= 0 && d < p) {
		t.Fatalf("with no own call the parent's recent prompts must drive ranking; ctx=%q", ctx)
	}
}

func TestSubagentStart_TopFiveFacts(t *testing.T) {
	f := newStoreFixture(t)
	for _, n := range []string{"widget1", "widget2", "widget3", "widget4", "widget5", "widget6", "widget7"} {
		f.fact(t, "/tmp/proj", n, "widget rendering note")
	}
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	parent := writeTranscript(t, agentCallLine("t1", "general-purpose", "widgets", "widget rendering"))
	out, _, _ := runStdin(t, f.env, subagentEnv("s1", "a1", "general-purpose", "/tmp/proj", parent, ""),
		"inject", "--event=SubagentStart")
	if n := strings.Count(additionalContext(t, out), "\n## "); n != 5 {
		t.Errorf("want exactly 5 fact headers for 7 short facts, got %d", n)
	}
}

func TestSubagentStart_ByteBudget(t *testing.T) {
	f := newStoreFixture(t)
	for _, n := range []string{"widget1", "widget2", "widget3", "widget4", "widget5"} {
		f.fact(t, "/tmp/proj", n, "widget rendering note "+strings.Repeat("detail ", 300))
	}
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	parent := writeTranscript(t, agentCallLine("t1", "general-purpose", "widgets", "widget rendering"))
	out, _, _ := runStdin(t, f.env, subagentEnv("s1", "a1", "general-purpose", "/tmp/proj", parent, ""),
		"inject", "--event=SubagentStart")
	if ctx := additionalContext(t, out); ctx == "" || len(ctx) > 5000 {
		t.Errorf("render is %d bytes, want 1..5000", len(ctx))
	}
}

func TestSubagentStart_SkipList(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	parent := writeTranscript(t, userPromptLine("dockercache"))
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
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
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
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
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
	own := writeTranscript(t, agentCallLine("t1", "general-purpose", "Fix migration", "Investigate the pgmigration lock"))
	query, fromTask := subagentQuery(own, "general-purpose", "a1")
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
	_, fromTask2 := subagentQuery(fallback, "general-purpose", "a2")
	if fromTask2 {
		t.Errorf("want fromTask=false when falling back to the parent's recent prompts")
	}
}

func TestSubagentStart_ConcurrentSameKeyRendersOnce(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
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

func TestSubagentStart_ParentHoldoutStaysWithheld(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
	f.fact(t, "/tmp/proj", "pgmigration", "postgres migration lock")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	rt := filepath.Join(f.mem, ".runtime")
	os.MkdirAll(rt, 0o755)
	holdout := filepath.Join(rt, "holdout-s1.list")
	os.WriteFile(holdout, []byte("pgmigration.md\n"), 0o600)
	parent := writeTranscript(t, agentCallLine("t1", "general-purpose", "Fix migration", "pgmigration postgres lock"))
	out, _, _ := runStdin(t, f.env, subagentEnv("s1", "a1", "general-purpose", "/tmp/proj", parent, ""),
		"inject", "--event=SubagentStart")
	if strings.Contains(additionalContext(t, out), "postgres migration lock") {
		t.Errorf("a fact withheld in the parent session must not reach its subagent; out=%q", out)
	}
	wal, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	if strings.Contains(string(wal), "|holdout-skip|") {
		t.Errorf("the subagent must not record holdout observations:\n%s", wal)
	}
	if b, _ := os.ReadFile(holdout); string(b) != "pgmigration.md\n" {
		t.Errorf("the parent's holdout list is read-only; got %q", b)
	}
}

func TestSubagentStart_MissingIDsAndBadStdin(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
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
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
	f.fact(t, "/tmp/proj", "pgmigration", "postgres migration lock")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	parent := writeTranscript(t, agentCallLine("t1", "general-purpose", "Fix migration", "pgmigration postgres lock dockercache"))
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
	if strings.Contains(wal, "|session-metrics|") || strings.Contains(wal, "|s1\n") {
		t.Errorf("no session-metrics and no parent-session rows expected:\n%s", wal)
	}
	if exists(filepath.Join(f.mem, ".runtime", "injected-s1.list")) {
		t.Error("parent session list must not exist")
	}
}

func TestSubagentStop_CitationInHandbackReport(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "pgmigration", "postgres migration lock")
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
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
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
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
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
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
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
