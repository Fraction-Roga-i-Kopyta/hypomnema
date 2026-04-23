package jsonl

import (
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
