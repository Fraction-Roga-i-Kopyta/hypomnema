package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

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

func TestFailSafe_CloseRecovers(t *testing.T) {
	var stderr bytes.Buffer
	panicked := failSafeTrace("close", &stderr, func() { panic("boom") })
	if !panicked {
		t.Fatal("panic must be reported")
	}
	if got, want := stderr.String(), "memoryctl close: recovered panic: boom\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

// Every verb the shims call must be registered (dispatch reads this map).
func TestHookVerbs_AllWrapped(t *testing.T) {
	for _, v := range []string{"inject", "close", "guard", "guard-bash", "skill-inject", "skill-active"} {
		if hookVerbs[v] == nil {
			t.Errorf("hook verb %q is not in hookVerbs (unwrapped)", v)
		}
	}
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	for v := range hookVerbs {
		if strings.Contains(string(src), "case \""+v+"\":") {
			t.Errorf("main.go dispatches %q outside hookVerbs", v)
		}
	}
}
