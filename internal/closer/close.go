// Package closer implements the Stop-hook close path: it classifies injected
// memories as cited (cite-useful) or not (cite-silent) based on explicit
// <cc-memory> citations in the assistant text, and emits the closing WAL
// events, refreshes the sidecar, and decays stale entries.
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
	// Store is the resolved native store. Zero value → resolved from CWD
	// (tests and legacy callers); the memoryctl verb always sets it.
	Store native.Store
	// Subagent marks a SubagentStop close: SessionID is the subagent's
	// observation key and TranscriptPath its own transcript. Only the
	// citation classification and a session-close row run; session-level
	// work (sidecar reprojection, decay, MEMORY.md) is left to the parent's
	// next Stop.
	Subagent bool
}

func (in Input) store() native.Store {
	if in.Store.Dir != "" {
		return in.Store
	}
	return native.StoreFor(in.ClaudeHome, in.CWD)
}

// Result reports what close did (for diagnostics).
type Result struct {
	Useful int
	Silent int
	Staled int
}

// Run performs the close path: classify injected memories, emit closing WAL
// events, refresh the sidecar, decay stale rows, regenerate MEMORY.md.
// Best-effort throughout — a failure in one step never aborts the others.
func Run(in Input) (Result, error) {
	var res Result
	st := in.store()
	injected := readInjectedSet(in.MemoryDir, in.SessionID)
	status := slugStatus(in.ClaudeHome, st)

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
		projectOf := projectBySlug(in.ClaudeHome, st)
		cited := resolveCitations(Citations(sess.Text+"\n\n"+sess.Thinking), projectOf)
		injectedSet := make(map[string]bool, len(injected))
		for _, slug := range injected {
			injectedSet[slug] = true
		}
		globalDir := native.GlobalMemoryDir(filepath.Dir(in.ClaudeHome))
		// Delivery gate: a resolved citation only earns
		// cite-useful if the fact actually reached the model this session —
		// injected (inject + recall + skill-inject, already folded into
		// injected-<sid>.list) or read directly with the Read tool. Without
		// this gate close would credit ANY in-scope filename the model
		// happened to type in a <cc-memory> tag, fabricated, echoed, or
		// name-only, with durable usefulness (effectiveness, candidate
		// graduation) even though the fact was never shown to it.
		var delivered, undelivered []string
		deliveredSet := make(map[string]bool, len(cited))
		for _, slug := range cited {
			if injectedSet[slug] || readDelivered(sess.ReadPaths, slug, st.Dir, globalDir) {
				deliveredSet[slug] = true
				delivered = append(delivered, slug)
			} else {
				undelivered = append(undelivered, slug)
			}
		}
		for _, slug := range delivered {
			appendWAL(in.MemoryDir, in.Today, "cite-useful", qualify(projectOf, slug), sid)
		}
		res.Useful = len(delivered)
		for _, slug := range undelivered {
			// Resolved but undelivered: counts for nothing else — not
			// useful, not candidate-confirmed, and does not arm the
			// cite-silent guard below. Diagnostic only (see docs/EVENTS.md).
			appendWAL(in.MemoryDir, in.Today, "cite-undelivered", qualify(projectOf, slug), sid)
		}
		// Silence is evidence only where the citation channel demonstrably
		// worked this session: with no DELIVERED citation at all, an uncited
		// fact is more likely a lost or skipped citation than a useless
		// fact — and an undelivered citation alone (fabricated/echoed/
		// name-only) must not arm this guard either.
		if len(delivered) > 0 {
			for _, slug := range injected {
				if deliveredSet[slug] {
					continue
				}
				appendWAL(in.MemoryDir, in.Today, "cite-silent", qualify(projectOf, slug), sid)
				res.Silent++
			}
		}
		for _, slug := range delivered {
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
		// A readable transcript with injected facts but no resolvable,
		// DELIVERED citation still closed its classification pass — record
		// that explicitly (session-level, once per session) so doctor's
		// open-quanta check does not read a citation-less session as a lost
		// one. A session with only undelivered citations still writes this
		// (undelivered never arms the guard above).
		if len(delivered) == 0 && len(injected) > 0 {
			wal.AppendSuffixUnique(in.MemoryDir, fmt.Sprintf("%s|cite-none|%s|%s", in.Today, sid, sid), "|cite-none|"+sid+"|"+sid)
		}
	}
	if in.Subagent {
		wal.AppendSuffixUnique(in.MemoryDir, fmt.Sprintf("%s|session-close|%s|%s", in.Today, sid, sid), "|session-close|"+sid+"|"+sid)
		return res, nil
	}
	wal.Append(in.MemoryDir, fmt.Sprintf("%s|session-close|%s|%s", in.Today, sid, sid), "")

	nativeFiles := collectNative(in.ClaudeHome, st)
	if s, err := sidecar.Open(filepath.Join(in.MemoryDir, ".sidecar.db")); err == nil {
		_ = sidecar.Reproject(s, nativeFiles, filepath.Join(in.MemoryDir, ".wal"), st.Scope())
		if n, derr := s.MarkStale(in.Today); derr == nil {
			res.Staled = n
		}
		s.Close()
	}
	// Regenerate the native MEMORY.md index for this project (best-effort).
	// v2 owns this file; v1 left an orphan with ../../../ links.
	projDir := st.Dir
	if projFiles, lerr := native.List(projDir); lerr == nil && len(projFiles) > 0 {
		_ = memindex.Write(projDir, memindex.Render(projFiles, memindex.DefaultMaxBytes))
	}
	// And the global store's own index (absolute links). The harness loads only
	// the project index; the global store surfaces via ranked injection, or —
	// in pull-only mode — via this file imported from the user's CLAUDE.md.
	// A MEMORY.md hypomnema did not generate there is left untouched.
	globalDir := native.GlobalMemoryDir(filepath.Dir(in.ClaudeHome))
	if globalFiles, gerr := native.List(globalDir); gerr == nil && len(globalFiles) > 0 {
		_ = memindex.WriteGlobal(globalDir, memindex.RenderGlobal(globalFiles, memindex.DefaultMaxBytes))
	}

	return res, nil
}

func readInjectedSet(memDir, sessionID string) []string {
	return readRuntimeList(filepath.Join(memDir, ".runtime",
		"injected-"+pathutil.SafeFileName(sessionID)+".list"))
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

func collectNative(claudeHome string, st native.Store) []native.MemFile {
	return native.Collect(claudeHome, st)
}

// slugStatus maps each in-scope fact's slug to its frontmatter status (the
// candidate-graduation check reads it).
func slugStatus(claudeHome string, st native.Store) map[string]string {
	status := map[string]string{}
	for _, f := range collectNative(claudeHome, st) {
		status[f.Slug] = f.Status
	}
	return status
}

// appendWAL writes one classification row, deduplicated per (event, target,
// session): close runs on every Stop, and re-emitting the same verdict each
// turn only inflates the WAL (every reader already collapses rows per
// (slug, session) with useful-wins). A verdict that changes later in the
// session — silent at turn 1, useful at turn 5 — has a different key and
// still lands.
func appendWAL(memDir, day, event, slug, sid string) {
	target := wal.SanitizeField(slug)
	line := fmt.Sprintf("%s|%s|%s|%s", day, event, target, sid)
	wal.AppendSuffixUnique(memDir, line, "|"+event+"|"+target+"|"+sid)
}

// projectBySlug maps each in-scope fact's slug to its owning project, project-
// local winning over global on a basename tie (matches injection preference).
func projectBySlug(claudeHome string, st native.Store) map[string]string {
	out := map[string]string{}
	for _, f := range collectNative(claudeHome, st) {
		if f.Project == st.Project || out[f.Slug] == "" {
			out[f.Slug] = f.Project
		}
	}
	return out
}

// resolveCitations keeps the cited names that are in-scope facts (projectOf
// already prefers the project copy over the global one on a basename tie).
func resolveCitations(names []string, projectOf map[string]string) []string {
	var out []string
	for _, n := range names {
		if _, ok := projectOf[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

// readDelivered reports whether slug (an in-scope fact's basename, e.g.
// "docker.md") was delivered by a direct file read this session: one of
// paths (Session.ReadPaths — every Read tool_use's input.file_path), once
// filepath.Clean'd, has that basename AND sits directly in the resolved
// project store dir or the global store dir. A same-named file read from
// anywhere else does not count — the directory check is what makes this "a
// direct read of THIS fact", not just a file that happens to share a name.
func readDelivered(paths []string, slug, storeDir, globalDir string) bool {
	storeDir = filepath.Clean(storeDir)
	globalDir = filepath.Clean(globalDir)
	for _, p := range paths {
		clean := filepath.Clean(p)
		if filepath.Base(clean) != slug {
			continue
		}
		dir := filepath.Dir(clean)
		if dir == storeDir || dir == globalDir {
			return true
		}
	}
	return false
}

// qualify project-qualifies a slug for the WAL target so classification events
// feed per-project effectiveness (review E5-deep); an unknown project falls
// back to the bare slug (grandfathered on read).
func qualify(projectOf map[string]string, slug string) string {
	if p := projectOf[slug]; p != "" {
		return native.QKey(p, slug)
	}
	return slug
}
