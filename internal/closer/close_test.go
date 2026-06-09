package closer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_EmitsClosingEvents(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	os.MkdirAll(filepath.Join(memDir, ".runtime"), 0o755)
	os.MkdirAll(projDir, 0o755)
	os.WriteFile(filepath.Join(projDir, "docker.md"),
		[]byte("---\nname: Docker cache\ntype: mistake\n---\ndocker cache\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "sql.md"),
		[]byte("---\nname: SQL index\ntype: knowledge\n---\nsql index\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(memDir, ".runtime", "injected-s1.list"),
		[]byte("docker.md\nsql.md\n"), 0o600)
	tx := filepath.Join(home, "t.jsonl")
	os.WriteFile(tx, []byte(`{"type":"assistant","sessionId":"s1","message":{"content":[{"type":"text","text":"used docker.md to fix it"}]}}`+"\n"), 0o644)

	res, err := Run(Input{
		SessionID: "s1", CWD: "/tmp/proj", TranscriptPath: tx,
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-05-29",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Useful != 1 || res.Silent != 1 {
		t.Errorf("useful=%d silent=%d, want 1/1", res.Useful, res.Silent)
	}
	wal, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	w := string(wal)
	for _, want := range []string{"|trigger-useful|docker.md|s1", "|trigger-silent|sql.md|s1", "|session-close|s1|s1", "|session-metrics|"} {
		if !strings.Contains(w, want) {
			t.Errorf("WAL missing %q:\n%s", want, w)
		}
	}
	if _, err := os.Stat(filepath.Join(memDir, "self-profile.md")); err != nil {
		t.Errorf("self-profile not generated: %v", err)
	}
}

func TestRun_MissingTranscriptIsNotFatal(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(filepath.Join(memDir, ".runtime"), 0o755)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	res, err := Run(Input{
		SessionID: "s1", CWD: "/tmp/proj", TranscriptPath: filepath.Join(home, "nope.jsonl"),
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-05-29",
	})
	if err != nil {
		t.Fatalf("missing transcript must not error: %v", err)
	}
	wal, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if !strings.Contains(string(wal), "|session-close|s1|s1") {
		t.Errorf("session-close must be emitted even with no transcript:\n%s", wal)
	}
	_ = res
}

// session-metrics must carry real transcript-derived numbers, not the
// hardcoded zeros v2 shipped with.
func TestRun_SessionMetricsFromTranscript(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	os.MkdirAll(filepath.Join(memDir, ".runtime"), 0o755)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	tx := filepath.Join(home, "t.jsonl")
	transcript := `{"type":"user","sessionId":"s1","timestamp":"2026-05-29T10:00:00Z","message":{"content":[{"type":"text","text":"go"}]}}
{"type":"assistant","timestamp":"2026-05-29T10:00:30Z","message":{"content":[{"type":"text","text":"ok"},{"type":"tool_use"},{"type":"tool_use"}]}}
{"type":"user","timestamp":"2026-05-29T10:01:00Z","message":{"content":[{"type":"tool_result","is_error":true}]}}
{"type":"assistant","timestamp":"2026-05-29T10:02:00Z","message":{"content":[{"type":"tool_use"}]}}`
	os.WriteFile(tx, []byte(transcript+"\n"), 0o644)

	if _, err := Run(Input{
		SessionID: "s1", CWD: "/tmp/proj", TranscriptPath: tx,
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-05-29",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	wal, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	want := "|session-metrics|domains:_global_,error_count:1,tool_calls:3,duration:120s|s1"
	if !strings.Contains(string(wal), want) {
		t.Errorf("WAL missing %q:\n%s", want, wal)
	}
}
