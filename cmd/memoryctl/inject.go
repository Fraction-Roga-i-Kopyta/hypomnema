package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"time"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/inject"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/pathutil"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/wal"
)

type hookStdin struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	Prompt    string `json:"prompt"`
}

func runInject(args []string) {
	event := "SessionStart"
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--event="):
			event = strings.TrimPrefix(a, "--event=")
		case a == "-h" || a == "--help":
			fmt.Print(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "memoryctl inject: unknown flag %q\n", a)
			os.Exit(2)
		}
	}
	if event != "SessionStart" && event != "UserPromptSubmit" {
		event = "SessionStart"
	}
	raw, _ := io.ReadAll(os.Stdin)
	var in hookStdin
	if err := json.Unmarshal(raw, &in); err != nil {
		os.Exit(0) // fail-safe
	}
	already := readInjectedList(in.SessionID)
	res, err := inject.Run(inject.Input{
		Event: event, SessionID: in.SessionID, CWD: in.CWD, Prompt: in.Prompt,
		ClaudeHome: claudeDir(), MemoryDir: memoryDir(), Today: today(), MaxK: 8,
		AlreadyInjected: already,
	})
	if err != nil {
		os.Exit(0)
	}
	if len(res.Injected) > 0 && in.SessionID != "" {
		persistInjected(already, res.Injected, in.SessionID)
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
	b, err := os.ReadFile(sessionListPath(sessionID))
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

func persistInjected(already, slugs []string, sessionID string) {
	day := today()
	sid := wal.SanitizeField(sessionID)
	for _, slug := range slugs {
		line := fmt.Sprintf("%s|inject|%s|%s", day, wal.SanitizeField(slug), sid)
		wal.Append(memoryDir(), line, "")
	}
	writeSessionList(already, slugs, sessionID)
}

// writeSessionList rewrites the session's injected list with the set-union
// of already + slugs. Shared by push (inject) and pull (recall) so push
// dedup and close classification see both delivery paths. Two concurrent
// writers race read→union→write (last writer wins) — pre-existing behaviour,
// acceptable for per-session lists.
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
	_ = os.WriteFile(sessionListPath(sessionID),
		[]byte(strings.Join(union, "\n")+"\n"), 0o600)
}

// pruneRuntimeLists drops session lists untouched for 7 days: a session that
// old will never see another inject or close, so its list is dead weight
// (the live install had accumulated 170+ of them).
func pruneRuntimeLists(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "injected-") || !strings.HasSuffix(name, ".list") {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

func emitEnvelope(event, markdown string) {
	type hookOut struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	}
	env := struct {
		HookSpecificOutput hookOut `json:"hookSpecificOutput"`
	}{HookSpecificOutput: hookOut{HookEventName: event, AdditionalContext: markdown}}
	b, err := json.Marshal(env)
	if err != nil {
		return
	}
	fmt.Println(string(b))
}
