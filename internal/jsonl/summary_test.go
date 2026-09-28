package jsonl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLastCompactSummary(t *testing.T) {
	cases := []struct {
		name   string
		lines  []string
		want   string
		wantOK bool
	}{
		{"string content", []string{
			`{"type":"system","subtype":"compact_boundary"}`,
			`{"type":"user","isCompactSummary":true,"message":{"role":"user","content":"Summary: postgres migration"}}`,
		}, "Summary: postgres migration", true},
		{"array content", []string{
			`{"type":"user","isCompactSummary":true,"message":{"content":[{"type":"text","text":"array summary"}]}}`,
		}, "array summary", true},
		{"latest wins", []string{
			`{"type":"user","isCompactSummary":true,"message":{"content":"first"}}`,
			`{"type":"assistant","message":{"content":[{"type":"text","text":"work"}]}}`,
			`{"type":"user","isCompactSummary":true,"message":{"content":"second"}}`,
		}, "second", true},
		{"none", []string{
			`{"type":"user","message":{"content":"plain prompt"}}`,
		}, "", false},
		{"garbage tolerated", []string{
			`{not json`,
			`{"type":"user","isCompactSummary":true,"message":{"content":"ok"}}`,
		}, "ok", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := LastCompactSummary(writeTranscript(t, tc.lines...))
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("got (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
	if _, ok := LastCompactSummary(filepath.Join(t.TempDir(), "missing.jsonl")); ok {
		t.Error("missing file must return ok=false")
	}
	if _, ok := LastCompactSummary(""); ok {
		t.Error("empty path must return ok=false")
	}
}
