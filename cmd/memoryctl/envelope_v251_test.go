package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// H1: the additionalContext budget counts markdown bytes, but Claude Code
// diverts on the JSON ENVELOPE size. Default json.Marshal escapes <,>,& to
// \uXXXX (6 bytes each), so a code/markup-heavy fact at the 8KB markdown
// budget produces a far larger envelope that gets diverted — the exact thing
// the budget exists to prevent. marshalEnvelope must not blow up on <>&.
func TestMarshalEnvelope_NoHTMLEscapeBlowup(t *testing.T) {
	md := "# Memory Context\n\n## fact\n" +
		strings.Repeat("shell 2>&1 && grep <pat> | a<b && b<c  ", 100)
	b, err := marshalEnvelope("SessionStart", md)
	if err != nil {
		t.Fatal(err)
	}
	ratio := float64(len(b)) / float64(len(md))
	if ratio > 1.15 {
		t.Errorf("envelope is %.2fx the markdown (%d vs %d) — HTML-escaping blows the budget", ratio, len(b), len(md))
	}
	// Must remain valid JSON round-tripping to the same markdown.
	var out struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("envelope not valid JSON: %v", err)
	}
	if out.HookSpecificOutput.AdditionalContext != md {
		t.Error("round-trip changed the markdown")
	}
}
