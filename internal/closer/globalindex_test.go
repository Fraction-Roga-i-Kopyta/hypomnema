package closer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The harness loads only the project's MEMORY.md, so a parent Stop also
// regenerates the global store's index — the file a pull-only user imports
// from CLAUDE.md. A subagent's close leaves both indexes to the parent.
func TestRun_RegeneratesGlobalIndexOnParentStop(t *testing.T) {
	t.Setenv("HYPOMNEMA_GLOBAL_DIR", "")
	claudeHome := filepath.Join(t.TempDir(), ".claude")
	stores := map[string]string{
		"memory":  filepath.Join(claudeHome, "memory"),
		"project": filepath.Join(claudeHome, "projects", "-srv-api", "memory"),
		"global":  filepath.Join(claudeHome, "memory-global"),
	}
	for _, dir := range stores {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	facts := map[string]string{
		filepath.Join(stores["project"], "migrations.md"): "---\nname: migrations\ntype: knowledge\n---\nrun them in order\n",
		filepath.Join(stores["global"], "tz.md"):          "---\nname: server-timezone\ndescription: Prod servers run in UTC\ntype: knowledge\n---\nUTC\n",
	}
	for path, body := range facts {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	globalIndex := filepath.Join(stores["global"], "MEMORY.md")
	stop := func(sid string, subagent bool) {
		t.Helper()
		_, err := Run(Input{
			SessionID: sid, CWD: "/srv/api", TranscriptPath: filepath.Join(claudeHome, "absent.jsonl"),
			ClaudeHome: claudeHome, MemoryDir: stores["memory"], Today: "2026-06-01", Subagent: subagent,
		})
		if err != nil {
			t.Fatalf("Run(%s): %v", sid, err)
		}
	}

	stop("sess:agent", true)
	if _, err := os.Stat(globalIndex); err == nil {
		t.Fatal("a subagent close must not write the global index")
	}

	stop("sess", false)
	got, err := os.ReadFile(globalIndex)
	if err != nil {
		t.Fatalf("parent close did not write the global index: %v", err)
	}
	want := "- [server-timezone](" + filepath.Join(stores["global"], "tz.md") + ") — Prod servers run in UTC\n"
	if !strings.Contains(string(got), want) {
		t.Errorf("want entry %q in:\n%s", want, got)
	}
	if _, err := os.Stat(filepath.Join(stores["project"], "MEMORY.md")); err != nil {
		t.Errorf("the project index must be written alongside: %v", err)
	}
}
