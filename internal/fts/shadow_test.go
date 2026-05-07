package fts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShadow_ProjectFilter_RejectsOtherProjects pins the precision fix:
// shadow-pass must drop files whose `project:` frontmatter is neither
// empty/global nor equal to CurrentProject. Without this filter, FTS5
// keyword-overlap surfaces cross-project files (e.g. an iOS Swift
// strategy in a JIRA Groovy session) and the resulting shadow-miss
// events are pure false-positives.
func TestShadow_ProjectFilter_RejectsOtherProjects(t *testing.T) {
	mem := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mem, "mistakes"), 0o755); err != nil {
		t.Fatal(err)
	}

	fixtures := map[string]string{
		"current.md": "---\ntype: mistake\nproject: current-proj\n---\nzzqx marker token current.\n",
		"global.md":  "---\ntype: mistake\nproject: global\n---\nzzqx marker token global.\n",
		"empty.md":   "---\ntype: mistake\n---\nzzqx marker token empty.\n",
		"other.md":   "---\ntype: mistake\nproject: other-proj\n---\nzzqx marker token other.\n",
	}
	for name, body := range fixtures {
		if err := os.WriteFile(filepath.Join(mem, "mistakes", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := ShadowConfig{
		MaxShadow:      10,
		Today:          "2026-05-07",
		CurrentProject: "current-proj",
	}
	if err := Shadow(context.Background(), mem, "zzqx", "test-session", nil, cfg); err != nil {
		t.Fatalf("Shadow: %v", err)
	}

	wal, err := os.ReadFile(filepath.Join(mem, ".wal"))
	if err != nil {
		t.Fatalf("read wal: %v", err)
	}
	walStr := string(wal)

	for _, s := range []string{"current", "global", "empty"} {
		if !strings.Contains(walStr, "|shadow-miss|"+s+"|") {
			t.Errorf("expected shadow-miss for %q in WAL, got:\n%s", s, walStr)
		}
	}
	if strings.Contains(walStr, "|shadow-miss|other|") {
		t.Errorf("other-project file MUST be filtered, but appeared in WAL:\n%s", walStr)
	}
}

// TestShadow_ProjectFilter_BackCompat verifies that when CurrentProject
// is empty (legacy caller, or session whose cwd is not in projects.json)
// no project filtering happens — all admissible files surface, matching
// pre-patch behaviour.
func TestShadow_ProjectFilter_BackCompat(t *testing.T) {
	mem := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mem, "mistakes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mem, "mistakes", "other.md"),
		[]byte("---\ntype: mistake\nproject: other-proj\n---\nzzqx marker token.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := ShadowConfig{
		MaxShadow: 4,
		Today:     "2026-05-07",
	}
	_ = Shadow(context.Background(), mem, "zzqx", "test-session", nil, cfg)

	wal, _ := os.ReadFile(filepath.Join(mem, ".wal"))
	if !strings.Contains(string(wal), "|shadow-miss|other|") {
		t.Errorf("with empty CurrentProject the cross-project file should still surface; got WAL:\n%s", wal)
	}
}

// TestProjectAllowed_FrontmatterValueShapes pins the parser against
// frontmatter-syntax variants the bash awk also tolerates: tab after
// key, single/double-quoted scalars, missing project line, project line
// outside the frontmatter block.
func TestProjectAllowed_FrontmatterValueShapes(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name   string
		body   string
		curr   string
		wanted bool
	}{
		{"plain-current", "---\ntype: mistake\nproject: foo\n---\nbody", "foo", true},
		{"plain-other", "---\ntype: mistake\nproject: bar\n---\nbody", "foo", false},
		{"global-allowed", "---\ntype: mistake\nproject: global\n---\nbody", "foo", true},
		{"missing-project", "---\ntype: mistake\n---\nbody", "foo", true},
		{"double-quoted", "---\ntype: mistake\nproject: \"foo\"\n---\nbody", "foo", true},
		{"single-quoted", "---\ntype: mistake\nproject: 'foo'\n---\nbody", "foo", true},
		{"tab-after-key", "---\ntype: mistake\nproject:\tfoo\n---\nbody", "foo", true},
		{"trailing-spaces", "---\ntype: mistake\nproject: foo   \n---\nbody", "foo", true},
		{"project-line-outside-fm", "---\ntype: mistake\n---\nproject: bar\n", "foo", true},
	}
	for _, tc := range cases {
		path := filepath.Join(dir, tc.name+".md")
		if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := projectAllowed(path, tc.curr); got != tc.wanted {
			t.Errorf("%s: projectAllowed(curr=%q)=%v, want %v\nbody:\n%s",
				tc.name, tc.curr, got, tc.wanted, tc.body)
		}
	}
}
