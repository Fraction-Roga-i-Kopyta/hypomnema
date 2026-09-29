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

func TestAgentCalls_MatchesTypeAndPending(t *testing.T) {
	p := writeLines(t,
		agentCall("Agent", "t1", "general-purpose", "old", "answered already"),
		toolResult("t1"),
		agentCall("Agent", "t2", "reviewer", "review", "check the diff"),
		agentCall("Agent", "t3", "general-purpose", "first pending", "first"),
		agentCall("Agent", "t4", "general-purpose", "fix migration", "pgmigration lock"),
	)
	pending, own, readable := AgentCalls(p, "general-purpose", "")
	if !readable || own != nil {
		t.Fatalf("got pending=%+v own=%v readable=%v, want readable with no own call", pending, own, readable)
	}
	if len(pending) != 2 || pending[0].Description != "first pending" || pending[0].Prompt != "first" ||
		pending[1].Description != "fix migration" || pending[1].Prompt != "pgmigration lock" {
		t.Fatalf("got %+v, want the two pending general-purpose calls oldest-first (t3, t4)", pending)
	}
	rPending, _, ok := AgentCalls(p, "reviewer", "")
	if !ok || len(rPending) != 1 || rPending[0].Description != "review" {
		t.Fatalf("reviewer: got %+v ok=%v", rPending, ok)
	}
	ePending, eOwn, ok := AgentCalls(p, "Explore", "")
	if !ok || len(ePending) != 0 || eOwn != nil {
		t.Fatalf("no Explore call exists; want readable=true with nothing found, got pending=%+v own=%v", ePending, eOwn)
	}
}

func TestAgentCalls_OwnAckWinsAndIsExcludedFromPending(t *testing.T) {
	// Two background launches of the same type; t1's acknowledgement names
	// agent a0000000000000001 and has already landed.
	p := writeLines(t,
		agentCall("Agent", "t1", "general-purpose", "task one", "one"),
		agentCall("Agent", "t2", "general-purpose", "task two", "two"),
		launchAck("t1", "a0000000000000001"),
	)
	pending, own, ok := AgentCalls(p, "general-purpose", "a0000000000000001")
	if !ok || own == nil || own.Description != "task one" {
		t.Fatalf("got own=%v ok=%v, want this agent's own call", own, ok)
	}
	if len(pending) != 1 || pending[0].Description != "task two" {
		t.Fatalf("got pending=%+v, want only the sibling's still-unanswered call", pending)
	}
}

func TestAgentCalls_EmptyTypeIsGeneralPurposeAndTaskName(t *testing.T) {
	p := writeLines(t, agentCall("Task", "t1", "", "legacy", "legacy prompt"))
	pending, _, ok := AgentCalls(p, "general-purpose", "")
	if !ok || len(pending) != 1 || pending[0].Description != "legacy" {
		t.Fatalf("got %+v ok=%v, want the Task call with no subagent_type", pending, ok)
	}
}

func TestAgentCalls_AnsweredCallIsNotPending(t *testing.T) {
	p := writeLines(t, agentCall("Agent", "t1", "general-purpose", "d", "p"), toolResult("t1"))
	pending, own, ok := AgentCalls(p, "general-purpose", "someone-else")
	if !ok || len(pending) != 0 || own != nil {
		t.Fatalf("an answered call that is not this agent's must not be pending or own; got pending=%+v own=%v", pending, own)
	}
}

func TestAgentCalls_PendingOrderOldestFirst(t *testing.T) {
	p := writeLines(t,
		agentCall("Agent", "t1", "general-purpose", "first", "f1"),
		agentCall("Agent", "t2", "general-purpose", "second", "f2"),
		agentCall("Agent", "t3", "general-purpose", "third", "f3"),
	)
	pending, _, ok := AgentCalls(p, "general-purpose", "")
	if !ok || len(pending) != 3 {
		t.Fatalf("want 3 pending calls, got %+v", pending)
	}
	for i, want := range []string{"first", "second", "third"} {
		if pending[i].Description != want {
			t.Fatalf("pending[%d] = %q, want %q (oldest-first): %+v", i, pending[i].Description, want, pending)
		}
	}
}

func TestAgentCalls_OutsideTailWindowIsNotSeen(t *testing.T) {
	filler := userString(strings.Repeat("x", 1000))
	lines := []string{agentCall("Agent", "t1", "general-purpose", "too old", "p")}
	for i := 0; i < (agentTailBytes/1000)+100; i++ {
		lines = append(lines, filler)
	}
	p := writeLines(t, lines...)
	pending, _, ok := AgentCalls(p, "general-purpose", "")
	if !ok || len(pending) != 0 {
		t.Fatalf("a call before the tail window must not be found; got %+v", pending)
	}
	// A call at the end of the same big file is found; the partial first
	// line of the window does not break parsing.
	lines = append(lines, agentCall("Agent", "t2", "general-purpose", "fresh", "p"))
	p = writeLines(t, lines...)
	pending, _, ok = AgentCalls(p, "general-purpose", "")
	if !ok || len(pending) != 1 || pending[0].Description != "fresh" {
		t.Fatalf("got %+v ok=%v, want the call at the end of a large file", pending, ok)
	}
}

func TestAgentCalls_WindowStartingOnLineBoundaryKeepsFirstLine(t *testing.T) {
	call := agentCall("Agent", "t1", "general-purpose", "boundary", "p")
	empty := userString("")
	pad := userString(strings.Repeat("y", agentTailBytes-len(call)-1-len(empty)-1))
	p := writeLines(t, userString(strings.Repeat("x", 5000)), call, pad)
	pending, _, ok := AgentCalls(p, "general-purpose", "")
	if !ok || len(pending) != 1 || pending[0].Description != "boundary" {
		t.Fatalf("got %+v ok=%v, want the call that starts exactly at the window edge", pending, ok)
	}
}

func TestAgentCalls_UnreadablePath(t *testing.T) {
	if _, _, ok := AgentCalls("", "general-purpose", ""); ok {
		t.Fatal("empty path must be readable=false")
	}
	if _, _, ok := AgentCalls(filepath.Join(t.TempDir(), "missing.jsonl"), "general-purpose", ""); ok {
		t.Fatal("missing file must be readable=false")
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
