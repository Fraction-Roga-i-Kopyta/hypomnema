package tokenize

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTokenize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"ascii lowercased", "Hello World", []string{"hello", "world"}},
		{"punctuation splits", "foo,bar.baz", []string{"foo", "bar", "baz"}},
		{"min rune filter drops short", "a ab abc abcd", []string{"abc", "abcd"}},
		{"cyrillic kept (rune length, not bytes)", "база данных", []string{"база", "данных"}},
		{"underscore is part of token", "snake_case", []string{"snake_case"}},
		{"digits kept", "go125 ok", []string{"go125"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Tokenize(tc.in, nil)
			if len(got) != len(tc.want) {
				t.Fatalf("Tokenize(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("Tokenize(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestTokenizeStopwords(t *testing.T) {
	sw := map[string]struct{}{"the": {}, "and": {}}
	got := Tokenize("the quick and brown", sw)
	want := []string{"quick", "brown"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("with stopwords = %v, want %v", got, want)
	}
}

func TestLoadStopwords(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sw")
	if err := os.WriteFile(p, []byte("The\n\nAnd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sw, err := LoadStopwords(p)
	if err != nil {
		t.Fatalf("LoadStopwords: %v", err)
	}
	if _, ok := sw["the"]; !ok {
		t.Error("expected lowercased 'the' in stopwords")
	}
	if _, ok := sw["and"]; !ok {
		t.Error("expected 'and' in stopwords")
	}
	sw2, err := LoadStopwords(filepath.Join(dir, "nope"))
	if err != nil || len(sw2) != 0 {
		t.Errorf("missing stopword file: got len=%d err=%v, want 0/nil", len(sw2), err)
	}
}
