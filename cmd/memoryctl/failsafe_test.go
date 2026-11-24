package main

import "testing"

// A panic on the prompt path must not escape: Go exits 2 on an unrecovered
// panic, and exit 2 on UserPromptSubmit blocks the user's prompt (I5).
func TestFailSafe_RecoversPanic(t *testing.T) {
	ran := false
	panicked := failSafe(func() {
		ran = true
		var s []int
		_ = s[len(s)] // index out of range: a real runtime panic, not just panic("x")
	})
	if !ran || !panicked {
		t.Fatalf("failSafe must run f and report its panic; ran=%v panicked=%v", ran, panicked)
	}
}

func TestFailSafe_NoPanic(t *testing.T) {
	ran := false
	if failSafe(func() { ran = true }) || !ran {
		t.Fatalf("a clean run reports no panic; ran=%v", ran)
	}
}
