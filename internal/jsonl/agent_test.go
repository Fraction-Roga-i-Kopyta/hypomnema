package jsonl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLines(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "parent.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func agentCall(name, id, subagentType, description, prompt string) string {
	in := map[string]any{"description": description, "prompt": prompt}
	if subagentType != "" {
		in["subagent_type"] = subagentType
	}
	b, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{
		"content": []map[string]any{{"type": "tool_use", "id": id, "name": name, "input": in}}}})
	return string(b)
}

func toolResult(id string) string {
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{
		"content": []map[string]any{{"type": "tool_result", "tool_use_id": id, "content": "done"}}}})
	return string(b)
}

// launchAck is the tool_result a background Agent launch gets at once.
func launchAck(id, agent string) string {
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{
		"content": []map[string]any{{"type": "tool_result", "tool_use_id": id,
			"content": []map[string]any{{"type": "text", "text": "Async agent launched successfully.\nagentId: " + agent + " (internal ID)"}}}}}})
	return string(b)
}

func userString(text string) string {
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"content": text}})
	return string(b)
}

func TestPendingAgentCall_MatchesTypeAndPending(t *testing.T) {
	p := writeLines(t,
		agentCall("Agent", "t1", "general-purpose", "old", "answered already"),
		toolResult("t1"),
		agentCall("Agent", "t2", "reviewer", "review", "check the diff"),
		agentCall("Agent", "t3", "general-purpose", "first pending", "first"),
		agentCall("Agent", "t4", "general-purpose", "fix migration", "pgmigration lock"),
	)
	desc, prompt, ok := PendingAgentCall(p, "general-purpose", "")
	if !ok || desc != "fix migration" || prompt != "pgmigration lock" {
		t.Fatalf("got (%q, %q, %v), want the newest pending general-purpose call", desc, prompt, ok)
	}
	if desc, _, ok := PendingAgentCall(p, "reviewer", ""); !ok || desc != "review" {
		t.Fatalf("reviewer: got (%q, %v)", desc, ok)
	}
	if _, _, ok := PendingAgentCall(p, "Explore", ""); ok {
		t.Fatal("no Explore call exists; want ok=false")
	}
}

func TestPendingAgentCall_OwnLaunchAckWinsAmongSameType(t *testing.T) {
	// Two background launches of the same type; t1's acknowledgement names
	// agent a0000000000000001 and has already landed.
	p := writeLines(t,
		agentCall("Agent", "t1", "general-purpose", "task one", "one"),
		agentCall("Agent", "t2", "general-purpose", "task two", "two"),
		launchAck("t1", "a0000000000000001"),
	)
	if desc, _, ok := PendingAgentCall(p, "general-purpose", "a0000000000000001"); !ok || desc != "task one" {
		t.Fatalf("got (%q, %v), want this agent's own call", desc, ok)
	}
}

func TestPendingAgentCall_EmptyTypeIsGeneralPurposeAndTaskName(t *testing.T) {
	p := writeLines(t, agentCall("Task", "t1", "", "legacy", "legacy prompt"))
	if desc, _, ok := PendingAgentCall(p, "general-purpose", ""); !ok || desc != "legacy" {
		t.Fatalf("got (%q, %v), want the Task call with no subagent_type", desc, ok)
	}
}

func TestPendingAgentCall_AllAnsweredIsNotFound(t *testing.T) {
	p := writeLines(t, agentCall("Agent", "t1", "general-purpose", "d", "p"), toolResult("t1"))
	if _, _, ok := PendingAgentCall(p, "general-purpose", "someone-else"); ok {
		t.Fatal("an answered call that is not this agent's must not be returned")
	}
}

func TestPendingAgentCall_OutsideTailWindowIsNotSeen(t *testing.T) {
	filler := userString(strings.Repeat("x", 1000))
	lines := []string{agentCall("Agent", "t1", "general-purpose", "too old", "p")}
	for i := 0; i < (agentTailBytes/1000)+100; i++ {
		lines = append(lines, filler)
	}
	p := writeLines(t, lines...)
	if _, _, ok := PendingAgentCall(p, "general-purpose", ""); ok {
		t.Fatal("a call before the tail window must not be found")
	}
	// A call at the end of the same big file is found; the partial first
	// line of the window does not break parsing.
	lines = append(lines, agentCall("Agent", "t2", "general-purpose", "fresh", "p"))
	p = writeLines(t, lines...)
	if desc, _, ok := PendingAgentCall(p, "general-purpose", ""); !ok || desc != "fresh" {
		t.Fatalf("got (%q, %v), want the call at the end of a large file", desc, ok)
	}
}

func TestPendingAgentCall_WindowStartingOnLineBoundaryKeepsFirstLine(t *testing.T) {
	call := agentCall("Agent", "t1", "general-purpose", "boundary", "p")
	empty := userString("")
	pad := userString(strings.Repeat("y", agentTailBytes-len(call)-1-len(empty)-1))
	p := writeLines(t, userString(strings.Repeat("x", 5000)), call, pad)
	if desc, _, ok := PendingAgentCall(p, "general-purpose", ""); !ok || desc != "boundary" {
		t.Fatalf("got (%q, %v), want the call that starts exactly at the window edge", desc, ok)
	}
}

func TestPendingAgentCall_UnreadablePath(t *testing.T) {
	if _, _, ok := PendingAgentCall("", "general-purpose", ""); ok {
		t.Fatal("empty path must be ok=false")
	}
	if _, _, ok := PendingAgentCall(filepath.Join(t.TempDir(), "missing.jsonl"), "general-purpose", ""); ok {
		t.Fatal("missing file must be ok=false")
	}
}

func TestRecentUserPrompts_SkipsMachineText(t *testing.T) {
	parts := func(texts ...string) string {
		var c []map[string]any
		for _, s := range texts {
			c = append(c, map[string]any{"type": "text", "text": s})
		}
		b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"content": c}})
		return string(b)
	}
	meta, _ := json.Marshal(map[string]any{"type": "user", "isMeta": true, "message": map[string]any{"content": "meta text"}})
	summary, _ := json.Marshal(map[string]any{"type": "user", "isCompactSummary": true, "message": map[string]any{"content": "summary text"}})
	p := writeLines(t,
		userString("oldest prompt about queues"),
		userString("fix the dockercache layer"),
		parts("<system-reminder>ignore me</system-reminder>", "and the pgmigration lock"),
		toolResult("t9"),
		userString("<task-notification>done</task-notification>"),
		string(meta),
		string(summary),
	)
	got, ok := RecentUserPrompts(p, 2)
	if !ok || got != "fix the dockercache layer\nand the pgmigration lock" {
		t.Fatalf("got (%q, %v), want the two newest human prompts, oldest first", got, ok)
	}
	if got, _ := RecentUserPrompts(p, 3); !strings.HasPrefix(got, "oldest prompt about queues\n") {
		t.Fatalf("n=3 must include the third-newest prompt; got %q", got)
	}
}

func TestRecentUserPrompts_NoneFound(t *testing.T) {
	p := writeLines(t, toolResult("t1"), userString("<command-name>/clear</command-name>"))
	if _, ok := RecentUserPrompts(p, 3); ok {
		t.Fatal("want ok=false when only machine text exists")
	}
}
