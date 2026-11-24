package closer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// sameSlugFixture builds a project store and the global store, each holding
// the given files, plus the session's injected list and a transcript.
// Returns the WAL text after one close and the two store dirs.
func sameSlugFixture(t *testing.T, proj, glob map[string]string, injected []string, transcript string) (wal, projDir, globalDir string) {
	t.Helper()
	t.Setenv("HYPOMNEMA_GLOBAL_DIR", "")
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "memory")
	projDir = filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	globalDir = filepath.Join(home, ".claude", "memory-global")
	for _, d := range []string{filepath.Join(memDir, ".runtime"), projDir, globalDir} {
		os.MkdirAll(d, 0o755)
	}
	for name, body := range proj {
		os.WriteFile(filepath.Join(projDir, name), []byte(body), 0o644)
	}
	for name, body := range glob {
		os.WriteFile(filepath.Join(globalDir, name), []byte(body), 0o644)
	}
	os.WriteFile(filepath.Join(memDir, ".wal"), nil, 0o644)
	os.WriteFile(filepath.Join(memDir, ".runtime", "injected-s1.list"), []byte(strings.Join(injected, "\n")+"\n"), 0o600)
	tx := filepath.Join(home, "t.jsonl")
	// The transcript may name the store dirs (Read paths) — fill them in.
	transcript = strings.ReplaceAll(transcript, "@PROJ@", projDir)
	transcript = strings.ReplaceAll(transcript, "@GLOBAL@", globalDir)
	os.WriteFile(tx, []byte(transcript+"\n"), 0o644)
	if _, err := Run(Input{SessionID: "s1", CWD: "/tmp/proj", TranscriptPath: tx,
		ClaudeHome: filepath.Join(home, ".claude"), MemoryDir: memDir, Today: "2026-11-24"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return string(mustRead(t, filepath.Join(memDir, ".wal"))), projDir, globalDir
}

// citeRows returns the WAL's classification rows (cite-useful / cite-silent /
// cite-undelivered / candidate-confirmed), one per line.
func citeRows(w string) []string {
	var out []string
	for _, ln := range strings.Split(w, "\n") {
		for _, ev := range []string{"|cite-useful|", "|cite-silent|", "|cite-undelivered|", "|candidate-confirmed|"} {
			if strings.Contains(ln, ev) {
				out = append(out, ln)
			}
		}
	}
	return out
}

// Review focus 3: a cited file name credits the fact(s) with that name that
// were DELIVERED this session — not whichever store wins a basename tie.
func TestClose_CitationAttributesDeliveredStore(t *testing.T) {
	x := "---\nname: x\ntype: knowledge\n---\nx body\n"
	files := map[string]string{"x.md": x}
	pX := native.QKey("-tmp-proj", "x.md")
	gX := native.QKey(native.GlobalProject, "x.md")
	cite := assistantText(`<cc-memory filenames="x.md">applied x</cc-memory>`)
	readGlobal := assistantParts(
		map[string]any{"type": "tool_use", "id": "r1", "name": "Read",
			"input": map[string]any{"file_path": "@GLOBAL@/x.md"}},
		map[string]any{"type": "text", "text": `<cc-memory filenames="x.md">applied x</cc-memory>`},
	) + "\n" + toolResultLine("r1", false)
	cases := []struct {
		name       string
		injected   []string
		transcript string
		want       []string // exact classification rows (event|target)
	}{
		{"global only delivered → global credited", []string{gX}, cite,
			[]string{"|cite-useful|" + gX}},
		{"project only delivered → project credited", []string{pX}, cite,
			[]string{"|cite-useful|" + pX}},
		{"both delivered → both credited", []string{gX, pX}, cite,
			[]string{"|cite-useful|" + pX, "|cite-useful|" + gX}},
		{"global read via Read → global credited, other injected silent",
			[]string{native.QKey("-tmp-proj", "other.md")}, readGlobal,
			[]string{"|cite-useful|" + gX, "|cite-silent|" + native.QKey("-tmp-proj", "other.md")}},
		{"none delivered → project-first undelivered (unchanged)",
			[]string{native.QKey("-tmp-proj", "other.md")}, cite,
			[]string{"|cite-undelivered|" + pX}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proj := map[string]string{"x.md": x, "other.md": "---\nname: other\ntype: note\n---\nother\n"}
			w, _, _ := sameSlugFixture(t, proj, files, tc.injected, tc.transcript)
			got := citeRows(w)
			if len(got) != len(tc.want) {
				t.Fatalf("classification rows = %d, want %d:\n%s", len(got), len(tc.want), strings.Join(got, "\n"))
			}
			for _, want := range tc.want {
				if !strings.Contains(w, want+"|s1") {
					t.Errorf("missing %q:\n%s", want, strings.Join(got, "\n"))
				}
			}
		})
	}
}

// Candidate graduation reads the credited fact's own status: a candidate in
// one store and an active fact of the same name in the other must not
// borrow each other's status.
func TestClose_CandidateConfirmedUsesOwnStatus(t *testing.T) {
	cand := "---\nname: x\ntype: mistake\nstatus: candidate\n---\nx body\n"
	act := "---\nname: x\ntype: mistake\nstatus: active\n---\nx body\n"
	pX := native.QKey("-tmp-proj", "x.md")
	gX := native.QKey(native.GlobalProject, "x.md")
	cite := assistantText(`<cc-memory filenames="x.md">applied x</cc-memory>`)

	t.Run("global candidate delivered, project active → global confirmed", func(t *testing.T) {
		w, _, _ := sameSlugFixture(t, map[string]string{"x.md": act}, map[string]string{"x.md": cand}, []string{gX}, cite)
		if !strings.Contains(w, "|candidate-confirmed|"+gX+"|s1") {
			t.Errorf("global candidate must be confirmed:\n%s", w)
		}
		if strings.Contains(w, "|candidate-confirmed|"+pX) {
			t.Errorf("project fact is active and was not delivered — no confirm:\n%s", w)
		}
	})
	t.Run("project candidate delivered, global active → project confirmed", func(t *testing.T) {
		w, _, _ := sameSlugFixture(t, map[string]string{"x.md": cand}, map[string]string{"x.md": act}, []string{pX}, cite)
		if !strings.Contains(w, "|candidate-confirmed|"+pX+"|s1") {
			t.Errorf("project candidate must be confirmed:\n%s", w)
		}
		if strings.Contains(w, "|candidate-confirmed|"+gX) {
			t.Errorf("global fact is active — no confirm:\n%s", w)
		}
	})
	t.Run("global active delivered, project candidate not delivered → nothing confirmed", func(t *testing.T) {
		w, _, _ := sameSlugFixture(t, map[string]string{"x.md": cand}, map[string]string{"x.md": act}, []string{gX}, cite)
		if strings.Contains(w, "|candidate-confirmed|") {
			t.Errorf("only an active fact was credited — no confirm:\n%s", w)
		}
	})
}
