package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// A project fact and a global fact with the same file name are distinct:
// recall renders the hit's own body, lists one fact per file name (project
// wins, as in injection), and logs the rendered fact's project.
func TestRecall_SameSlugProjectAndGlobal(t *testing.T) {
	f := newStoreFixture(t)
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	f.env["HYPOMNEMA_SESSION_ID"] = "s1"
	f.env["CLAUDE_CODE_SESSION_ID"] = ""
	proj := native.StoreFor(f.claude, "/tmp/proj").Project
	f.globalFact(t, "x", "name: globalx\ntype: knowledge\nkeywords: [alphaword]", "GLOBAL BODY")
	f.factFM(t, "/tmp/proj", "x", "name: projx\ntype: knowledge\nkeywords: [alphaword]", "PROJECT BODY")
	f.factFM(t, "/tmp/proj", "y", "name: projy\ntype: knowledge\nkeywords: [alphaword]", "PROJECT Y")

	out, errOut, code := run(t, f.env, "recall", "alphaword")
	if code != 0 {
		t.Fatalf("recall exit=%d stderr=%s", code, errOut)
	}
	head, rest, _ := strings.Cut(out, "Also relevant:")
	if !strings.Contains(head, "## projx — x.md") || !strings.Contains(head, "PROJECT BODY") {
		t.Errorf("top hit is project x.md and must render its own body:\n%s", out)
	}
	if strings.Contains(out, "GLOBAL BODY") {
		t.Errorf("global x.md body rendered for the project hit:\n%s", out)
	}
	if n := strings.Count(rest, "x.md"); n != 0 {
		t.Errorf("x.md already rendered — Also relevant must not list it again (%d rows):\n%s", n, rest)
	}
	if !strings.Contains(rest, "y.md") {
		t.Errorf("y.md must stay in Also relevant:\n%s", rest)
	}
	wal, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	w := string(wal)
	if !strings.Contains(w, "|recall|"+native.QKey(proj, "x.md")+"|s1") {
		t.Errorf("recall row must carry the rendered fact's project:\n%s", w)
	}
	if strings.Contains(w, "|recall|"+native.QKey(native.GlobalProject, "x.md")) {
		t.Errorf("global x.md was not rendered — no recall row:\n%s", w)
	}
	if got := listLines(t, filepath.Join(f.mem, ".runtime", "injected-s1.list")); strings.Join(got, ",") != native.QKey(proj, "x.md") {
		t.Errorf("session list = %q, want the project QKey only", got)
	}
}

// skill-inject filters each candidate on its OWN file: a same-named fact in
// the other store must neither smuggle a non-learning in nor shut a
// learning out.
func TestSkillInject_FilterUsesOwnFile(t *testing.T) {
	learning := "type: skill-learning\nname: xl\nskill: commit\nstatus: active\ncreated: 2026-09-20"
	note := "type: note\nname: xn\nstatus: active\ncreated: 2026-09-20"
	stdin := `{"session_id":"s1","cwd":"/tmp/proj","tool_input":{"skill":"commit"}}`

	t.Run("global learning, project note", func(t *testing.T) {
		f := newStoreFixture(t)
		f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
		proj := native.StoreFor(f.claude, "/tmp/proj").Project
		f.globalFact(t, "x", learning, "GLOBAL LEARNING")
		f.factFM(t, "/tmp/proj", "x", note, "PROJECT NOTE")
		out, _, code := runStdin(t, f.env, stdin, "skill-inject")
		if code != 0 {
			t.Fatalf("exit=%d", code)
		}
		ctx := additionalContext(t, out)
		if n := strings.Count(ctx, "GLOBAL LEARNING"); n != 1 {
			t.Errorf("global learning must render once, got %d:\n%s", n, ctx)
		}
		if strings.Contains(ctx, "PROJECT NOTE") {
			t.Errorf("project note is not a learning:\n%s", ctx)
		}
		w := string(mustReadFile(t, filepath.Join(f.mem, ".wal")))
		if !strings.Contains(w, "|recall|"+native.QKey(native.GlobalProject, "x.md")+"|s1") {
			t.Errorf("missing recall row for global x.md:\n%s", w)
		}
		if strings.Contains(w, "|recall|"+native.QKey(proj, "x.md")) {
			t.Errorf("project note must not be recorded as delivered:\n%s", w)
		}
	})
	t.Run("project learning, global note", func(t *testing.T) {
		f := newStoreFixture(t)
		f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
		proj := native.StoreFor(f.claude, "/tmp/proj").Project
		f.globalFact(t, "x", note, "GLOBAL NOTE")
		f.factFM(t, "/tmp/proj", "x", learning, "PROJECT LEARNING")
		out, _, code := runStdin(t, f.env, stdin, "skill-inject")
		if code != 0 {
			t.Fatalf("exit=%d", code)
		}
		ctx := additionalContext(t, out)
		if !strings.Contains(ctx, "PROJECT LEARNING") || strings.Contains(ctx, "GLOBAL NOTE") {
			t.Errorf("want the project learning only:\n%s", ctx)
		}
		w := string(mustReadFile(t, filepath.Join(f.mem, ".wal")))
		if !strings.Contains(w, "|recall|"+native.QKey(proj, "x.md")+"|s1") {
			t.Errorf("missing recall row for project x.md:\n%s", w)
		}
	})
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
