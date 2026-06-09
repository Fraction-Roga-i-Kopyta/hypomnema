package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

// recallFixture builds a CLAUDE_HOME with two project facts and returns
// (env, memDir, projDir). Tests pin both session env vars: the live Claude
// Code session exports CLAUDE_CODE_SESSION_ID and it would leak through
// os.Environ() into the subprocess otherwise.
func recallFixture(t *testing.T) (map[string]string, string, string) {
	t.Helper()
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	os.MkdirAll(projDir, 0o755)
	os.MkdirAll(memDir, 0o755)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: docker-cache\ntype: mistake\ndescription: docker layer cache pitfall\nkeywords: [docker, cache]\n---\ndocker layer cache body\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "redis.md"),
		[]byte("---\nname: redis-ttl\ntype: knowledge\ndescription: redis ttl semantics\nkeywords: [redis, ttl, cache]\n---\nredis ttl body\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	env := map[string]string{
		"CLAUDE_HOME":            filepath.Join(home, ".claude"),
		"CLAUDE_MEMORY_DIR":      memDir,
		"CLAUDE_PROJECT_CWD":     "/tmp/proj",
		"HYPOMNEMA_TODAY":        "2026-06-10",
		"HYPOMNEMA_SESSION_ID":   "s9",
		"CLAUDE_CODE_SESSION_ID": "",
	}
	return env, memDir, projDir
}

func TestRecallVerb(t *testing.T) {
	env, memDir, projDir := recallFixture(t)

	out, errOut, code := run(t, env, "recall", "docker", "cache")
	if code != 0 {
		t.Fatalf("recall exit=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "## docker-cache (mistake, score ") {
		t.Errorf("missing top-1 header, got:\n%s", out)
	}
	if !strings.Contains(out, "docker layer cache body") {
		t.Errorf("missing top-1 body, got:\n%s", out)
	}
	if !strings.Contains(out, "Also relevant:") {
		t.Errorf("missing index section, got:\n%s", out)
	}
	if !strings.Contains(out, filepath.Join(projDir, "redis.md")) {
		t.Errorf("index must carry the absolute path, got:\n%s", out)
	}

	walB, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if !strings.Contains(string(walB), "|recall|docker.md|s9") {
		t.Errorf("missing recall WAL event, got:\n%s", walB)
	}
	if strings.Contains(string(walB), "|recall|redis.md|") {
		t.Errorf("index entries must not emit recall events:\n%s", walB)
	}
	list, _ := os.ReadFile(filepath.Join(memDir, ".runtime", "injected-s9.list"))
	if !strings.Contains(string(list), "docker.md") {
		t.Errorf("top-1 must land in the session list, got %q", list)
	}
	if strings.Contains(string(list), "redis.md") {
		t.Errorf("index entries must not land in the session list, got %q", list)
	}
}

func TestRecallNoMatches(t *testing.T) {
	env, _, _ := recallFixture(t)
	out, _, code := run(t, env, "recall", "kubernetes")
	if code != 0 {
		t.Fatalf("no-match recall must exit 0, got %d", code)
	}
	if !strings.Contains(out, "no matches") {
		t.Errorf("want 'no matches', got %q", out)
	}
}

func TestRecallEmptyQuery(t *testing.T) {
	env, _, _ := recallFixture(t)
	_, errOut, code := run(t, env, "recall")
	if code != 2 {
		t.Fatalf("empty query must exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "empty query") {
		t.Errorf("want usage hint on stderr, got %q", errOut)
	}
}

func TestRecallKFlag(t *testing.T) {
	env, _, _ := recallFixture(t)
	for _, bad := range []string{"0", "-1", "abc", "3x"} {
		_, _, code := run(t, env, "recall", "--k", bad, "docker")
		if code != 2 {
			t.Errorf("--k %q must exit 2, got %d", bad, code)
		}
	}
	out, _, code := run(t, env, "recall", "--k", "1", "docker", "cache")
	if code != 0 {
		t.Fatalf("--k 1 exit=%d", code)
	}
	if strings.Contains(out, "Also relevant:") {
		t.Errorf("--k 1 must suppress the index section, got:\n%s", out)
	}
}

func TestRecallStaleMarkerAndCliFallback(t *testing.T) {
	env, memDir, _ := recallFixture(t)
	// Age docker.md into stale: an old inject, then sidecar rebuild + decay.
	if err := os.WriteFile(filepath.Join(memDir, ".wal"),
		[]byte("2025-01-01|inject|docker.md|s0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := sidecar.Open(filepath.Join(memDir, ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	// claudeHome = CLAUDE_HOME = home/.claude; filepath.Dir(memDir) = home/.claude
	claudeHome := filepath.Dir(memDir)
	files := native.Collect(claudeHome, "/tmp/proj")
	if err := sidecar.Reproject(s, files, filepath.Join(memDir, ".wal"), native.Scope("/tmp/proj")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkStale("2026-06-10"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// No session env at all → WAL sid falls back to "cli", list untouched.
	env["HYPOMNEMA_SESSION_ID"] = ""
	out, errOut, code := run(t, env, "recall", "docker", "cache")
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "[stale]") {
		t.Errorf("stale fact must carry the [stale] marker, got:\n%s", out)
	}
	walB, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if !strings.Contains(string(walB), "|recall|docker.md|cli") {
		t.Errorf("want cli-fallback recall event, got:\n%s", walB)
	}
	if _, err := os.Stat(filepath.Join(memDir, ".runtime")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(memDir, ".runtime"))
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "injected-") {
				t.Errorf("no-session recall must not write a session list, found %s", e.Name())
			}
		}
	}
}
