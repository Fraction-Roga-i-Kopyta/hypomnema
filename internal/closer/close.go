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
	names, evidence, status := slugMeta(in.ClaudeHome, in.CWD)

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
		projectOf := projectBySlug(in.ClaudeHome, in.CWD)
		for _, slug := range useful {
			appendWAL(in.MemoryDir, in.Today, "trigger-useful", qualify(projectOf, slug), sid)
		}
		for _, slug := range silent {
			appendWAL(in.MemoryDir, in.Today, "trigger-silent", qualify(projectOf, slug), sid)
		}
		for _, slug := range useful {
			if status[slug] != "candidate" {
				continue
			}
			// Graduation: first useful citation confirms the candidate. The
			// dedupKey makes the WAL record it exactly once per fact —
			// repeats add nothing for the reader (confirmed = seen ≥ 1).
			target := wal.SanitizeField(qualify(projectOf, slug))
			line := fmt.Sprintf("%s|candidate-confirmed|%s|%s", in.Today, target, sid)
			wal.Append(in.MemoryDir, line, "|candidate-confirmed|"+target+"|")
		}
		// Ablation observation: facts withheld this session get the same
		// evidence classification, but the verdict flows to holdout-hit/miss
		// (ablate report), never to trigger events — a fact cannot earn or
		// lose effectiveness in a session where the model never saw it.
		// A fact also present in the injected set was delivered anyway by a
		// pull path (recall/skill-inject) — the model saw it, the
		// observation is contaminated, so it is excluded here.
		holdout := readHoldoutSet(in.MemoryDir, in.SessionID)
		if len(holdout) > 0 {
			wasInjected := make(map[string]bool, len(injected))
			for _, s := range injected {
				wasInjected[s] = true
			}
			clean := holdout[:0]
			for _, s := range holdout {
				if !wasInjected[s] {
					clean = append(clean, s)
				}
			}
			hits, misses := Classify(clean, names, evidence, sess.Text)
			for _, slug := range hits {
				appendWAL(in.MemoryDir, in.Today, "holdout-hit", qualify(projectOf, slug), sid)
			}
			for _, slug := range misses {
				appendWAL(in.MemoryDir, in.Today, "holdout-miss", qualify(projectOf, slug), sid)
			}
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
	return readRuntimeList(filepath.Join(memDir, ".runtime",
		"injected-"+pathutil.SafeFileName(sessionID)+".list"))
}

func readHoldoutSet(memDir, sessionID string) []string {
	return readRuntimeList(filepath.Join(memDir, ".runtime",
		"holdout-"+pathutil.SafeFileName(sessionID)+".list"))
}

func readRuntimeList(path string) []string {
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

func collectNative(claudeHome, cwd string) []native.MemFile {
	return native.Collect(claudeHome, cwd)
}

func slugMeta(claudeHome, cwd string) (names map[string]string, evidence map[string][]string, status map[string]string) {
	names = map[string]string{}
	evidence = map[string][]string{}
	status = map[string]string{}
	for _, f := range collectNative(claudeHome, cwd) {
		names[f.Slug] = f.Name
		status[f.Slug] = f.Status
		if len(f.Evidence) > 0 {
			evidence[f.Slug] = f.Evidence
		}
	}
	return names, evidence, status
}

func appendWAL(memDir, day, event, slug, sid string) {
	line := fmt.Sprintf("%s|%s|%s|%s", day, event, wal.SanitizeField(slug), sid)
	wal.Append(memDir, line, "")
}

// projectBySlug maps each in-scope fact's slug to its owning project, project-
// local winning over global on a basename tie (matches injection preference).
func projectBySlug(claudeHome, cwd string) map[string]string {
	local := native.SlugFromCWD(cwd)
	out := map[string]string{}
	for _, f := range collectNative(claudeHome, cwd) {
		if f.Project == local || out[f.Slug] == "" {
			out[f.Slug] = f.Project
		}
	}
	return out
}

// qualify project-qualifies a slug for the WAL target so trigger events feed
// per-project effectiveness (review E5-deep); an unknown project falls back to
// the bare slug (grandfathered on read).
func qualify(projectOf map[string]string, slug string) string {
	if p := projectOf[slug]; p != "" {
		return native.QKey(p, slug)
	}
	return slug
}
