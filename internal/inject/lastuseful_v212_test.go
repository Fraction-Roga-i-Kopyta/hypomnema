package inject

import (
	"os"
	"path/filepath"
	"testing"
)

// The recency basis is wired through the sidecar → inject candidate path:
// a fact useful yesterday outranks an equal fact that was merely injected.
func TestRun_OrdersByLastUseful(t *testing.T) {
	home := t.TempDir()
	claudeHome := filepath.Join(home, ".claude")
	memDir := filepath.Join(claudeHome, "memory")
	projDir := filepath.Join(claudeHome, "projects", "-tmp-proj", "memory")
	for _, d := range []string{filepath.Join(memDir, ".runtime"), projDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Identical bodies/keywords → identical overlap; identical created.
	for _, n := range []string{"a-fact.md", "b-fact.md"} {
		os.WriteFile(filepath.Join(projDir, n),
			[]byte("---\nname: "+n+"\ntype: knowledge\ncreated: 2026-01-01\nkeywords: [alpha]\n---\nalpha body\n"), 0o644)
	}
	// Everything equal except last_useful: both ref 2, both eff 0.667
	// (one useful session each), both last_injected 2026-08-15; a was
	// useful in March, b yesterday. Unwired → identical scores → slug order
	// (a first); wired → b first.
	wal := "2026-03-01|inject|-tmp-proj\x1fa-fact.md|s0\n" +
		"2026-03-01|trigger-useful|-tmp-proj\x1fa-fact.md|s0\n" +
		"2026-08-15|inject|-tmp-proj\x1fa-fact.md|s1\n" +
		"2026-03-01|inject|-tmp-proj\x1fb-fact.md|s0\n" +
		"2026-08-15|inject|-tmp-proj\x1fb-fact.md|s1\n" +
		"2026-08-15|trigger-useful|-tmp-proj\x1fb-fact.md|s1\n"
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(wal), 0o644)

	res, err := Run(Input{SessionID: "s2", CWD: "/tmp/proj", Prompt: "alpha",
		ClaudeHome: claudeHome, MemoryDir: memDir, Today: "2026-08-16"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Injected) < 2 || res.Injected[0] != "b-fact.md" {
		t.Fatalf("useful-yesterday fact must inject first, got %v", res.Injected)
	}
}
