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
	"unicode"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/inject"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/pathutil"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/wal"
)

type hookStdin struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	Prompt    string `json:"prompt"`
	Source    string `json:"source"`
}

// skipPromptSource reports machine-injected turns (task notifications, peer
// and agent hand-backs, auto-continuation, poll events). Ranking their text
// injects noise and spends facts' once-per-session slot. A payload without
// source (field still rolling out) is treated as a user prompt.
func skipPromptSource(src string) bool { return src == "system" || src == "poll_event" }

// isMemboardToggle reports the /memboard panel toggle (optionally with
// arguments): it opens or closes a pane and never reaches the model, so
// ranking it would spend facts' once-per-context slot and inflate the
// injected/silent counts. Other slash prompts are ordinary prompts.
func isMemboardToggle(prompt string) bool {
	rest, ok := strings.CutPrefix(strings.TrimSpace(prompt), "/memboard")
	return ok && (rest == "" || unicode.IsSpace(rune(rest[0])))
}

// promptInjectOff reports the pull-only mode (HYPOMNEMA_PROMPT_INJECT=off):
// UserPromptSubmit injects nothing, and memory reaches the model through the
// harness-loaded MEMORY.md index (plus the global index imported from
// CLAUDE.md) and the model's own reads — Claude Code's own design — instead
// of a per-prompt lexical push. SessionStart and SubagentStart are unchanged.
func promptInjectOff() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("HYPOMNEMA_PROMPT_INJECT")), "off")
}

// contextWiped reports a SessionStart after which previously injected facts
// are no longer in the model's context (compaction summarized them away, or
// /clear dropped them), so render-dedup must not suppress them.
func contextWiped(event, src string) bool {
	return event == "SessionStart" && (src == "compact" || src == "clear")
}

// hookVerbs are the entry points the hook shims call. main dispatches them
// through runHook, so a panic in any of them is recovered (a verb absent here
// is not fail-safe). Go exits 2 on an unrecovered panic; exit 2 blocks
// UserPromptSubmit / PreToolUse and is noise on Stop (I5). A deliberate
// os.Exit(2) from guard/guard-bash is not a panic and still blocks.
var hookVerbs = map[string]func([]string){
	"inject":       runInject, // SessionStart, UserPromptSubmit, SubagentStart
	"close":        runClose,  // Stop, and SubagentStop via --subagent
	"guard":        runGuard,
	"guard-bash":   runGuardBash,
	"skill-inject": runSkillInject,
	"skill-active": runSkillActive,
}

// failSafe runs f and reports whether it panicked, swallowing the panic.
func failSafe(f func()) bool {
	return failSafeTrace("", io.Discard, f)
}

// failSafeTrace is failSafe plus a one-line stderr trace
// "memoryctl <verb>: recovered panic: <value>" on recovery.
func failSafeTrace(verb string, stderr io.Writer, f func()) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			fmt.Fprintf(stderr, "memoryctl %s: recovered panic: %v\n", verb, r)
		}
	}()
	f()
	return false
}

// runHook runs a hook verb under failSafeTrace; after a panic it exits 0 with
// no stdout (a panicking guard therefore fails open, like a missing binary).
func runHook(verb string, stderr io.Writer, f func()) {
	if failSafeTrace(verb, stderr, f) {
		os.Exit(0)
	}
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
	if event == "UserPromptSubmit" && (skipPromptSource(in.Source) || promptInjectOff() || isMemboardToggle(in.Prompt)) {
		os.Exit(0) // no output, no WAL, session list untouched
	}
	r := resolveStore(in.CWD)
	if event == "SessionStart" && in.SessionID != "" && (r.Kind == anchorHarness || r.Kind == anchorExplicit) {
		writePin(in.SessionID, r)
	}
	already := readInjectedList(in.SessionID)
	rl, rlOK := readRenderedList(in.SessionID)
	if hasBareLine(already) || hasBareLine(rl) {
		// Lists from an older binary: qualify their bare lines once, so the
		// writes below leave QKeys only.
		files := native.Collect(claudeDir(), r.Store)
		already, rl = qualifyBareLines(already, files), qualifyBareLines(rl, files)
	}
	// The rendered list — what is in the model's CURRENT context — is the
	// dedup source of truth: a fact the session union remembers delivering
	// but a later compaction/clear render dropped is no longer in context
	// and must be re-offerable. Sessions that predate the rendered list
	// (readRenderedList ok=false) fall back to the old union dedup.
	renderDedup := already
	if rlOK {
		renderDedup = rl
	}
	if contextWiped(event, in.Source) {
		// SessionStart injects the core only, after compaction too: the
		// summary carries the task state, anything else is pulled.
		renderDedup = nil
	}
	res, err := inject.Run(inject.Input{
		Event: event, Source: in.Source,
		SessionID: in.SessionID, CWD: in.CWD, Prompt: in.Prompt,
		ClaudeHome: claudeDir(), ConfigDir: configDir(), MemoryDir: memoryDir(), Today: today(), MaxK: 8,
		Store:           r.Store,
		AlreadyInjected: renderDedup,
	})
	if err != nil {
		os.Exit(0)
	}
	if len(res.InjectedKeys) > 0 && in.SessionID != "" {
		persistInjected(already, res.InjectedKeys, in.SessionID)
	}
	if in.SessionID != "" {
		// compact/clear reset renderDedup to nil above, so the union below
		// collapses to exactly res.InjectedKeys — matching "the rendered list
		// is what this render showed, nothing carried over from before the
		// wipe".
		writeRenderedList(renderDedup, res.InjectedKeys, in.SessionID)
	}
	emitEnvelope(event, res.Markdown)
	os.Exit(0)
}

func sessionListPath(sessionID string) string {
	return filepath.Join(memoryDir(), ".runtime",
		"injected-"+pathutil.SafeFileName(sessionID)+".list")
}

// Session lists (injected-<sid>.list, rendered-<sid>.list) hold one fact
// per line as native.QKey(project, slug): a project and a global fact with
// the same file name are two facts. A bare slug line was written by an
// older binary; readers treat it as that file name in every store
// (native.KeySet). inject rewrites it as QKeys (qualifyBareLines) only when
// it next writes that list; a bare line with no in-scope fact stays bare.

// readInjectedList returns the facts already injected this session, so the
// ranker never re-renders them (and close classifies the whole session's
// set, not just the last batch).
func readInjectedList(sessionID string) []string {
	if sessionID == "" {
		return nil
	}
	return readListFile(sessionListPath(sessionID))
}

// readListFile reads a newline-delimited session list; a missing file is nil.
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

// readRenderedList returns the facts in the model's CURRENT context (the
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

// persistInjected writes one WAL inject row per newly delivered fact and
// unions keys (QKeys) into the session's injected list. Dedup is per fact:
// project x.md delivered after global x.md gets its own row; a bare line in
// already (older binary) still covers that file name in every store.
func persistInjected(already, keys []string, sessionID string) {
	day := today()
	sid := wal.SanitizeField(sessionID)
	logged := native.NewKeySet(already)
	for _, key := range keys {
		project, slug, qualified := native.ParseQKey(key)
		// Re-rendered after compaction/clear: already counted for this
		// session — another row would inflate ref_count (it counts rows).
		if logged.Has(project, slug) {
			continue
		}
		logged.Add(key)
		// Project-qualified WAL target so per-project effectiveness doesn't
		// merge across same-basename facts (review E5-deep). A key without
		// a project falls back to the bare slug (grandfathered on read).
		target := wal.SanitizeField(slug)
		if qualified && project != "" {
			target = native.QKey(project, target)
		}
		line := fmt.Sprintf("%s|inject|%s|%s", day, target, sid)
		wal.Append(memoryDir(), line, "")
	}
	writeSessionList(already, keys, sessionID)
}

// hasBareLine reports whether a session list carries a line from an older
// binary (a slug without its project).
func hasBareLine(lines []string) bool {
	for _, ln := range lines {
		if _, _, qualified := native.ParseQKey(ln); !qualified {
			return true
		}
	}
	return false
}

// qualifyBareLines rewrites each bare slug line as the QKeys of every
// in-scope fact with that file name — the same "this file name in any
// store" reading the bare line had, now in the list's current form. A bare
// slug with no in-scope fact stays as is (nothing to qualify it with; it
// can match nothing in this scope either).
func qualifyBareLines(lines []string, files []native.MemFile) []string {
	if !hasBareLine(lines) {
		return lines
	}
	bySlug := make(map[string][]string, len(files))
	for _, f := range files {
		bySlug[f.Slug] = append(bySlug[f.Slug], native.QKey(f.Project, f.Slug))
	}
	seen := make(map[string]bool, len(lines))
	out := make([]string, 0, len(lines))
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, ln := range lines {
		_, slug, qualified := native.ParseQKey(ln)
		if keys := bySlug[slug]; !qualified && len(keys) > 0 {
			for _, k := range keys {
				add(k)
			}
			continue
		}
		add(ln)
	}
	return out
}

// writeSessionList rewrites the session's injected list with the set-union
// of already + keys (session-list lines, see readInjectedList). Shared by
// push (inject) and pull (recall) so push
// dedup and close classification see both delivery paths. The write is atomic
// (tmp+rename) so the Stop-hook reader never sees a truncated/empty list mid-
// write — a torn read there means a whole turn's trigger classification is
// lost (review C4). Two concurrent writers still race read→union→write (last
// writer wins), acceptable for per-session lists.
func writeSessionList(already, keys []string, sessionID string) {
	runtimeDir := filepath.Join(memoryDir(), ".runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return
	}
	pruneRuntimeLists(runtimeDir)
	seen := make(map[string]bool, len(already))
	union := append(make([]string, 0, len(already)+len(keys)), already...)
	for _, s := range already {
		seen[s] = true
	}
	for _, s := range keys {
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
// long since been superseded by a fresh transcript tail. holdout-*.list files
// are left over from the removed ablation machinery and age out the same way.
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
