package jsonl

import (
	"fmt"
	"strings"
	"testing"
)

func TestDecodeStream_ExtractsAssistantTextOnly(t *testing.T) {
	src := `{"type":"permission-mode","sessionId":"sess-123"}
{"type":"user","message":{"content":[{"type":"text","text":"user prompt"}]}}
{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"internal"},{"type":"text","text":"First reply"}]}}
{"type":"attachment","attachment":{"type":"hook_success"}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Second reply"}]}}
`
	s, err := decodeStream(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "sess-123" {
		t.Errorf("ID: got %q, want sess-123", s.ID)
	}
	// Thinking must NOT appear (internal reasoning).
	if strings.Contains(s.Text, "internal") {
		t.Errorf("thinking content leaked into text: %q", s.Text)
	}
	// User content must NOT appear (we mine assistant-authored only).
	if strings.Contains(s.Text, "user prompt") {
		t.Errorf("user prompt leaked into assistant text: %q", s.Text)
	}
	// Both assistant replies must be present, separated by \n\n.
	if !strings.Contains(s.Text, "First reply") || !strings.Contains(s.Text, "Second reply") {
		t.Errorf("assistant text missing replies: %q", s.Text)
	}
	if !strings.Contains(s.Text, "First reply\n\nSecond reply") {
		t.Errorf("replies should join with \\n\\n, got %q", s.Text)
	}
}

func TestDecodeStream_MalformedLinesSkipped(t *testing.T) {
	src := `{not json
{"type":"assistant","message":{"content":[{"type":"text","text":"good"}]}}
another garbage line
`
	s, err := decodeStream(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if s.Text != "good" {
		t.Errorf("expected 'good', got %q", s.Text)
	}
}

func TestDecodeStream_EmptyInputIsOK(t *testing.T) {
	s, err := decodeStream(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "" || s.Text != "" {
		t.Errorf("expected empty Session, got %+v", s)
	}
}

func TestDecodeStream_NoAssistantRecords(t *testing.T) {
	src := `{"type":"user","sessionId":"s1","message":{"content":[{"type":"text","text":"hi"}]}}
{"type":"attachment"}
`
	s, err := decodeStream(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "s1" {
		t.Errorf("ID should still be captured from record 0, got %q", s.ID)
	}
	if s.Text != "" {
		t.Errorf("Text should be empty when no assistant records, got %q", s.Text)
	}
}

func TestDecodeStream_SessionMetrics(t *testing.T) {
	lines := `{"type":"user","sessionId":"s1","timestamp":"2026-06-09T10:00:00Z","message":{"content":[{"type":"text","text":"go"}]}}
{"type":"assistant","timestamp":"2026-06-09T10:00:30Z","message":{"content":[{"type":"text","text":"running"},{"type":"tool_use"},{"type":"tool_use"}]}}
{"type":"user","timestamp":"2026-06-09T10:01:00Z","message":{"content":[{"type":"tool_result","is_error":true}]}}
{"type":"assistant","timestamp":"2026-06-09T10:02:00Z","message":{"content":[{"type":"tool_use"}]}}`
	s, err := decodeStream(strings.NewReader(lines))
	if err != nil {
		t.Fatal(err)
	}
	if s.ToolCalls != 3 {
		t.Errorf("ToolCalls = %d, want 3", s.ToolCalls)
	}
	if s.ToolErrors != 1 {
		t.Errorf("ToolErrors = %d, want 1", s.ToolErrors)
	}
	if s.DurationSec != 120 {
		t.Errorf("DurationSec = %d, want 120 (10:00:00 → 10:02:00)", s.DurationSec)
	}
}

func TestDecodeStream_OversizeLineDoesNotTruncateRest(t *testing.T) { // review E3
	// A giant tool_result line (>4MB) must not stop the scan: assistant text
	// AFTER it would otherwise be lost, fabricating "silent" for cited facts.
	huge := strings.Repeat("x", 5*1024*1024)
	src := `{"type":"assistant","message":{"content":[{"type":"text","text":"before"}]}}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"tool_result","text":"` + huge + `"}]}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"after-CITATION"}]}}` + "\n"
	s, err := decodeStream(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Text, "after-CITATION") {
		t.Errorf("text after an oversized line was lost: %q", s.Text)
	}
	if !strings.Contains(s.Text, "before") {
		t.Errorf("text before the oversized line missing: %q", s.Text)
	}
}

func TestDecodeStream_ReadPathsCollected(t *testing.T) {
	src := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/proj/memory/docker.md"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/proj/memory/docker.md"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/proj/memory/sql.md"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":"not-an-object"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":123}}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"still here"}]}}
`
	s, err := decodeStream(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/proj/memory/docker.md", "/proj/memory/sql.md"}
	if len(s.ReadPaths) != len(want) {
		t.Fatalf("ReadPaths = %v, want %v", s.ReadPaths, want)
	}
	for i, w := range want {
		if s.ReadPaths[i] != w {
			t.Errorf("ReadPaths[%d] = %q, want %q", i, s.ReadPaths[i], w)
		}
	}
	// A malformed/odd `input` on a Read tool_use must not drop the rest of
	// the session's content — the plain text line after it still parses.
	if s.Text != "still here" {
		t.Errorf("text after odd tool_use input was lost: %q", s.Text)
	}
}

func TestDecodeStream_ReadPathsCappedAt1024(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 1100; i++ {
		fmt.Fprintf(&b, `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/proj/memory/f%d.md"}}]}}`+"\n", i)
	}
	s, err := decodeStream(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ReadPaths) != 1024 {
		t.Errorf("ReadPaths length = %d, want capped at 1024", len(s.ReadPaths))
	}
}

func TestDecodeStream_ThinkingCollectedSeparately(t *testing.T) {
	src := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"plan <cc-memory filenames=\"a.md\">x</cc-memory>"},{"type":"text","text":"visible"}]}}
{"type":"assistant","message":{"content":[{"type":"thinking","thinking":""},{"type":"thinking","thinking":"second"}]}}
`
	s, err := decodeStream(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if s.Text != "visible" {
		t.Errorf("Text must stay text-only, got %q", s.Text)
	}
	if !strings.Contains(s.Thinking, `filenames="a.md"`) || !strings.Contains(s.Thinking, "second") {
		t.Errorf("Thinking not collected: %q", s.Thinking)
	}
}
