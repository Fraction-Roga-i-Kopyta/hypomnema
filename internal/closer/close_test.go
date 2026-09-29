package closer

import (
	"encoding/json"
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
	os.WriteFile(tx, []byte(`{"type":"assistant","sessionId":"s1","message":{"content":[{"type":"text","text":"<cc-memory filenames=\"docker.md\">used it to fix it</cc-memory>"}]}}`+"\n"), 0o644)

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
	for _, want := range []string{"|cite-useful|-tmp-proj\x1fdocker.md|s1", "|cite-silent|-tmp-proj\x1fsql.md|s1", "|session-close|s1|s1", "|session-metrics|"} {
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
// a candidate-confirmed WAL event alongside cite-useful; an uncited
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
	os.WriteFile(tx, []byte(`{"type":"assistant","sessionId":"s1","message":{"content":[{"type":"text","text":"<cc-memory filenames=\"cited.md\">applied cited-rule here</cc-memory>"}]}}`+"\n"), 0o644)

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
// holdout-hit, absent → holdout-miss; neither touches the cite-* events. A
// slug present in BOTH lists (pull contamination) gets cite-* events only.
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
	os.WriteFile(tx, []byte(`{"type":"assistant","sessionId":"s1","message":{"content":[{"type":"text","text":"I quoted the tariff and <cc-memory filenames=\"pulled.md\">used pulled-rule</cc-memory>"}]}}`+"\n"), 0o644)

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
		"|cite-useful|-tmp-proj\x1fapplied.md", "|cite-silent|-tmp-proj\x1fapplied.md",
		"|cite-useful|-tmp-proj\x1fforgotten.md", "|cite-silent|-tmp-proj\x1fforgotten.md",
		"|holdout-hit|-tmp-proj\x1fpulled.md", "|holdout-miss|-tmp-proj\x1fpulled.md",
	} {
		if strings.Contains(w, bad) {
			t.Fatalf("unexpected %q in WAL:\n%s", bad, w)
		}
	}
	if !strings.Contains(w, "|cite-useful|-tmp-proj\x1fpulled.md") {
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

// close runs on every Stop (per turn). The per-fact classification rows must
// be written once per (event, fact, session), not once per turn — one live
// session had written 16 341 trigger-silent rows for a dozen facts.
func TestRun_ClassificationRowsOncePerSession(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	os.MkdirAll(filepath.Join(memDir, ".runtime"), 0o755)
	os.MkdirAll(projDir, 0o755)
	os.WriteFile(filepath.Join(projDir, "cited.md"),
		[]byte("---\nname: cited-rule\ntype: mistake\n---\nbody\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "quiet.md"),
		[]byte("---\nname: quiet-rule\ntype: mistake\n---\nbody\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "held.md"),
		[]byte("---\nname: held-rule\ntype: mistake\n---\nbody\n"), 0o644)
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(memDir, ".runtime", "injected-s1.list"), []byte("cited.md\nquiet.md\n"), 0o600)
	os.WriteFile(filepath.Join(memDir, ".runtime", "holdout-s1.list"), []byte("held.md\n"), 0o600)
	tx := filepath.Join(home, "t.jsonl")
	os.WriteFile(tx, []byte(`{"type":"assistant","sessionId":"s1","message":{"content":[{"type":"text","text":"<cc-memory filenames=\"cited.md\">applied cited-rule here</cc-memory>"}]}}`+"\n"), 0o644)
	in := Input{SessionID: "s1", CWD: "/tmp/proj", TranscriptPath: tx,
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-08-16"}

	for turn := 0; turn < 3; turn++ { // three Stop events in one session
		if _, err := Run(in); err != nil {
			t.Fatalf("Run #%d: %v", turn, err)
		}
	}
	wal, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	w := string(wal)
	count := func(sub string) int { return strings.Count(w, sub) }
	if n := count("|cite-useful|-tmp-proj\x1fcited.md|s1"); n != 1 {
		t.Errorf("cite-useful rows for cited = %d, want 1:\n%s", n, w)
	}
	if n := count("|cite-silent|-tmp-proj\x1fquiet.md|s1"); n != 1 {
		t.Errorf("cite-silent rows for quiet = %d, want 1:\n%s", n, w)
	}
	if n := count("|holdout-miss|-tmp-proj\x1fheld.md|s1"); n != 1 {
		t.Errorf("holdout-miss rows for held = %d, want 1:\n%s", n, w)
	}
	// Per-turn bookkeeping rows are intentionally NOT deduped (doctor reads them).
	if n := count("|session-close|s1|s1"); n != 3 {
		t.Errorf("session-close rows = %d, want 3 (one per turn)", n)
	}
	// A verdict that changes later in the session still lands: silent → useful.
	os.WriteFile(tx, []byte(`{"type":"assistant","sessionId":"s1","message":{"content":[{"type":"text","text":"<cc-memory filenames=\"cited.md,quiet.md\">applied both</cc-memory>"}]}}`+"\n"), 0o644)
	if _, err := Run(in); err != nil {
		t.Fatal(err)
	}
	wal, _ = os.ReadFile(filepath.Join(memDir, ".wal"))
	w = string(wal)
	if n := count("|cite-useful|-tmp-proj\x1fquiet.md|s1"); n != 1 {
		t.Errorf("quiet became useful: want exactly one useful row, got %d", n)
	}
	if n := count("|cite-silent|-tmp-proj\x1fquiet.md|s1"); n != 1 {
		t.Errorf("earlier silent row stays (useful-wins is the reader's job), got %d", n)
	}
}

func closeFixture(t *testing.T, files map[string]string, injected []string, assistant string) (memDir string, run func() Result) {
	t.Helper()
	home := t.TempDir()
	memDir = filepath.Join(home, ".claude", "memory")
	projDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	os.MkdirAll(filepath.Join(memDir, ".runtime"), 0o755)
	os.MkdirAll(projDir, 0o755)
	for name, body := range files {
		os.WriteFile(filepath.Join(projDir, name), []byte(body), 0o644)
	}
	os.WriteFile(filepath.Join(memDir, ".wal"), nil, 0o644)
	os.WriteFile(filepath.Join(memDir, ".runtime", "injected-s1.list"), []byte(strings.Join(injected, "\n")+"\n"), 0o600)
	tx := filepath.Join(home, "t.jsonl")
	os.WriteFile(tx, []byte(assistant+"\n"), 0o644)
	return memDir, func() Result {
		res, err := Run(Input{SessionID: "s1", CWD: "/tmp/proj", TranscriptPath: tx,
			ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-09-29"})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return res
	}
}

func assistantText(s string) string {
	b, _ := json.Marshal(map[string]any{"type": "assistant", "sessionId": "s1",
		"message": map[string]any{"content": []map[string]string{{"type": "text", "text": s}}}})
	return string(b)
}

func TestRun_CitationSignal(t *testing.T) {
	files := map[string]string{
		"docker.md": "---\nname: Docker cache\ntype: mistake\n---\ndocker cache\n",
		"sql.md":    "---\nname: SQL index\ntype: knowledge\n---\nsql index\n",
		"extra.md":  "---\nname: Extra\ntype: note\n---\nextra\n",
	}
	cases := []struct {
		name       string
		text       string
		wantUseful []string
		wantSilent []string
	}{
		{"cited injected + uncited injected (guard on)",
			`<cc-memory filenames="docker.md">fixed the cache</cc-memory>`, []string{"docker.md"}, []string{"sql.md"}},
		{"no citation at all → no silent (guard off)",
			`used docker.md and SQL index to fix it`, nil, nil},
		{"citing a non-injected fact counts as useful",
			`<cc-memory filenames="extra.md">per extra</cc-memory>`, []string{"extra.md"}, []string{"docker.md", "sql.md"}},
		{"unknown file name is ignored and does not arm the guard",
			`<cc-memory filenames="nope.md">x</cc-memory>`, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memDir, run := closeFixture(t, files, []string{"docker.md", "sql.md"}, assistantText(tc.text))
			run()
			wal, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
			w := string(wal)
			for _, s := range tc.wantUseful {
				if !strings.Contains(w, "|cite-useful|-tmp-proj\x1f"+s+"|s1") {
					t.Errorf("missing cite-useful for %s:\n%s", s, w)
				}
			}
			for _, s := range tc.wantSilent {
				if !strings.Contains(w, "|cite-silent|-tmp-proj\x1f"+s+"|s1") {
					t.Errorf("missing cite-silent for %s:\n%s", s, w)
				}
			}
			if n := strings.Count(w, "|cite-useful|") + strings.Count(w, "|cite-silent|"); n != len(tc.wantUseful)+len(tc.wantSilent) {
				t.Errorf("unexpected classification rows (%d):\n%s", n, w)
			}
			if strings.Contains(w, "|trigger-useful|") || strings.Contains(w, "|trigger-silent|") {
				t.Errorf("close must not write trigger-* anymore:\n%s", w)
			}
		})
	}
}

func TestRun_CitationInThinkingCounts(t *testing.T) {
	files := map[string]string{"docker.md": "---\nname: Docker cache\ntype: mistake\n---\ndocker cache\n"}
	line := `{"type":"assistant","sessionId":"s1","message":{"content":[{"type":"thinking","thinking":"<cc-memory filenames=\"docker.md\">x</cc-memory>"}]}}`
	memDir, run := closeFixture(t, files, []string{"docker.md"}, line)
	run()
	wal, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if !strings.Contains(string(wal), "|cite-useful|-tmp-proj\x1fdocker.md|s1") {
		t.Errorf("citation inside thinking must count:\n%s", wal)
	}
}

// TestRun_CitationSignal_NoResolvableCitationEmitsCiteNoneOnce: a readable
// transcript with injected facts but no resolvable citation still closed its
// classification pass — assert exactly one cite-none row after two Run calls
// for the same session, and none when a citation resolved.
func TestRun_CitationSignal_NoResolvableCitationEmitsCiteNoneOnce(t *testing.T) {
	files := map[string]string{
		"docker.md": "---\nname: Docker cache\ntype: mistake\n---\ndocker cache\n",
	}
	memDir, run := closeFixture(t, files, []string{"docker.md"}, assistantText("used docker.md to fix it"))
	run()
	run()
	wal, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	w := string(wal)
	if n := strings.Count(w, "|cite-none|s1|s1"); n != 1 {
		t.Errorf("cite-none rows = %d, want 1:\n%s", n, w)
	}

	memDir2, run2 := closeFixture(t, files, []string{"docker.md"},
		assistantText(`<cc-memory filenames="docker.md">fixed the cache</cc-memory>`))
	run2()
	wal2, _ := os.ReadFile(filepath.Join(memDir2, ".wal"))
	if strings.Contains(string(wal2), "|cite-none|") {
		t.Errorf("cite-none must not appear when a citation resolved:\n%s", wal2)
	}
}
