package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
	f.fact(t, "/tmp/proj", "pgmigration", "postgres migration lock")
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
	f.fact(t, "/tmp/proj", "buildjournal", inventory) // long, irrelevant
	f.fact(t, "/tmp/proj", "pgmigration", "postgres migration lock")
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
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","prompt":"dockercache"}`,
		"inject", "--event=UserPromptSubmit")
	transcript := filepath.Join(t.TempDir(), "s1.jsonl")
	os.WriteFile(transcript, []byte(`{"type":"system","subtype":"compact_boundary"}`+"\n"), 0o644)
	out, _, _ := runStdin(t, f.env,
		`{"session_id":"s1","cwd":"/tmp/proj","source":"compact","transcript_path":"`+transcript+`"}`,
		"inject", "--event=SessionStart")
	if !strings.Contains(additionalContext(t, out), "docker layer cache") {
		t.Errorf("no summary yet → still re-inject on git/cwd context; got %q", out)
	}
}

func TestInject_ClearReinjects(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","prompt":"dockercache"}`,
		"inject", "--event=UserPromptSubmit")
	out, _, _ := runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","source":"clear"}`,
		"inject", "--event=SessionStart")
	if !strings.Contains(additionalContext(t, out), "docker layer cache") {
		t.Errorf("/clear dropped the context; the fact must come back; got %q", out)
	}
}

func TestInject_ResumeKeepsDedup(t *testing.T) {
	f := newStoreFixture(t)
	f.fact(t, "/tmp/proj", "dockercache", "docker layer cache")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","prompt":"dockercache"}`,
		"inject", "--event=UserPromptSubmit")
	out, _, _ := runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","source":"resume"}`,
		"inject", "--event=SessionStart")
	if strings.Contains(additionalContext(t, out), "docker layer cache") {
		t.Errorf("resume restores the conversation; dedup must still apply: %q", out)
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
	f.fact(t, "/tmp/proj", "postbeta", "post beta fact")
	f.env["CLAUDE_PROJECT_DIR"] = "/tmp/proj"
	runStdin(t, f.env, `{"session_id":"s1","cwd":"/tmp/proj","prompt":"prealpha postbeta"}`, "inject", "--event=UserPromptSubmit")
	transcript := writeSummaryTranscript(t, "8. Current Work:\n   postbeta")
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
