package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"time"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/inject"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/jsonl"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/pathutil"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/wal"
)

type hookStdin struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	Prompt         string `json:"prompt"`
	Source         string `json:"source"`
	TranscriptPath string `json:"transcript_path"`
}

// skipPromptSource reports machine-injected turns (task notifications, peer
// and agent hand-backs, auto-continuation, poll events). Ranking their text
// injects noise and spends facts' once-per-session slot. A payload without
// source (field still rolling out) is treated as a user prompt.
func skipPromptSource(src string) bool { return src == "system" || src == "poll_event" }

// contextWiped reports a SessionStart after which previously injected facts
// are no longer in the model's context (compaction summarized them away, or
// /clear dropped them), so render-dedup must not suppress them.
func contextWiped(event, src string) bool {
	return event == "SessionStart" && (src == "compact" || src == "clear")
}

func runInject(args []string) {
	event := "SessionStart"
	subagent := false
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--event="):
			event = strings.TrimPrefix(a, "--event=")
		case a == "--subagent":
			// Forces the SubagentStart path regardless of --event=. A
			// memoryctl built before subagent support does not recognize
			// this flag and exits 2 (unknown flag, below) before reading
			// stdin — that refusal is what lets a pre-subagent shim detect
			// and swallow an incompatible binary instead of silently
			// treating the subagent's envelope as the parent's SessionStart.
			subagent = true
		case a == "-h" || a == "--help":
			fmt.Print(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "memoryctl inject: unknown flag %q\n", a)
			os.Exit(2)
		}
	}
	if subagent {
		event = "SubagentStart"
	}
	if event != "SessionStart" && event != "UserPromptSubmit" && event != "SubagentStart" {
		event = "SessionStart"
	}
	raw, _ := io.ReadAll(os.Stdin)
	if event == "SubagentStart" {
		runSubagentStart(raw)
		os.Exit(0)
	}
	var in hookStdin
	if err := json.Unmarshal(raw, &in); err != nil {
		os.Exit(0) // fail-safe
	}
	if event == "UserPromptSubmit" && skipPromptSource(in.Source) {
		os.Exit(0) // no output, no WAL, session list untouched
	}
	r := resolveStore(in.CWD)
	if event == "SessionStart" && in.SessionID != "" && (r.Kind == anchorHarness || r.Kind == anchorExplicit) {
		writePin(in.SessionID, r)
	}
	already := readInjectedList(in.SessionID)
	// The rendered list — what is in the model's CURRENT context — is the
	// dedup source of truth: a fact the session union remembers delivering
	// but a later compaction/clear render dropped is no longer in context
	// and must be re-offerable. Sessions that predate the rendered list
	// (readRenderedList ok=false) fall back to the old union dedup.
	renderDedup, prompt := already, in.Prompt
	if rl, ok := readRenderedList(in.SessionID); ok {
		renderDedup = rl
	}
	if contextWiped(event, in.Source) {
		renderDedup = nil
		if in.Source == "compact" {
			if s, ok := jsonl.LastCompactSummary(in.TranscriptPath); ok {
				prompt = inject.CompactQuery(s) // bounded: focus sections, top-40 terms
			}
		}
	}
	res, err := inject.Run(inject.Input{
		Event: event, SessionID: in.SessionID, CWD: in.CWD, Prompt: prompt,
		ClaudeHome: claudeDir(), MemoryDir: memoryDir(), Today: today(), MaxK: 8,
		Store:           r.Store,
		AlreadyInjected: renderDedup,
		HoldoutSession:  readListFile(holdoutListPath(in.SessionID)),
	})
	if err != nil {
		os.Exit(0)
	}
	if len(res.Injected) > 0 && in.SessionID != "" {
		persistInjected(already, res.Injected, res.ProjectBySlug, in.SessionID)
	}
	if in.SessionID != "" {
		persistHoldoutSkips(res, in.SessionID)
		// compact/clear reset renderDedup to nil above, so the union below
		// collapses to exactly res.Injected — matching "the rendered list is
		// what this render showed, nothing carried over from before the wipe".
		writeRenderedList(renderDedup, res.Injected, in.SessionID)
	}
	emitEnvelope(event, res.Markdown)
	os.Exit(0)
}

func sessionListPath(sessionID string) string {
	return filepath.Join(memoryDir(), ".runtime",
		"injected-"+pathutil.SafeFileName(sessionID)+".list")
}

// readInjectedList returns the slugs already injected this session, so the
// ranker never re-renders them (and close classifies the whole session's
// set, not just the last batch).
func readInjectedList(sessionID string) []string {
	if sessionID == "" {
		return nil
	}
	return readListFile(sessionListPath(sessionID))
}

// readListFile reads a newline-delimited slug list; a missing file is nil.
func readListFile(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, ln := range strings.Split(string(b), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			out = append(out, ln)
		}
	}
	return out
}

func renderedListPath(sessionID string) string {
	return filepath.Join(memoryDir(), ".runtime",
		"rendered-"+pathutil.SafeFileName(sessionID)+".list")
}

// readRenderedList returns the slugs in the model's CURRENT context (the
// most recent render this session), distinguishing "no rendered list yet"
// (ok=false — a session that predates this file, or has not rendered at
// all) from "the last render showed nothing" (ok=true, nil).
func readRenderedList(sessionID string) (list []string, ok bool) {
	if sessionID == "" {
		return nil, false
	}
	b, err := os.ReadFile(renderedListPath(sessionID))
	if err != nil {
		return nil, false
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			list = append(list, ln)
		}
	}
	return list, true
}

// writeRenderedList rewrites the session's rendered list with the set-union
// of current + add. Called after every render (inject) and every pull
// delivery (recall/skill-inject) so the file always reflects what is
// actually in the model's context right now. On compact/clear, callers pass
// current=nil so the result collapses to exactly what the new render showed.
func writeRenderedList(current, add []string, sessionID string) {
	if sessionID == "" {
		return
	}
	if err := os.MkdirAll(filepath.Join(memoryDir(), ".runtime"), 0o755); err != nil {
		return
	}
	seen := make(map[string]bool, len(current)+len(add))
	union := append(make([]string, 0, len(current)+len(add)), current...)
	for _, s := range current {
		seen[s] = true
	}
	for _, s := range add {
		if !seen[s] {
			union = append(union, s)
			seen[s] = true
		}
	}
	content := ""
	if len(union) > 0 {
		content = strings.Join(union, "\n") + "\n"
	}
	_ = pathutil.WriteFileAtomic(renderedListPath(sessionID), []byte(content), 0o600)
}

func holdoutListPath(sessionID string) string {
	return filepath.Join(memoryDir(), ".runtime",
		"holdout-"+pathutil.SafeFileName(sessionID)+".list")
}

// persistHoldoutSkips records each held-out fact's would-have-injected
// observation: one holdout-skip WAL event per fact per session (the runtime
// list dedups repeat prompts), plus the session list the close hook reads to
// classify behaviour-without-injection. The final observation session also
// closes the experiment with ablate-stop:expired.
func persistHoldoutSkips(res inject.Result, sessionID string) {
	if len(res.HoldoutSkipped) == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Join(memoryDir(), ".runtime"), 0o755); err != nil {
		return // fail-safe: no list means a repeat emission at worst
	}
	union := readListFile(holdoutListPath(sessionID))
	already := make(map[string]bool, len(union))
	for _, s := range union {
		already[s] = true
	}
	day, sid := today(), wal.SanitizeField(sessionID)
	for _, slug := range res.HoldoutSkipped {
		if already[slug] {
			continue
		}
		target := wal.SanitizeField(slug)
		if p := res.ProjectBySlug[slug]; p != "" {
			target = native.QKey(p, target)
		}
		wal.Append(memoryDir(), fmt.Sprintf("%s|holdout-skip|%s|%s", day, target, sid), "")
		if res.HoldoutRemaining[slug] == 1 {
			// This was the last budgeted observation — close the experiment.
			wal.Append(memoryDir(), fmt.Sprintf("%s|ablate-stop|%s:expired|%s", day, target, sid), "")
		}
		union = append(union, slug)
	}
	if len(union) > 0 {
		_ = pathutil.WriteFileAtomic(holdoutListPath(sessionID),
			[]byte(strings.Join(union, "\n")+"\n"), 0o600)
	}
}

func persistInjected(already, slugs []string, projectBySlug map[string]string, sessionID string) {
	day := today()
	sid := wal.SanitizeField(sessionID)
	logged := make(map[string]bool, len(already))
	for _, s := range already {
		logged[s] = true
	}
	for _, slug := range slugs {
		// Re-rendered after compaction/clear: already counted for this
		// session — another row would inflate ref_count (it counts rows).
		if logged[slug] {
			continue
		}
		// Project-qualify the WAL target so per-project effectiveness doesn't
		// merge across same-basename facts (review E5-deep). Fall back to the
		// bare slug if the project is unknown (grandfathered on read).
		target := wal.SanitizeField(slug)
		if p := projectBySlug[slug]; p != "" {
			target = native.QKey(p, target)
		}
		line := fmt.Sprintf("%s|inject|%s|%s", day, target, sid)
		wal.Append(memoryDir(), line, "")
	}
	writeSessionList(already, slugs, sessionID)
}

// writeSessionList rewrites the session's injected list with the set-union
// of already + slugs. Shared by push (inject) and pull (recall) so push
// dedup and close classification see both delivery paths. The write is atomic
// (tmp+rename) so the Stop-hook reader never sees a truncated/empty list mid-
// write — a torn read there means a whole turn's trigger classification is
// lost (review C4). Two concurrent writers still race read→union→write (last
// writer wins), acceptable for per-session lists.
func writeSessionList(already, slugs []string, sessionID string) {
	runtimeDir := filepath.Join(memoryDir(), ".runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return
	}
	pruneRuntimeLists(runtimeDir)
	seen := make(map[string]bool, len(already))
	union := append(make([]string, 0, len(already)+len(slugs)), already...)
	for _, s := range already {
		seen[s] = true
	}
	for _, s := range slugs {
		if !seen[s] {
			union = append(union, s)
			seen[s] = true
		}
	}
	_ = pathutil.WriteFileAtomic(sessionListPath(sessionID),
		[]byte(strings.Join(union, "\n")+"\n"), 0o600)
}

// pruneRuntimeLists drops session lists untouched for 7 days: a session that
// old will never see another inject or close, so its list is dead weight
// (the live install had accumulated 170+ of them). agentcall-*.claim marks
// (pickAgentCall) age out on the same cutoff — a launching call that old has
// long since been superseded by a fresh transcript tail.
func pruneRuntimeLists(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, e := range entries {
		name := e.Name()
		isList := (strings.HasPrefix(name, "injected-") || strings.HasPrefix(name, "holdout-") ||
			strings.HasPrefix(name, "rendered-")) &&
			strings.HasSuffix(name, ".list")
		isPin := strings.HasPrefix(name, "project-") && strings.HasSuffix(name, ".json")
		isClaim := strings.HasPrefix(name, "agentcall-") && strings.HasSuffix(name, ".claim")
		if !isList && !isPin && !isClaim {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// marshalEnvelope builds the hookSpecificOutput JSON with HTML-escaping OFF.
// Default json.Marshal rewrites <,>,& to </>/& (6 bytes each);
// on code/markup-heavy facts that inflates the envelope ~2x past the markdown
// byte count the injection budget measures, so Claude Code diverts the payload
// to a file the model never reads inline — defeating the budget (review H1).
func marshalEnvelope(event, markdown string) ([]byte, error) {
	type hookOut struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	}
	env := struct {
		HookSpecificOutput hookOut `json:"hookSpecificOutput"`
	}{HookSpecificOutput: hookOut{HookEventName: event, AdditionalContext: markdown}}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(env); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func emitEnvelope(event, markdown string) {
	b, err := marshalEnvelope(event, markdown)
	if err != nil {
		return
	}
	fmt.Println(string(b))
}
