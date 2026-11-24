package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/inject"
)

// Pull-only mode turns the UserPromptSubmit push off entirely — no output,
// no WAL row, no session list — while SessionStart keeps priming the core.
// The same prompt without the switch injects, so the silence is the mode's
// doing and not the fixture's.
func TestInjectVerb_PullOnlyMode(t *testing.T) {
	claudeHome := filepath.Join(t.TempDir(), ".claude")
	memDir := filepath.Join(claudeHome, "memory")
	projDir := filepath.Join(claudeHome, "projects", "-srv-api", "memory")
	for _, d := range []string{memDir, projDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fact := "---\nname: Migration order\ntype: feedback\n---\nrun schema migrations before the deploy\n"
	if err := os.WriteFile(filepath.Join(projDir, "migrations.md"), []byte(fact), 0o644); err != nil {
		t.Fatal(err)
	}
	walPath := filepath.Join(memDir, ".wal")
	resetWAL := func() {
		t.Helper()
		if err := os.WriteFile(walPath, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := func(mode string) map[string]string {
		return map[string]string{
			"CLAUDE_HOME": claudeHome, "CLAUDE_MEMORY_DIR": memDir,
			"HYPOMNEMA_TODAY": "2026-06-01", inject.PromptInjectEnv: mode,
		}
	}
	prompt := func(sid string) string {
		return `{"session_id":"` + sid + `","cwd":"/srv/api","prompt":"order of schema migrations"}`
	}

	resetWAL()
	if out, _, _ := runStdin(t, env(""), prompt("push"), "inject", "--event=UserPromptSubmit"); !strings.Contains(out, "migrations.md") {
		t.Fatalf("control: without the switch the prompt must inject the fact, got %q", out)
	}

	for _, mode := range []string{"off", " OFF "} {
		resetWAL()
		sid := "pull-" + strings.TrimSpace(mode)
		out, _, code := runStdin(t, env(mode), prompt(sid), "inject", "--event=UserPromptSubmit")
		if code != 0 || strings.TrimSpace(out) != "" {
			t.Errorf("mode %q: UserPromptSubmit must exit 0 with no output, got exit %d, %q", mode, code, out)
		}
		if wal, _ := os.ReadFile(walPath); len(wal) != 0 {
			t.Errorf("mode %q: nothing may be written to the WAL, got:\n%s", mode, wal)
		}
		if _, err := os.Stat(filepath.Join(memDir, ".runtime", "injected-"+sid+".list")); err == nil {
			t.Errorf("mode %q: the session list must not be created", mode)
		}
	}

	out, _, _ := runStdin(t, env("off"), `{"session_id":"start","cwd":"/srv/api","prompt":""}`, "inject", "--event=SessionStart")
	if !strings.Contains(out, "migrations.md") {
		t.Errorf("SessionStart must still inject the core in pull-only mode, got %q", out)
	}
}
