// Package doctor collects on-demand health diagnostics for a hypomnema
// install. It complements the SessionStart health-check block (which runs
// only at session start and only for three conditions) with a broader
// inspection an operator can invoke any time: installation sanity, corpus
// counts, recent WAL errors, sidecar freshness.
//
// The package deliberately does NOT touch memory files or WAL state — it
// is read-only. The Run function returns a Report the caller renders as
// text or JSON; no network, no writes.
package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// Status is the three-level health grade for each check.
type Status int

const (
	// OK — check passed; no action required.
	OK Status = iota
	// WARN — degraded but functional (e.g. optional binary missing, stale index).
	WARN
	// FAIL — broken; hypomnema is partially or fully non-functional.
	FAIL
)

func (s Status) String() string {
	switch s {
	case OK:
		return "OK"
	case WARN:
		return "WARN"
	case FAIL:
		return "FAIL"
	}
	return "?"
}

// Check is a single diagnostic result. Extra carries structured
// context that shells can parse (see `corpus_frontmatter_quality`,
// which exposes empty-status counts and sample filenames instead of
// packing them into Detail); omitted when empty to keep the JSON output
// stable for checks that don't need it.
type Check struct {
	Name   string                 `json:"name"`
	Status Status                 `json:"status"`
	Detail string                 `json:"detail,omitempty"`
	Extra  map[string]interface{} `json:"extra,omitempty"`
}

// Report is the aggregated set of checks returned by Run.
type Report struct {
	MemoryDir string  `json:"memory_dir"`
	ClaudeDir string  `json:"claude_dir"`
	Checks    []Check `json:"checks"`
	Now       time.Time
}

// Counts summarises per-check totals.
func (r Report) Counts() (ok, warn, fail int) {
	for _, c := range r.Checks {
		switch c.Status {
		case OK:
			ok++
		case WARN:
			warn++
		case FAIL:
			fail++
		}
	}
	return
}

// ExitCode translates failure severity to a shell exit: 0 if no FAIL,
// 1 if any FAIL. WARN does not bump the code — operators poll doctor in
// scripts and shouldn't get a non-zero just because an index went stale.
func (r Report) ExitCode() int {
	_, _, fail := r.Counts()
	if fail > 0 {
		return 1
	}
	return 0
}

// Print renders the report as plain text. Column widths are fixed so the
// output stays readable through a terminal pager.
func (r Report) Print(w io.Writer) {
	fmt.Fprintf(w, "hypomnema doctor — %s\n", r.Now.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(w, "  claude_dir: %s\n", r.ClaudeDir)
	fmt.Fprintf(w, "  memory_dir: %s\n", r.MemoryDir)
	fmt.Fprintln(w)
	for _, c := range r.Checks {
		fmt.Fprintf(w, "  [%-4s] %-32s %s\n", c.Status, c.Name, c.Detail)
	}
	ok, warn, fail := r.Counts()
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Summary: %d OK / %d WARN / %d FAIL\n", ok, warn, fail)
}

// PrintJSON is the machine-readable mode — for `memoryctl doctor --json`
// invoked from scripts or dashboards.
func (r Report) PrintJSON(w io.Writer) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(r)
}

// Run gathers every diagnostic in order and returns a combined Report.
// All checks are defensive: individual read failures are rendered as WARN
// or FAIL in the relevant check, never panic.
//
// st is the resolved native store: the corpus checks enumerate its
// per-project directory (plus the global store), via native.Collect. In v2
// the memory content lives in native files, not under memoryDir — memoryDir
// is only the metadata root (.wal, .sidecar.db). anchor is the project
// anchor this invocation resolved its store from (CLAUDE_PROJECT_DIR /
// CLAUDE_PROJECT_CWD / session pin / cwd); checkStoreResolution reports it
// and uses it to look for legacy-slug store siblings.
func Run(claudeDir, memoryDir string, st native.Store, anchor string) Report {
	now := resolveDoctorNow()
	r := Report{ClaudeDir: claudeDir, MemoryDir: memoryDir, Now: now}
	r.Checks = append(r.Checks,
		checkClaudeDir(claudeDir),
		checkMemoryDir(memoryDir),
		checkSettings(filepath.Join(claudeDir, "settings.json")),
		checkShimFiles(claudeDir),
		checkBrokenSymlinks(filepath.Join(claudeDir, "hooks"), "hooks"),
		checkBrokenSymlinks(filepath.Join(claudeDir, "bin"), "bin"),
		checkMemoryctl(claudeDir),
		checkCorpus(claudeDir, memoryDir, st),
		checkWALErrors(filepath.Join(memoryDir, ".wal"), now),
		checkOpenQuanta(filepath.Join(memoryDir, ".wal"), now),
		checkCitationSignal(filepath.Join(memoryDir, ".wal"), now),
		checkSidecar(memoryDir, now),
		checkCorpusQuality(claudeDir, st),
		checkOversized(claudeDir, st),
		checkCandidates(memoryDir, claudeDir, st),
		checkStoreResolution(claudeDir, st, anchor),
	)
	return r
}

// --- Individual checks. Each returns a single Check and never fails the
// whole pass; a missing prerequisite degrades gracefully. ---

// resolveDoctorNow honours HYPOMNEMA_TODAY, then HYPOMNEMA_NOW, so
// doctor's WAL-window cutoffs freeze with the rest of the system in tests
// and fixture snapshots.
// Falls back to wall-clock time when neither var is set.
func resolveDoctorNow() time.Time {
	if s := os.Getenv("HYPOMNEMA_TODAY"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t
		}
	}
	if s := os.Getenv("HYPOMNEMA_NOW"); s != "" {
		if t, err := time.Parse("2006-01-02 15:04", s); err == nil {
			return t
		}
	}
	return time.Now()
}
