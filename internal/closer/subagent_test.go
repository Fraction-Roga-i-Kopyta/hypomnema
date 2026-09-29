package closer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func subagentFixture(t *testing.T, files map[string]string, injected, holdout []string, transcript string) (memDir, projDir string, run func() Result) {
	t.Helper()
	home := t.TempDir()
	memDir = filepath.Join(home, ".claude", "memory")
	projDir = filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	os.MkdirAll(filepath.Join(memDir, ".runtime"), 0o755)
	os.MkdirAll(projDir, 0o755)
	for name, body := range files {
		os.WriteFile(filepath.Join(projDir, name), []byte(body), 0o644)
	}
	os.WriteFile(filepath.Join(memDir, ".wal"), nil, 0o644)
	os.WriteFile(filepath.Join(memDir, ".runtime", "injected-s1_a1.list"), []byte(strings.Join(injected, "\n")+"\n"), 0o600)
	if len(holdout) > 0 {
		os.WriteFile(filepath.Join(memDir, ".runtime", "holdout-s1_a1.list"), []byte(strings.Join(holdout, "\n")+"\n"), 0o600)
	}
	tx := filepath.Join(home, "agent.jsonl")
	os.WriteFile(tx, []byte(transcript+"\n"), 0o644)
	return memDir, projDir, func() Result {
		res, err := Run(Input{SessionID: "s1:a1", CWD: "/tmp/proj", TranscriptPath: tx,
			ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-09-29",
			Subagent: true})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return res
	}
}

var subagentFiles = map[string]string{
	"docker.md": "---\nname: Docker cache\ntype: mistake\nstatus: candidate\n---\ndocker cache\n",
	"sql.md":    "---\nname: SQL index\ntype: knowledge\nevidence: [\"sql index\"]\n---\nsql index\n",
	"extra.md":  "---\nname: Extra\ntype: note\n---\nextra\n",
}

func TestRun_SubagentClassifiesUnderItsKey(t *testing.T) {
	memDir, projDir, run := subagentFixture(t, subagentFiles, []string{"docker.md", "sql.md"}, nil,
		assistantText(`<cc-memory filenames="docker.md">fixed the cache</cc-memory>`))
	run()
	w, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	wal := string(w)
	for _, want := range []string{
		"|cite-useful|-tmp-proj\x1fdocker.md|s1:a1",
		"|cite-silent|-tmp-proj\x1fsql.md|s1:a1",
		"|candidate-confirmed|-tmp-proj\x1fdocker.md|s1:a1",
		"|session-close|s1:a1|s1:a1",
	} {
		if !strings.Contains(wal, want) {
			t.Errorf("missing %q in WAL:\n%s", want, wal)
		}
	}
	if strings.Contains(wal, "|session-metrics|") {
		t.Errorf("subagent close must not write session-metrics:\n%s", wal)
	}
	for _, p := range []string{filepath.Join(projDir, "MEMORY.md"), filepath.Join(memDir, "self-profile.md"), filepath.Join(memDir, ".sidecar.db")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("subagent close must leave session-level work to the parent's Stop; found %s", p)
		}
	}
}

func TestRun_SubagentUndeliveredCitation(t *testing.T) {
	memDir, _, run := subagentFixture(t, subagentFiles, []string{"docker.md"}, nil,
		assistantText(`<cc-memory filenames="extra.md">per extra</cc-memory>`))
	run()
	w, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	wal := string(w)
	if !strings.Contains(wal, "|cite-undelivered|-tmp-proj\x1fextra.md|s1:a1") {
		t.Errorf("missing cite-undelivered under the subagent key:\n%s", wal)
	}
	if !strings.Contains(wal, "|cite-none|s1:a1|s1:a1") {
		t.Errorf("missing cite-none under the subagent key:\n%s", wal)
	}
	if strings.Contains(wal, "|cite-useful|") || strings.Contains(wal, "|cite-silent|") {
		t.Errorf("undelivered citation must not credit or arm the guard:\n%s", wal)
	}
}

func TestRun_SubagentSkipsHoldout(t *testing.T) {
	memDir, _, run := subagentFixture(t, subagentFiles, []string{"docker.md"}, []string{"sql.md"},
		assistantText(`the sql index was the problem`))
	run()
	w, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if strings.Contains(string(w), "|holdout-hit|") || strings.Contains(string(w), "|holdout-miss|") {
		t.Errorf("subagent close must not classify holdout observations:\n%s", w)
	}
}

func TestRun_SubagentRepeatStopWritesOneSessionClose(t *testing.T) {
	memDir, _, run := subagentFixture(t, subagentFiles, []string{"docker.md"}, nil,
		assistantText(`<cc-memory filenames="docker.md">fixed the cache</cc-memory>`))
	run()
	run()
	w, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if n := strings.Count(string(w), "|session-close|s1:a1|s1:a1"); n != 1 {
		t.Fatalf("want exactly one session-close for the subagent key, got %d:\n%s", n, w)
	}
	if n := strings.Count(string(w), "|cite-useful|"); n != 1 {
		t.Fatalf("want exactly one cite-useful row, got %d:\n%s", n, w)
	}
}
