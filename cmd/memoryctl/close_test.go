package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloseVerb(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	os.MkdirAll(filepath.Join(memDir, ".runtime"), 0o755)
	os.MkdirAll(projDir, 0o755)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker\ntype: mistake\n---\ndocker\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(memDir, ".runtime", "injected-s1.list"), []byte("docker.md\n"), 0o600)
	tx := filepath.Join(home, "t.jsonl")
	os.WriteFile(tx, []byte(`{"type":"assistant","sessionId":"s1","message":{"content":[{"type":"text","text":"docker.md helped"}]}}`+"\n"), 0o644)

	env := map[string]string{
		"CLAUDE_HOME": filepath.Join(home, ".claude"), "CLAUDE_MEMORY_DIR": memDir,
		"HYPOMNEMA_TODAY": "2026-05-29",
	}
	stdin := `{"session_id":"s1","cwd":"/tmp/proj","transcript_path":"` + tx + `"}`
	_, errOut, code := runStdin(t, env, stdin, "close")
	if code != 0 {
		t.Fatalf("close exit=%d stderr=%s", code, errOut)
	}
	wal, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if !strings.Contains(string(wal), "|trigger-useful|docker.md|s1") {
		t.Errorf("expected trigger-useful for cited docker.md:\n%s", wal)
	}
	if !strings.Contains(string(wal), "|session-close|s1|s1") {
		t.Errorf("expected session-close:\n%s", wal)
	}
}

func TestCloseVerb_FailSafe(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(memDir, 0o755)
	env := map[string]string{"CLAUDE_HOME": filepath.Join(home, ".claude"), "CLAUDE_MEMORY_DIR": memDir}
	_, _, code := runStdin(t, env, "garbage", "close")
	if code != 0 {
		t.Fatalf("bad stdin must exit 0, got %d", code)
	}
}

func TestCloseVerb_EmptySessionFallsBackToUnknown(t *testing.T) { // review H3
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(memDir, 0o755)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	env := map[string]string{"CLAUDE_HOME": filepath.Join(home, ".claude"), "CLAUDE_MEMORY_DIR": memDir}
	// Envelope with no session_id — must NOT write empty-session WAL rows,
	// which `wal validate` rejects (poisoning the CI gate).
	_, _, code := runStdin(t, env, `{}`, "close")
	if code != 0 {
		t.Fatalf("close must be fail-safe (exit 0), got %d", code)
	}
	wal, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	for _, ln := range strings.Split(strings.TrimSpace(string(wal)), "\n") {
		if ln == "" {
			continue
		}
		cols := strings.Split(ln, "|")
		if len(cols) == 4 && cols[3] == "" {
			t.Errorf("close wrote a WAL row with an empty session column: %q", ln)
		}
	}
	// And the WAL must pass validation.
	_, _, vcode := runStdin(t, env, "", "wal", "validate")
	if vcode != 0 {
		t.Errorf("wal validate rejects close's rows (exit %d)", vcode)
	}
}

func TestCloseVerb_UnreadableTranscriptSkipsClassification(t *testing.T) { // review E4
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	os.MkdirAll(filepath.Join(memDir, ".runtime"), 0o755)
	os.MkdirAll(projDir, 0o755)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker\ntype: mistake\n---\ndocker\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(memDir, ".runtime", "injected-s1.list"), []byte("docker.md\n"), 0o600)

	env := map[string]string{
		"CLAUDE_HOME": filepath.Join(home, ".claude"), "CLAUDE_MEMORY_DIR": memDir,
		"HYPOMNEMA_TODAY": "2026-05-29",
	}
	// transcript_path points at a file that does not exist — usefulness is
	// unobservable, so close must NOT fabricate trigger-silent for the injected
	// facts (review E4). session-metrics/close still get written.
	stdin := `{"session_id":"s1","cwd":"/tmp/proj","transcript_path":"` + filepath.Join(home, "nope.jsonl") + `"}`
	_, errOut, code := runStdin(t, env, stdin, "close")
	if code != 0 {
		t.Fatalf("close exit=%d stderr=%s", code, errOut)
	}
	wal := mustReadStr(t, filepath.Join(memDir, ".wal"))
	if strings.Contains(wal, "trigger-silent") || strings.Contains(wal, "trigger-useful") {
		t.Errorf("unreadable transcript must skip trigger classification, got:\n%s", wal)
	}
	if !strings.Contains(wal, "|session-close|") {
		t.Errorf("session-close should still be written:\n%s", wal)
	}
}

func mustReadStr(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
