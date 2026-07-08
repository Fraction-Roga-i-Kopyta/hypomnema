package closer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/jsonl"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/memindex"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/pathutil"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/profile"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/wal"
)

// Input is the parsed Stop-hook envelope plus resolved paths.
type Input struct {
	SessionID      string
	CWD            string
	TranscriptPath string
	ClaudeHome     string
	MemoryDir      string
	Today          string
}

// Result reports what close did (for diagnostics).
type Result struct {
	Useful int
	Silent int
	Staled int
}

// Run performs the close path: classify injected memories, emit closing WAL
// events, refresh the sidecar, regenerate the self-profile, decay stale rows.
// Best-effort throughout — a failure in one step never aborts the others.
func Run(in Input) (Result, error) {
	var res Result
	injected := readInjectedSet(in.MemoryDir, in.SessionID)
	names, evidence := slugMeta(in.ClaudeHome, in.CWD)

	sess, sErr := jsonl.ReadSession(in.TranscriptPath)

	// A missing/blank session id must not produce empty-session WAL rows:
	// wal validate (and the CI gate) reject them as corrupt (review H3).
	// Mirror recall's non-empty fallback.
	sid := wal.SanitizeField(in.SessionID)
	if sid == "" {
		sid = "unknown"
	}

	// Classify usefulness ONLY when the transcript was actually readable. A
	// failed read (missing/blocked path, no transcript_path) leaves Text empty,
	// which Classify would score as "every injected fact was silent" —
	// fabricating negative evidence for the whole session (review E4). v1
	// skipped the evidence pass in this case; v2 must too.
	if sErr == nil {
		useful, silent := Classify(injected, names, evidence, sess.Text)
		res.Useful, res.Silent = len(useful), len(silent)
		for _, slug := range useful {
			appendWAL(in.MemoryDir, in.Today, "trigger-useful", slug, sid)
		}
		for _, slug := range silent {
			appendWAL(in.MemoryDir, in.Today, "trigger-silent", slug, sid)
		}
	}
	metrics := fmt.Sprintf("%s|session-metrics|domains:_global_,error_count:%d,tool_calls:%d,duration:%ds|%s",
		in.Today, sess.ToolErrors, sess.ToolCalls, sess.DurationSec, sid)
	wal.Append(in.MemoryDir, metrics, "")
	wal.Append(in.MemoryDir, fmt.Sprintf("%s|session-close|%s|%s", in.Today, sid, sid), "")

	nativeFiles := collectNative(in.ClaudeHome, in.CWD)
	if s, err := sidecar.Open(filepath.Join(in.MemoryDir, ".sidecar.db")); err == nil {
		_ = sidecar.Reproject(s, nativeFiles, filepath.Join(in.MemoryDir, ".wal"), native.Scope(in.CWD))
		if n, derr := s.MarkStale(in.Today); derr == nil {
			res.Staled = n
		}
		s.Close()
	}
	// Regenerate the native MEMORY.md index for this project (best-effort).
	// v2 owns this file; v1 left an orphan with ../../../ links. Project scope
	// only — the global store surfaces via the ranked injection.
	osHome := filepath.Dir(in.ClaudeHome)
	projDir := native.ProjectMemoryDir(osHome, in.CWD)
	if projFiles, lerr := native.List(projDir); lerr == nil && len(projFiles) > 0 {
		_ = memindex.Write(projDir, memindex.Render(projFiles, memindex.DefaultMaxBytes))
	}

	_ = profile.Generate(in.MemoryDir, nativeFiles)

	return res, nil
}

func readInjectedSet(memDir, sessionID string) []string {
	b, err := os.ReadFile(filepath.Join(memDir, ".runtime",
		"injected-"+pathutil.SafeFileName(sessionID)+".list"))
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

func collectNative(claudeHome, cwd string) []native.MemFile {
	return native.Collect(claudeHome, cwd)
}

func slugMeta(claudeHome, cwd string) (names map[string]string, evidence map[string][]string) {
	names = map[string]string{}
	evidence = map[string][]string{}
	for _, f := range collectNative(claudeHome, cwd) {
		names[f.Slug] = f.Name
		if len(f.Evidence) > 0 {
			evidence[f.Slug] = f.Evidence
		}
	}
	return names, evidence
}

func appendWAL(memDir, day, event, slug, sid string) {
	line := fmt.Sprintf("%s|%s|%s|%s", day, event, wal.SanitizeField(slug), sid)
	wal.Append(memDir, line, "")
}
