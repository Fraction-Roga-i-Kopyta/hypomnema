package wal

import "testing"

func TestSanitizeField(t *testing.T) {
	cases := map[string]string{
		"clean-slug": "clean-slug",
		"ev|il":      "ev_il",
		"a\nb":       "a_b",
		"x\r\ny|z":   "x__y_z",
	}
	for in, want := range cases {
		if got := SanitizeField(in); got != want {
			t.Errorf("SanitizeField(%q) = %q, want %q", in, got, want)
		}
	}
}
