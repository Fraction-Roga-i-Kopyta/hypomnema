package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

const globalIndexCheck = "global_index"

func writeGlobalFact(t *testing.T, claude, slug string) {
	t.Helper()
	body := "---\nname: " + strings.TrimSuffix(slug, ".md") + "\ndescription: d\ntype: knowledge\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(claude, "memory-global", slug), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGlobalIndex(t *testing.T, claude, mem, cwd string, want Status) Check {
	t.Helper()
	return mustFindCheck(t, Run(claude, mem, native.StoreFor(claude, cwd), cwd), globalIndexCheck, want)
}

// A hand-written MEMORY.md in the global store is left untouched by the Stop
// hook forever — the generated index (and every fact it would list) never
// appears. doctor must say so and how to fix it.
func TestGlobalIndex_ForeignIndexWarns(t *testing.T) {
	t.Setenv("HYPOMNEMA_GLOBAL_DIR", "")
	claude, mem, cwd := newFixture(t)
	writeGlobalFact(t, claude, "a.md")
	os.WriteFile(filepath.Join(claude, "memory-global", "MEMORY.md"), []byte("- [a](a.md) — mine\n"), 0o644)
	c := runGlobalIndex(t, claude, mem, cwd, WARN)
	if !strings.Contains(c.Detail, "reindex") {
		t.Errorf("detail should name the fix (move aside + reindex), got %q", c.Detail)
	}
}

// Pull-only mode turns prompt injection off; without the global index
// imported from the user CLAUDE.md the global store is invisible.
func TestGlobalIndex_PullOnlyWithoutImportWarns(t *testing.T) {
	t.Setenv("HYPOMNEMA_GLOBAL_DIR", "")
	t.Setenv("HYPOMNEMA_PROMPT_INJECT", "off")
	claude, mem, cwd := newFixture(t)
	writeGlobalFact(t, claude, "a.md")
	os.WriteFile(filepath.Join(claude, "memory-global", "MEMORY.md"), []byte("# Global Memory Index\n- [a](a.md) — d\n"), 0o644)
	os.WriteFile(filepath.Join(claude, "CLAUDE.md"), []byte("# prefs\n"), 0o644)
	c := runGlobalIndex(t, claude, mem, cwd, WARN)
	if !strings.Contains(c.Detail, "@~/.claude/memory-global/MEMORY.md") {
		t.Errorf("detail should give the import line, got %q", c.Detail)
	}
}

// settings.json env counts too: doctor usually runs from a plain shell that
// does not carry Claude Code's env block.
func TestGlobalIndex_PullOnlyFromSettingsEnv(t *testing.T) {
	t.Setenv("HYPOMNEMA_GLOBAL_DIR", "")
	t.Setenv("HYPOMNEMA_PROMPT_INJECT", "")
	claude, mem, cwd := newFixture(t)
	writeGlobalFact(t, claude, "a.md")
	os.WriteFile(filepath.Join(claude, "memory-global", "MEMORY.md"), []byte("# Global Memory Index\n- [a](a.md) — d\n"), 0o644)
	s := strings.Replace(validSettingsJSON(nil), "{", `{"env":{"HYPOMNEMA_PROMPT_INJECT":"off"},`, 1)
	os.WriteFile(filepath.Join(claude, "settings.json"), []byte(s), 0o644)
	runGlobalIndex(t, claude, mem, cwd, WARN)
}

func TestGlobalIndex_PullOnlyWithImportOK(t *testing.T) {
	t.Setenv("HYPOMNEMA_GLOBAL_DIR", "")
	t.Setenv("HYPOMNEMA_PROMPT_INJECT", "off")
	claude, mem, cwd := newFixture(t)
	writeGlobalFact(t, claude, "a.md")
	os.WriteFile(filepath.Join(claude, "memory-global", "MEMORY.md"), []byte("# Global Memory Index\n- [a](a.md) — d\n"), 0o644)
	os.WriteFile(filepath.Join(claude, "CLAUDE.md"), []byte("# prefs\n@~/.claude/memory-global/MEMORY.md\n"), 0o644)
	runGlobalIndex(t, claude, mem, cwd, OK)
}

func TestGlobalIndex_EmptyStoreOK(t *testing.T) {
	t.Setenv("HYPOMNEMA_GLOBAL_DIR", "")
	t.Setenv("HYPOMNEMA_PROMPT_INJECT", "")
	claude, mem, cwd := newFixture(t)
	runGlobalIndex(t, claude, mem, cwd, OK)
}
