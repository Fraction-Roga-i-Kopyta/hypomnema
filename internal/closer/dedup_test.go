package closer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A subagent's classification rows (session s1:a1) land before its parent's
// (s1) — SubagentStop fires inside the parent's turn — and must not suppress
// the parent's same-verdict rows for the same facts.
func TestRun_SubagentRowsDoNotSuppressParentRows(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	os.MkdirAll(filepath.Join(memDir, ".runtime"), 0o755)
	os.MkdirAll(projDir, 0o755)
	os.WriteFile(filepath.Join(projDir, "docker.md"), []byte("---\nname: Docker\ntype: mistake\n---\ndocker\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "sql.md"), []byte("---\nname: SQL\ntype: knowledge\n---\nsql\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), nil, 0o644)
	for _, k := range []string{"s1", "s1_a1"} {
		os.WriteFile(filepath.Join(memDir, ".runtime", "injected-"+k+".list"), []byte("docker.md\nsql.md\n"), 0o600)
	}
	tx := filepath.Join(home, "t.jsonl")
	os.WriteFile(tx, []byte(assistantText(`<cc-memory filenames="docker.md">x</cc-memory>`)+"\n"), 0o644)
	for _, sid := range []string{"s1:a1", "s1"} {
		if _, err := Run(Input{SessionID: sid, CWD: "/tmp/proj", TranscriptPath: tx,
			ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-09-29"}); err != nil {
			t.Fatal(err)
		}
	}
	w, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	for _, want := range []string{"|cite-useful|-tmp-proj\x1fdocker.md|s1\n", "|cite-silent|-tmp-proj\x1fsql.md|s1\n"} {
		if !strings.Contains(string(w), want) {
			t.Errorf("parent row %q missing:\n%s", want, w)
		}
	}
}
