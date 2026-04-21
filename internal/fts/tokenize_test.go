package fts

import "testing"

func TestTokenize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"simple", "hello world", "hello OR world"},
		{"single", "hello", "hello"},
		{"punctuation-split", "foo, bar; baz!", "foo OR bar OR baz"},
		{"cyrillic", "помоги с sed", "помоги OR с OR sed"},
		{"reserved-and-dropped", "foo AND bar", "foo OR bar"},
		{"reserved-or-dropped", "foo OR bar", "foo OR bar"}, // OR dropped, two tokens remain
		{"reserved-not-dropped", "foo NOT bar", "foo OR bar"},
		{"reserved-near-dropped", "foo NEAR bar", "foo OR bar"},
		{"case-preserved-for-content", "FOO bar", "FOO OR bar"},
		{"empty", "", ""},
		{"whitespace-only", "   \t  ", ""},
		{"only-punct", ".,;:", ""},
		{"underscore-kept", "foo_bar baz", "foo_bar OR baz"},
		{"digits", "ERR-42 timeout", "ERR OR 42 OR timeout"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tokenize(tc.in)
			if got != tc.want {
				t.Errorf("tokenize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
