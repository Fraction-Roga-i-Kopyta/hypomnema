package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// With no project memory at all, reindex still builds the global store's
// index — absolute links, header WriteGlobal recognises — and reports the
// write on stderr.
func TestReindex_GlobalIndexWithoutProjectStore(t *testing.T) {
	claudeHome := filepath.Join(t.TempDir(), ".claude")
	globalDir := filepath.Join(claudeHome, "memory-global")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fact := "---\nname: ssh-hosts\ndescription: Jump-host aliases for every environment\ntype: knowledge\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(globalDir, "ssh-hosts.md"), []byte(fact), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := run(t, map[string]string{
		"CLAUDE_HOME":        claudeHome,
		"CLAUDE_PROJECT_CWD": "/srv/no-memory-here",
	}, "reindex")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	idx := filepath.Join(globalDir, "MEMORY.md")
	got, err := os.ReadFile(idx)
	if err != nil {
		t.Fatalf("global index missing: %v", err)
	}
	if !strings.HasPrefix(string(got), "# Global Memory Index\n") {
		t.Errorf("index must open with the generated header:\n%s", got)
	}
	if want := "- [ssh-hosts](" + filepath.Join(globalDir, "ssh-hosts.md") + ") — Jump-host aliases"; !strings.Contains(string(got), want) {
		t.Errorf("missing absolute-link entry %q in:\n%s", want, got)
	}
	if !strings.Contains(stderr, "wrote "+idx+" (1 files,") {
		t.Errorf("stderr must report the global write, got: %s", stderr)
	}
}
