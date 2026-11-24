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
	// ConfigDir is Claude Code's config dir (projects/ lives there; differs
	// from ClaudeHome under CLAUDE_CONFIG_DIR). "" → ClaudeHome. It locates
	// every project's store for the mark table's file checks.
	ConfigDir string
	MemoryDir string
	Today     string
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

// storeDirs maps every reachable store tag to its directory for
// sidecar.ReprojectAll's mark checks.
func (in Input) storeDirs(st native.Store) map[string]string {
	cfg := in.ConfigDir
	if cfg == "" {
		cfg = in.ClaudeHome
	}
	return native.StoreDirs(cfg, in.ClaudeHome, st)
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
	files := collectNative(in.ClaudeHome, st)
	byName := factsBySlug(files, st.Project)
	injected := readInjectedSet(in.MemoryDir, in.SessionID, st.Project, byName)
	status := slugStatus(files)

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
		cited := resolveCitations(Citations(sess.Text+"\n\n"+sess.Thinking), byName)
		injectedSet := make(map[string]bool, len(injected))
		for _, k := range injected {
			injectedSet[k] = true
		}
		// Delivery gate: a resolved citation only earns
		// cite-useful if the fact actually reached the model this session —
		// injected (inject + recall + skill-inject, already folded into
		// injected-<sid>.list) or read directly with the Read tool. Without
		// this gate close would credit ANY in-scope filename the model
		// happened to type in a <cc-memory> tag, fabricated, echoed, or
		// name-only, with durable usefulness (effectiveness, candidate
		// graduation) even though the fact was never shown to it.
		//
		// The model cites by file name, and a project fact and a global fact
		// may share one. The name credits every fact with that name that was
		// delivered this session (one, or both of a collision pair) — never a
		// same-named fact that was not. Only when none was delivered does the
		// name fall back to the project-local copy, as cite-undelivered.
		var delivered, undelivered []string
		deliveredSet := make(map[string]bool, len(cited))
		for _, name := range cited {
			hit := false
			for _, f := range byName[name] {
				k := native.QKey(f.Project, f.Slug)
				if !injectedSet[k] && !readDelivered(sess.ReadPaths, f.Path) {
					continue
				}
				hit = true
				if !deliveredSet[k] {
					deliveredSet[k] = true
					delivered = append(delivered, k)
				}
			}
			if !hit {
				f := byName[name][0]
				undelivered = append(undelivered, native.QKey(f.Project, f.Slug))
			}
		}
		for _, k := range delivered {
			appendWAL(in.MemoryDir, in.Today, "cite-useful", k, sid)
		}
		res.Useful = len(delivered)
		for _, k := range undelivered {
			// Resolved but undelivered: counts for nothing else — not
			// useful, not candidate-confirmed, and does not arm the
			// cite-silent guard below. Diagnostic only (see docs/EVENTS.md).
			appendWAL(in.MemoryDir, in.Today, "cite-undelivered", k, sid)
		}
		// Silence is evidence only where the citation channel demonstrably
		// worked this session: with no DELIVERED citation at all, an uncited
		// fact is more likely a lost or skipped citation than a useless
		// fact — and an undelivered citation alone (fabricated/echoed/
		// name-only) must not arm this guard either.
		if len(delivered) > 0 {
			for _, k := range injected {
				if deliveredSet[k] {
					continue
				}
				appendWAL(in.MemoryDir, in.Today, "cite-silent", k, sid)
				res.Silent++
			}
		}
		for _, k := range delivered {
			if status[k] != "candidate" {
				continue
			}
			// Graduation: first useful citation confirms the candidate — the
			// credited fact's own status, not a same-named fact's. The
			// dedupKey makes the WAL record it exactly once per fact —
			// repeats add nothing for the reader (confirmed = seen ≥ 1).
			target := wal.SanitizeField(k)
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

	if s, err := sidecar.Open(filepath.Join(in.MemoryDir, ".sidecar.db")); err == nil {
		_ = sidecar.ReprojectAll(s, files, filepath.Join(in.MemoryDir, ".wal"), st.Scope(), in.storeDirs(st))
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

// readInjectedSet returns the facts delivered this session as QKeys, once
// each, in list order. Lines are read by native.KeySet's rule: a QKey line
// names one fact; a bare slug line (written by an older binary) names that
// file in every store, so it expands to every in-scope fact with that name —
// or stays bare when none is in scope (its rows then carry the bare slug,
// grandfathered on read, as before). A QKey line of a project other than the
// current store's (or the global store) is ignored.
func readInjectedSet(memDir, sessionID, project string, byName map[string][]native.MemFile) []string {
	lines := readRuntimeList(filepath.Join(memDir, ".runtime",
		"injected-"+pathutil.SafeFileName(sessionID)+".list"))
	seen := make(map[string]bool, len(lines))
	var out []string
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, ln := range lines {
		p, slug, qualified := native.ParseQKey(ln)
		switch {
		case slug == "":
		case qualified && p != project && p != native.GlobalProject:
			// Another project's fact: not in this store's scope.
		case qualified:
			add(ln)
		case len(byName[slug]) == 0:
			add(slug)
		default:
			for _, f := range byName[slug] {
				add(native.QKey(f.Project, f.Slug))
			}
		}
	}
	return out
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

// slugStatus maps each in-scope fact's QKey to its frontmatter status (the
// candidate-graduation check reads the credited fact's own status).
func slugStatus(files []native.MemFile) map[string]string {
	status := make(map[string]string, len(files))
	for _, f := range files {
		status[native.QKey(f.Project, f.Slug)] = f.Status
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

// factsBySlug groups the in-scope facts by file name, the project-local copy
// first — injection's preference on a basename tie, and the attribution of a
// citation none of whose facts was delivered (cite-undelivered).
func factsBySlug(files []native.MemFile, project string) map[string][]native.MemFile {
	out := map[string][]native.MemFile{}
	for _, f := range files {
		if f.Project == project {
			out[f.Slug] = append([]native.MemFile{f}, out[f.Slug]...)
		} else {
			out[f.Slug] = append(out[f.Slug], f)
		}
	}
	return out
}

// resolveCitations keeps the cited names that name an in-scope fact.
func resolveCitations(names []string, byName map[string][]native.MemFile) []string {
	var out []string
	for _, n := range names {
		if len(byName[n]) > 0 {
			out = append(out, n)
		}
	}
	return out
}

// readDelivered reports whether the fact stored at factPath (an in-scope
// fact's file, directly in the project store or the global store) was
// delivered by a direct file read this session: one of paths
// (Session.ReadPaths — every Read tool_use's input.file_path), once
// filepath.Clean'd, is that file. A same-named file read from anywhere else —
// another directory, or the other store of a same-named pair — does not
// count: the path check is what makes this "a direct read of THIS fact", not
// just a file that happens to share a name.
func readDelivered(paths []string, factPath string) bool {
	want := filepath.Clean(factPath)
	for _, p := range paths {
		if filepath.Clean(p) == want {
			return true
		}
	}
	return false
}
