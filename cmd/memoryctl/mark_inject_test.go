package main

import (
	"strings"
	"testing"
)

// mark → inject with no close in between: the hide takes effect at once.
func TestInject_HideEffectiveBeforeClose(t *testing.T) {
	env, _, _, _ := retireFixture(t)
	stdin := `{"session_id":"s1","cwd":"` + env["CLAUDE_PROJECT_CWD"] + `","prompt":"docker old-rule"}`
	out, _, code := runStdin(t, env, stdin, "inject", "--event=UserPromptSubmit")
	if code != 0 || !strings.Contains(out, "old-rule") {
		t.Fatalf("baseline inject must include old-rule: code=%d out=%s", code, out)
	}
	if _, e, c := run(t, env, "mark", "old-rule", "hide"); c != 0 {
		t.Fatal(e)
	}
	stdin = strings.Replace(stdin, "s1", "s2", 1)
	out, _, code = runStdin(t, env, stdin, "inject", "--event=UserPromptSubmit")
	if code != 0 || strings.Contains(out, "old-rule") {
		t.Fatalf("hidden fact injected: code=%d out=%s", code, out)
	}
}
