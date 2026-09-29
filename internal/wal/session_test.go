package wal

import "testing"

func TestParentSession(t *testing.T) {
	for in, want := range map[string]string{"s1:a1": "s1", "s1": "s1", "": "", "s1:a1:b2": "s1"} {
		if got := ParentSession(in); got != want {
			t.Errorf("ParentSession(%q) = %q, want %q", in, got, want)
		}
	}
}
