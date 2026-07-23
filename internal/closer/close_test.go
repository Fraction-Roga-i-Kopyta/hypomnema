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
	for _, want := range []string{"\x1fdocker.md|s1", "\x1fsql.md|s1", "|session-close|s1|s1", "|session-metrics|"} {
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

// TestRun_CandidateConfirmed: a candidate fact cited in the transcript gets
// a candidate-confirmed WAL event alongside trigger-useful; an uncited
// candidate does not.
func TestRun_CandidateConfirmed(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	os.MkdirAll(filepath.Join(memDir, ".runtime"), 0o755)
	os.MkdirAll(projDir, 0o755)
	os.WriteFile(filepath.Join(projDir, "cited.md"),
		[]byte("---\nname: cited-rule\ntype: mistake\nstatus: candidate\n---\nbody\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "quiet.md"),
		[]byte("---\nname: quiet-rule\ntype: mistake\nstatus: candidate\n---\nbody\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(memDir, ".runtime", "injected-s1.list"),
		[]byte("cited.md\nquiet.md\n"), 0o600)
	tx := filepath.Join(home, "t.jsonl")
	os.WriteFile(tx, []byte(`{"type":"assistant","sessionId":"s1","message":{"content":[{"type":"text","text":"applied cited-rule here"}]}}`+"\n"), 0o644)

	if _, err := Run(Input{
		SessionID: "s1", CWD: "/tmp/proj", TranscriptPath: tx,
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-07-23",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	wal, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	w := string(wal)
	if !strings.Contains(w, "|candidate-confirmed|") || !strings.Contains(w, "\x1fcited.md|s1") {
		t.Fatalf("missing candidate-confirmed for cited fact:\n%s", w)
	}
	if strings.Contains(w, "candidate-confirmed|-tmp-proj\x1fquiet.md") {
		t.Fatalf("silent candidate must not confirm:\n%s", w)
	}
}

// TestRun_HoldoutClassification: facts in the session's holdout list are
// classified against the transcript — evidence present without injection →
// holdout-hit, absent → holdout-miss; neither touches trigger events. A
// slug present in BOTH lists (pull contamination) gets trigger events only.
func TestRun_HoldoutClassification(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	os.MkdirAll(filepath.Join(memDir, ".runtime"), 0o755)
	os.MkdirAll(projDir, 0o755)
	os.WriteFile(filepath.Join(projDir, "applied.md"),
		[]byte("---\nname: applied-rule\ntype: mistake\nevidence:\n  - \"quoted the tariff\"\n---\nx\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "forgotten.md"),
		[]byte("---\nname: forgotten-rule\ntype: mistake\n---\nx\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "pulled.md"),
		[]byte("---\nname: pulled-rule\ntype: mistake\n---\nx\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(memDir, ".runtime", "holdout-s1.list"),
		[]byte("applied.md\nforgotten.md\npulled.md\n"), 0o600)
	// pulled.md was ALSO delivered via recall this session.
	os.WriteFile(filepath.Join(memDir, ".runtime", "injected-s1.list"),
		[]byte("pulled.md\n"), 0o600)
	tx := filepath.Join(home, "t.jsonl")
	os.WriteFile(tx, []byte(`{"type":"assistant","sessionId":"s1","message":{"content":[{"type":"text","text":"I quoted the tariff and used pulled-rule"}]}}`+"\n"), 0o644)

	if _, err := Run(Input{
		SessionID: "s1", CWD: "/tmp/proj", TranscriptPath: tx,
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-07-23",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	w := string(mustRead(t, filepath.Join(memDir, ".wal")))
	if !strings.Contains(w, "|holdout-hit|-tmp-proj\x1fapplied.md|s1") {
		t.Fatalf("missing holdout-hit for applied:\n%s", w)
	}
	if !strings.Contains(w, "|holdout-miss|-tmp-proj\x1fforgotten.md|s1") {
		t.Fatalf("missing holdout-miss for forgotten:\n%s", w)
	}
	for _, bad := range []string{
		"|trigger-useful|-tmp-proj\x1fapplied.md", "|trigger-silent|-tmp-proj\x1fapplied.md",
		"|trigger-useful|-tmp-proj\x1fforgotten.md", "|trigger-silent|-tmp-proj\x1fforgotten.md",
		"|holdout-hit|-tmp-proj\x1fpulled.md", "|holdout-miss|-tmp-proj\x1fpulled.md",
	} {
		if strings.Contains(w, bad) {
			t.Fatalf("unexpected %q in WAL:\n%s", bad, w)
		}
	}
	if !strings.Contains(w, "|trigger-useful|-tmp-proj\x1fpulled.md") {
		t.Fatalf("pull-contaminated fact must classify as injected:\n%s", w)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
