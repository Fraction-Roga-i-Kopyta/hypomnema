package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

func TestInject_SystemPromptSkipped(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	// A session list that already exists must stay byte-for-byte unchanged.
	list := filepath.Join(f.mem, ".runtime", "injected-s1.list")
	os.MkdirAll(filepath.Dir(list), 0o755)
	before := []byte("earlier.md\n")
	os.WriteFile(list, before, 0o600)
	for _, src := range []string{"system", "poll_event"} {
		out, _, code := runStdin(t, f.env,
			`{"session_id":"s1","cwd":"/tmp/proj","prompt":"dockercache","source":"`+src+`"}`,
			"inject", "--event=UserPromptSubmit")
		if code != 0 || strings.TrimSpace(out) != "" {
			t.Errorf("source=%s must emit nothing; code=%d out=%q", src, code, out)
		}
	}
	wal, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	if strings.Contains(string(wal), "|inject|") {
		t.Errorf("system prompts must not write inject rows:\n%s", wal)
	}
	if after, _ := os.ReadFile(list); !bytes.Equal(after, before) {
		t.Errorf("session list changed: %q → %q", before, after)
	}
	// A payload without source still injects (field is rolling out).
	out, _, _ := runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","prompt":"dockercache"}`,
		"inject", "--event=UserPromptSubmit")
	if !strings.Contains(additionalContext(t, out), "docker layer cache") {
		t.Errorf("missing source must behave as a user prompt; got %q", out)
	}
}

// R20: the /memboard toggle (with or without args, any surrounding
// whitespace) is not a prompt: no output, no inject row, the session lists
// untouched, so opening the panel spends no once-per-context slot. Other
// slash prompts — and a prompt that merely starts with the word — still
// inject.
func TestInject_MemboardToggleSkipped(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "memboard", "memboard dashboard pane")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	rt := filepath.Join(f.mem, ".runtime")
	os.MkdirAll(rt, 0o755)
	before := []byte("earlier.md\n")
	for _, name := range []string{"injected-s1.list", "rendered-s1.list"} {
		os.WriteFile(filepath.Join(rt, name), before, 0o600)
	}
	for _, prompt := range []string{"/memboard", "  /memboard  ", `/memboard memboard`, `\t/memboard\n`} {
		out, _, code := runStdin(t, f.env,
			`{"session_id":"s1","cwd":"/tmp/proj","prompt":"`+prompt+`"}`,
			"inject", "--event=UserPromptSubmit")
		if code != 0 || strings.TrimSpace(out) != "" {
			t.Errorf("prompt %q must emit nothing; code=%d out=%q", prompt, code, out)
		}
	}
	wal, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	if strings.Contains(string(wal), "|inject|") {
		t.Errorf("the toggle must not write inject rows:\n%s", wal)
	}
	for _, name := range []string{"injected-s1.list", "rendered-s1.list"} {
		if after, _ := os.ReadFile(filepath.Join(rt, name)); !bytes.Equal(after, before) {
			t.Errorf("%s changed: %q → %q", name, before, after)
		}
	}
	for _, prompt := range []string{"/memboardx memboard", "/review memboard", "memboard /memboard"} {
		out, _, _ := runStdin(t, f.env,
			`{"session_id":"s2","cwd":"/tmp/proj","prompt":"`+prompt+`"}`,
			"inject", "--event=UserPromptSubmit")
		if !strings.Contains(additionalContext(t, out), "memboard dashboard pane") {
			t.Errorf("prompt %q is not the toggle and must inject; got %q", prompt, out)
		}
		os.Remove(filepath.Join(rt, "injected-s2.list"))
		os.Remove(filepath.Join(rt, "rendered-s2.list"))
	}
}

func TestIsMemboardToggle(t *testing.T) {
	for prompt, want := range map[string]bool{
		"/memboard": true, " /memboard ": true, "/memboard\tx": true, "/memboard\n": true,
		"/memboards": false, "memboard": false, "/review": false, "": false, "x /memboard": false,
	} {
		if got := isMemboardToggle(prompt); got != want {
			t.Errorf("isMemboardToggle(%q) = %v, want %v", prompt, got, want)
		}
	}
}

func writeSummaryTranscript(t *testing.T, summary string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s1.jsonl")
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(summary)
	os.WriteFile(p, []byte(`{"type":"system","subtype":"compact_boundary"}`+"\n"+
		`{"type":"user","isCompactSummary":true,"message":{"content":"`+esc+`"}}`+"\n"), 0o644)
	return p
}

func TestInject_CompactReinjectsRankedBySummary(t *testing.T) {
	f := newStoreFixture(t)
	f.factDesc(t, "/tmp/proj", "dockercache", "docker layer cache")
	f.factDesc(t, "/tmp/proj", "pgmigration", "postgres migration lock")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"

	// Earlier in the session both facts were injected.
	runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","prompt":"dockercache pgmigration"}`,
		"inject", "--event=UserPromptSubmit")

	transcript := writeSummaryTranscript(t, "We were fixing the pgmigration postgres lock.")
	out, _, code := runStdin(t, f.env,
		`{"session_id":"s1","cwd":"/tmp/proj","source":"compact","transcript_path":"`+transcript+`"}`,
		"inject", "--event=SessionStart")
	ctx := additionalContext(t, out)
	if code != 0 || !strings.Contains(ctx, "postgres migration lock") {
		t.Fatalf("compact must re-inject despite session dedup; code=%d ctx=%q", code, ctx)
	}
	if d := strings.Index(ctx, "docker layer cache"); d >= 0 && d < strings.Index(ctx, "postgres migration lock") {
		t.Errorf("summary-matching fact must rank first; ctx=%q", ctx)
	}
	// ref_count counts inject rows: a re-render after compaction must not add
	// a second row for a fact already logged this session.
	wal, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	if n := strings.Count(string(wal), "\x1fpgmigration.md|s1"); n != 1 {
		t.Errorf("expected exactly 1 inject row per fact per session, got %d:\n%s", n, wal)
	}
}

func TestInject_CompactLongSummaryDoesNotRewardLongBodies(t *testing.T) {
	f := newStoreFixture(t)
	noise := []string{"handler", "module", "package", "registry", "adapter", "builder", "config", "schema",
		"router", "service", "gateway", "manifest", "pipeline", "artifact", "scheduler", "executor",
		"listener", "resolver", "provider", "factory", "renderer", "tokenizer", "parser", "encoder",
		"decoder", "buffer", "cursor", "session", "profile", "catalog", "ledger", "journal", "snapshot",
		"cluster", "replica", "shard", "bucket", "channel", "socket", "payload"}
	inventory := strings.Repeat(strings.Join(noise, " ")+" ", 8)
	// Long and irrelevant, but its head names two summary terms so it
	// passes the compact gate and competes on ranking alone.
	f.factFM(t, "/tmp/proj", "buildjournal", "name: buildjournal\ndescription: postgres lock journal\ntype: note", inventory)
	f.factDesc(t, "/tmp/proj", "pgmigration", "postgres migration lock")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	// Markdown-styled headers, as in ~half of live summaries.
	summary := strings.Join([]string{
		"Summary:",
		"1. **Primary Request and Intent:**",
		"   Fix the pgmigration postgres lock.",
		"3. **Files and Code Sections:**",
		"   " + inventory,
		"## 8. Current Work",
		"   Retrying pgmigration after the postgres lock timeout.",
	}, "\n")
	transcript := writeSummaryTranscript(t, summary)
	out, _, _ := runStdin(t, f.env,
		`{"session_id":"s2","cwd":"/tmp/proj","source":"compact","transcript_path":"`+transcript+`"}`,
		"inject", "--event=SessionStart")
	ctx := additionalContext(t, out)
	p, j := strings.Index(ctx, "postgres migration lock"), strings.Index(ctx, "## buildjournal")
	if p < 0 || (j >= 0 && j < p) {
		t.Errorf("targeted fact must outrank the long inventory-matching one; ctx=%q", ctx)
	}
}

func TestInject_CompactWithoutSummaryStillInjects(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
	f.factFM(t, "/tmp/proj", "lintrule", "name: lintrule\ntype: feedback", "always run make lint")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","source":"startup"}`,
		"inject", "--event=SessionStart")
	runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","prompt":"dockercache"}`,
		"inject", "--event=UserPromptSubmit")
	transcript := filepath.Join(t.TempDir(), "s1.jsonl")
	os.WriteFile(transcript, []byte(`{"type":"system","subtype":"compact_boundary"}`+"\n"), 0o644)
	out, _, _ := runStdin(t, f.env,
		`{"session_id":"s1","cwd":"/tmp/proj","source":"compact","transcript_path":"`+transcript+`"}`,
		"inject", "--event=SessionStart")
	ctx := additionalContext(t, out)
	if !strings.Contains(ctx, "always run make lint") {
		t.Errorf("no summary yet → the core comes back after compaction; got %q", ctx)
	}
	if strings.Contains(ctx, "docker layer cache") {
		t.Errorf("no summary → no query → no ranked facts; got %q", ctx)
	}
}

func TestInject_ClearReinjects(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
	f.factFM(t, "/tmp/proj", "lintrule", "name: lintrule\ntype: feedback", "always run make lint")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","source":"startup"}`,
		"inject", "--event=SessionStart")
	runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","prompt":"dockercache"}`,
		"inject", "--event=UserPromptSubmit")
	out, _, _ := runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","source":"clear"}`,
		"inject", "--event=SessionStart")
	ctx := additionalContext(t, out)
	if !strings.Contains(ctx, "always run make lint") {
		t.Errorf("/clear dropped the context; the core must come back; got %q", ctx)
	}
	if strings.Contains(ctx, "docker layer cache") {
		t.Errorf("/clear has no query; a non-core fact must not come back; got %q", ctx)
	}
}

func TestInject_ResumeKeepsDedup(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
	f.factFM(t, "/tmp/proj", "lintrule", "name: lintrule\ntype: feedback", "always run make lint")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","source":"startup"}`,
		"inject", "--event=SessionStart")
	runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","prompt":"dockercache"}`,
		"inject", "--event=UserPromptSubmit")
	out, _, _ := runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","source":"resume"}`,
		"inject", "--event=SessionStart")
	ctx := additionalContext(t, out)
	if strings.Contains(ctx, "docker layer cache") || strings.Contains(ctx, "always run make lint") {
		t.Errorf("resume restores the conversation; dedup must still apply: %q", ctx)
	}
}

// A fresh session injects the core only: a fact that merely matches the cwd
// basename stays out.
func TestInject_StartupSourceInjectsCoreOnly(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "proj", "proj layout notes")
	f.factFM(t, "/tmp/proj", "lintrule", "name: lintrule\ntype: feedback", "always run make lint")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	out, _, code := runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","source":"startup"}`,
		"inject", "--event=SessionStart")
	ctx := additionalContext(t, out)
	if code != 0 || !strings.Contains(ctx, "always run make lint") {
		t.Fatalf("the core must inject at startup; code=%d ctx=%q", code, ctx)
	}
	if strings.Contains(ctx, "proj layout notes") {
		t.Errorf("a cwd-matching non-core fact must not inject at startup: %q", ctx)
	}
}

// The compact gate does not count the project root's path components: a
// summary quoting the repo name plus one head term does not pull a fact in;
// two task terms do.
func TestInject_CompactGateExcludesProjectRoot(t *testing.T) {
	f := newStoreFixture(t)
	root := filepath.Join(t.TempDir(), "zebraproj")
	os.MkdirAll(root, 0o755)
	f.factDesc(t, root, "rootword", "zebraproj widget")
	f.factDesc(t, root, "taskwords", "widget gizmo")
	f.env["CLAUDE_PROJECT_DIR"] = root
	transcript := writeSummaryTranscript(t, "We fixed the zebraproj widget and the gizmo.")
	out, _, _ := runStdin(t, f.env,
		`{"session_id":"s1","cwd":"`+root+`","source":"compact","transcript_path":"`+transcript+`"}`,
		"inject", "--event=SessionStart")
	ctx := additionalContext(t, out)
	if !strings.Contains(ctx, "## taskwords") {
		t.Errorf("two task terms in the head pass the gate; ctx=%q", ctx)
	}
	if strings.Contains(ctx, "## rootword") {
		t.Errorf("the project root's name must not count toward the gate; ctx=%q", ctx)
	}
}

func TestInject_RenderDedupUsesCurrentContextNotSessionUnion(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "prealpha", "pre alpha fact")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	rt := filepath.Join(f.mem, ".runtime")
	os.MkdirAll(rt, 0o755)
	// prealpha was delivered earlier in the session (union) …
	os.WriteFile(filepath.Join(rt, "injected-s1.list"), []byte("prealpha.md\n"), 0o600)
	// … but the post-compaction render did not include it.
	os.WriteFile(filepath.Join(rt, "rendered-s1.list"), []byte(""), 0o600)
	out, _, _ := runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","prompt":"prealpha"}`, "inject", "--event=UserPromptSubmit")
	if !strings.Contains(additionalContext(t, out), "pre alpha fact") {
		t.Errorf("a fact outside the current context must be injectable again: %q", out)
	}
	wal, _ := os.ReadFile(filepath.Join(f.mem, ".wal"))
	if strings.Contains(string(wal), "\x1fprealpha.md|s1") {
		t.Errorf("re-offering an already-counted fact must not add an inject row:\n%s", wal)
	}
	rendered, _ := os.ReadFile(filepath.Join(rt, "rendered-s1.list"))
	if !strings.Contains(string(rendered), "prealpha.md") {
		t.Errorf("rendered list must now include prealpha: %q", rendered)
	}
}

func TestInject_CompactResetsRenderedList(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "prealpha", "pre alpha fact")
	f.factDesc(t, "/tmp/proj", "postbeta", "post beta fact")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","prompt":"prealpha postbeta"}`, "inject", "--event=UserPromptSubmit")
	transcript := writeSummaryTranscript(t, "8. Current Work:\n   postbeta beta")
	out, _, _ := runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","source":"compact","transcript_path":"`+transcript+`"}`, "inject", "--event=SessionStart")
	ctx := additionalContext(t, out)
	rendered, _ := os.ReadFile(filepath.Join(f.mem, ".runtime", "rendered-s1.list"))
	got := strings.Fields(string(rendered))
	// The rendered list is exactly what the compact render showed.
	for _, s := range got {
		if !strings.Contains(ctx, strings.TrimSuffix(s, ".md")) {
			t.Errorf("rendered list names %s which the compact render did not show; ctx=%q", s, ctx)
		}
	}
	if len(got) == 0 {
		t.Errorf("compact render must record what it showed")
	}
}

func TestInject_MissingRenderedListFallsBackToUnion(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "prealpha", "pre alpha fact")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	rt := filepath.Join(f.mem, ".runtime")
	os.MkdirAll(rt, 0o755)
	os.WriteFile(filepath.Join(rt, "injected-s1.list"), []byte("prealpha.md\n"), 0o600)
	out, _, _ := runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","prompt":"prealpha"}`, "inject", "--event=UserPromptSubmit")
	if strings.Contains(additionalContext(t, out), "pre alpha fact") {
		t.Errorf("a session that predates the rendered list must keep union dedup: %q", out)
	}
}

// factFM writes a fact with explicit frontmatter lines into projectDir's store.
func (f storeFixture) factFM(t *testing.T, projectDir, slug, fm, body string) {
	t.Helper()
	dir := native.StoreFor(f.claude, projectDir).Dir
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, slug+".md"), []byte("---\n"+fm+"\n---\n"+body+"\n"), 0o644)
}

// factDesc is fact() with the body also as the description, so the head
// names the body's terms (the compact gate reads the head only).
func (f storeFixture) factDesc(t *testing.T, projectDir, name, desc string) {
	t.Helper()
	f.factFM(t, projectDir, name, "name: "+name+"\ndescription: "+desc+"\ntype: mistake\nkeywords: ["+name+"]", desc)
}
