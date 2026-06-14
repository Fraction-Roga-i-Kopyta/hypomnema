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
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
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
// cwd is the project working directory: the corpus checks enumerate the
// per-project native store derived from it (plus the global store), via
// native.Collect. In v2 the memory content lives in native files, not under
// memoryDir — memoryDir is only the metadata root (.wal, .sidecar.db).
func Run(claudeDir, memoryDir, cwd string) Report {
	now := resolveDoctorNow()
	r := Report{ClaudeDir: claudeDir, MemoryDir: memoryDir, Now: now}
	r.Checks = append(r.Checks,
		checkClaudeDir(claudeDir),
		checkMemoryDir(memoryDir),
		checkSettings(filepath.Join(claudeDir, "settings.json")),
		checkBrokenSymlinks(filepath.Join(claudeDir, "hooks"), "hooks"),
		checkBrokenSymlinks(filepath.Join(claudeDir, "bin"), "bin"),
		checkMemoryctl(claudeDir),
		checkCorpus(claudeDir, memoryDir, cwd),
		checkWALErrors(filepath.Join(memoryDir, ".wal"), now),
		checkOpenQuanta(filepath.Join(memoryDir, ".wal"), now),
		checkSidecar(memoryDir, now),
		checkCorpusQuality(claudeDir, cwd),
	)
	return r
}

// --- Individual checks. Each returns a single Check and never fails the
// whole pass; a missing prerequisite degrades gracefully. ---

func checkClaudeDir(dir string) Check {
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return Check{Name: "claude_dir_exists", Status: OK, Detail: dir}
	}
	return Check{
		Name:   "claude_dir_exists",
		Status: FAIL,
		Detail: fmt.Sprintf("%s is missing — hypomnema is not installed", dir),
	}
}

// checkMemoryDir verifies the v2 metadata root exists. In v2 this directory
// holds only metadata (.wal, .sidecar.db) — memory content lives in native
// files elsewhere, so there are no required subdirectories. A missing .wal
// on a fresh install is normal and is merely noted, never a failure.
func checkMemoryDir(dir string) Check {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return Check{
			Name:   "memory_dir_exists",
			Status: FAIL,
			Detail: fmt.Sprintf("%s missing — run install.sh", dir),
		}
	}
	detail := dir
	if _, err := os.Stat(filepath.Join(dir, ".wal")); err == nil {
		detail += " (.wal present)"
	} else {
		detail += " (no .wal yet — fresh install)"
	}
	return Check{Name: "memory_dir_exists", Status: OK, Detail: detail}
}

// requiredHookCommands is the set install.sh wires up on a fresh install,
// as the suffix of the v2 shim path "hooks/v2/<name>". Kept in one place
// so the list stays in lockstep with install.sh.
var requiredHookCommands = []string{
	"session-start.sh",
	"user-prompt-submit.sh",
	"pre-tool-write.sh",
	"skill-learnings-inject.sh",
	"skill-active.sh",
	"session-stop.sh",
}

func checkSettings(path string) Check {
	data, err := os.ReadFile(path)
	if err != nil {
		return Check{
			Name:   "settings_hooks_registered",
			Status: FAIL,
			Detail: fmt.Sprintf("%s unreadable: %v", path, err),
		}
	}
	// Rather than parsing the full schema (Claude Code owns it and can
	// extend it between releases), substring-match each required v2 shim
	// path across the whole settings.json. install.sh registers them as
	// "~/.claude/hooks/v2/<name>" so match against "hooks/v2/<name>".
	text := string(data)
	missing := []string{}
	for _, cmd := range requiredHookCommands {
		if !strings.Contains(text, "hooks/v2/"+cmd) {
			missing = append(missing, cmd)
		}
	}
	if len(missing) == 0 {
		return Check{
			Name:   "settings_hooks_registered",
			Status: OK,
			Detail: fmt.Sprintf("all %d hypomnema hooks registered", len(requiredHookCommands)),
		}
	}
	return Check{
		Name:   "settings_hooks_registered",
		Status: FAIL,
		Detail: fmt.Sprintf("missing: %s — re-run ./install.sh", strings.Join(missing, ",")),
	}
}

// checkBrokenSymlinks walks dir (one level) and reports symlinks whose
// targets do not resolve. label is "hooks" or "bin" for the message.
func checkBrokenSymlinks(dir, label string) Check {
	name := "no_broken_symlinks_" + label
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return Check{Name: name, Status: WARN, Detail: dir + " absent"}
		}
		return Check{Name: name, Status: WARN, Detail: err.Error()}
	}
	broken := []string{}
	total := 0
	for _, e := range entries {
		info, err := os.Lstat(filepath.Join(dir, e.Name()))
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		total++
		if _, err := os.Stat(filepath.Join(dir, e.Name())); err != nil {
			broken = append(broken, e.Name())
		}
	}
	if len(broken) == 0 {
		return Check{Name: name, Status: OK, Detail: fmt.Sprintf("%d symlinks, all resolve", total)}
	}
	return Check{
		Name:   name,
		Status: FAIL,
		Detail: fmt.Sprintf("%d broken: %s", len(broken), strings.Join(broken, ",")),
	}
}

func checkMemoryctl(claudeDir string) Check {
	// First preference: the install.sh target (~/.claude/bin/memoryctl).
	// If that's present the PATH question is moot for hypomnema itself;
	// PATH only matters for users running `memoryctl ...` in a shell.
	local := filepath.Join(claudeDir, "bin", "memoryctl")
	if info, err := os.Stat(local); err == nil && !info.IsDir() {
		if _, err := exec.LookPath("memoryctl"); err != nil {
			return Check{
				Name:   "memoryctl_available",
				Status: WARN,
				Detail: local + " present but not on $PATH — shell invocations won't find it",
			}
		}
		return Check{Name: "memoryctl_available", Status: OK, Detail: local}
	}
	return Check{
		Name:   "memoryctl_available",
		Status: WARN,
		Detail: "memoryctl binary not built — run `make build` then ./install.sh (dedup/fts-sync will fall back to bash)",
	}
}

// checkCorpus enumerates the v2 native corpus (per-project + global, merged
// by native.Collect) and reports counts by logical type. The type is the
// singular `type:` frontmatter value (mistake, strategy, …), not a directory.
//
// Native files carry NO status: in v2 status lives only in the sidecar, so
// pinned/stale totals are looked up from the sidecar (.sidecar.db) by slug.
// A native file with no matching sidecar row defaults to "active". The lookup
// is fully graceful: a missing or unreadable sidecar yields 0 pinned / 0 stale
// and never fails the check. Sidecar rows whose status is "deleted" are
// orphans (no live native file) and contribute nothing to the corpus counts.
func checkCorpus(claudeDir, memoryDir, cwd string) Check {
	files := native.Collect(claudeDir, cwd)
	statusBySlug := sidecarStatusMap(memoryDir)
	counts := map[string]int{}
	pinned, stale := 0, 0
	for _, f := range files {
		typ := f.Type
		if typ == "" {
			typ = "untyped"
		}
		counts[typ]++
		status := statusBySlug[f.Slug]
		if status == "" {
			status = "active"
		}
		switch status {
		case "pinned":
			pinned++
		case "stale":
			stale++
		}
	}
	total := len(files)
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{}
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", k, counts[k]))
	}
	detail := fmt.Sprintf("%d total (%s); %d pinned, %d stale",
		total, strings.Join(parts, " "), pinned, stale)
	status := OK
	if total == 0 {
		status = WARN
		detail = "corpus is empty — hypomnema has nothing to inject yet"
	}
	return Check{Name: "corpus_counts", Status: status, Detail: detail}
}

// sidecarStatusMap opens the sidecar at memoryDir/.sidecar.db and returns a
// slug→status map for every live row. Rows with status "deleted" are skipped
// (orphans whose native file is gone). Fully graceful: a missing or unreadable
// sidecar returns an empty (non-nil) map so the caller can default every native
// file to "active" without crashing or failing the check.
func sidecarStatusMap(memoryDir string) map[string]string {
	out := map[string]string{}
	dbPath := filepath.Join(memoryDir, ".sidecar.db")
	// doctor is strictly read-only — do not let sidecar.Open create an empty
	// DB as a side effect when none exists. Treat absence as "no statuses".
	if _, err := os.Stat(dbPath); err != nil {
		return out
	}
	s, err := sidecar.Open(dbPath)
	if err != nil {
		return out
	}
	defer s.Close()
	recs, err := s.All()
	if err != nil {
		return out
	}
	for _, r := range recs {
		if r.Status == "deleted" {
			continue
		}
		out[r.Slug] = r.Status
	}
	return out
}

// fmStatus reports whether the first frontmatter block declares a `status:`
// key and, if so, its (possibly empty) value. Distinguishing "absent" from
// "present but empty" is the whole point. Stops at the second `---` sentinel.
func fmStatus(path string) (present bool, value string) {
	f, err := os.Open(path)
	if err != nil {
		return false, ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8*1024), 64*1024)
	fmCount := 0
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimRight(line, " \t\r") == "---" {
			fmCount++
			if fmCount >= 2 {
				break
			}
			continue
		}
		if fmCount != 1 {
			continue
		}
		if strings.HasPrefix(line, "status:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "status:"))
			v = strings.Trim(v, `"'`)
			return true, v
		}
	}
	return false, ""
}

// checkCorpusQuality surfaces frontmatter issues the ranking pipeline cannot
// act on but the user probably didn't mean. In v2 the only such issue is a
// `status:` line present but empty — the status filter then drops the file
// silently. (The v1 "feedback/knowledge without triggers:" nudge is gone:
// triggers are retired in v2; the ranker treats all tokens as relevance
// signal, so there is no reactive-trigger requirement to warn about.)
//
// Always WARN-level — a nudge, never a hard failure.
func checkCorpusQuality(claudeDir, cwd string) Check {
	const name = "corpus_frontmatter_quality"
	var emptyStatus []string
	for _, f := range native.Collect(claudeDir, cwd) {
		present, value := fmStatus(f.Path)
		if present && value == "" {
			emptyStatus = append(emptyStatus, f.Slug)
		}
	}
	sort.Strings(emptyStatus)

	status := OK
	detail := "all files have valid frontmatter"
	hint := ""
	if len(emptyStatus) > 0 {
		status = WARN
		detail = fmt.Sprintf("%d with empty status:", len(emptyStatus))
		hint = "set status: to 'active' (or remove the line) — an empty status: drops the file from injection"
	}

	extra := map[string]interface{}{
		"empty_status_count": len(emptyStatus),
	}
	if hint != "" {
		extra["hint"] = hint
	}
	// Surface sample filenames (capped) so the operator knows where to
	// look without running a separate grep.
	const sampleCap = 5
	if len(emptyStatus) > 0 {
		if len(emptyStatus) > sampleCap {
			extra["empty_status_sample"] = emptyStatus[:sampleCap]
		} else {
			extra["empty_status_sample"] = emptyStatus
		}
	}

	return Check{
		Name:   name,
		Status: status,
		Detail: detail,
		Extra:  extra,
	}
}

// walErrorEvents lists WAL event types that signal corpus-health trouble.
// Counted together as a single aggregate for the doctor check.
var walErrorEvents = []string{
	"schema-error",
	"format-unsupported",
	"evidence-missing",
	"evidence-empty",
}

func checkWALErrors(walPath string, now time.Time) Check {
	f, err := os.Open(walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Check{Name: "wal_errors_last_7d", Status: OK, Detail: "no .wal yet (fresh install)"}
		}
		return Check{Name: "wal_errors_last_7d", Status: WARN, Detail: err.Error()}
	}
	defer f.Close()
	cutoff := now.AddDate(0, 0, -7).Format("2006-01-02")
	counts := map[string]int{}
	totalLines := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		totalLines++
		line := sc.Text()
		// WAL grammar: date|event|target|session (docs/FORMAT.md §5).
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 2 {
			continue
		}
		if parts[0] < cutoff {
			continue
		}
		for _, ev := range walErrorEvents {
			if parts[1] == ev {
				counts[ev]++
				break
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Check{Name: "wal_errors_last_7d", Status: WARN, Detail: "scan error: " + err.Error()}
	}
	totalErrs := 0
	parts := []string{}
	// Deterministic ordering for the detail string.
	for _, ev := range walErrorEvents {
		if n := counts[ev]; n > 0 {
			totalErrs += n
			parts = append(parts, fmt.Sprintf("%s:%d", ev, n))
		}
	}
	if totalErrs == 0 {
		return Check{
			Name:   "wal_errors_last_7d",
			Status: OK,
			Detail: fmt.Sprintf("no schema/format/evidence errors in last 7d (wal=%d lines)", totalLines),
		}
	}
	// Any error class present is WARN, not FAIL — these degrade quality
	// but don't break the pipeline. They deserve attention, not alarm.
	return Check{
		Name:   "wal_errors_last_7d",
		Status: WARN,
		Detail: fmt.Sprintf("%d events (%s); inspect .wal", totalErrs, strings.Join(parts, " ")),
	}
}

// checkSidecar reports on the SQLite metadata sidecar (.sidecar.db). The
// sidecar is rebuildable from the WAL + native frontmatter, so its absence
// is only a problem once there is a WAL to rebuild from:
//   - absent, no .wal      → OK (fresh install, nothing to rebuild yet)
//   - absent, .wal present → WARN (run `memoryctl sidecar rebuild`)
//   - present but older than .wal (by mtime) → WARN (stale vs WAL)
//   - present and at least as new as .wal    → OK
//
// v2 has no FTS / TF-IDF / analytics derived indices, so none are checked.
func checkSidecar(memoryDir string, now time.Time) Check {
	const name = "sidecar"
	sidecarPath := filepath.Join(memoryDir, ".sidecar.db")
	walPath := filepath.Join(memoryDir, ".wal")

	walInfo, walErr := os.Stat(walPath)
	sidecarInfo, sidecarErr := os.Stat(sidecarPath)

	if sidecarErr != nil {
		if walErr != nil {
			return Check{
				Name:   name,
				Status: OK,
				Detail: "no .sidecar.db yet (fresh install — nothing to rebuild)",
			}
		}
		return Check{
			Name:   name,
			Status: WARN,
			Detail: ".sidecar.db missing but .wal exists — run `memoryctl sidecar rebuild`",
		}
	}

	if walErr == nil && sidecarInfo.ModTime().Before(walInfo.ModTime()) {
		return Check{
			Name:   name,
			Status: WARN,
			Detail: "sidecar stale vs WAL — run `memoryctl sidecar rebuild`",
		}
	}
	return Check{
		Name:   name,
		Status: OK,
		Detail: fmt.Sprintf(".sidecar.db present (mtime %s)", sidecarInfo.ModTime().Format("2006-01-02 15:04")),
	}
}

// closingEvents are the WAL event types that signal "Stop hook reached a
// classification pass for this session_id" — i.e. the behavioural quantum
// closed in the TFS sense (inject → some posterior). See
// docs/notes/measurement-bias.md for why session-metrics is intentionally
// excluded (it doesn't carry session_id in column 4, a FORMAT.md §5.2
// violation).
var closingEvents = map[string]bool{
	"trigger-useful":       true,
	"trigger-silent":       true,
	"trigger-silent-retro": true, // v0.13+ retroactive classification
	"outcome-positive":     true,
	"outcome-negative":     true,
	"clean-session":        true,
}

// openQuantaWindowDays bounds the window checkOpenQuanta examines. Older
// entries are excluded because they may predate the v0.7.1 evidence-match
// rollout, when trigger-useful/silent literally could not fire. 30 days
// matches the SessionStart recency horizon already used for scoring.
const openQuantaWindowDays = 30

// openQuantaWarnThreshold flips the check to WARN when at least this
// fraction of recent inject-carrying sessions never got a closing event.
// 0.5 — half the quanta open is loud enough to be worth a line in the
// report; anything below stays OK.
const openQuantaWarnThreshold = 0.5

func checkOpenQuanta(walPath string, now time.Time) Check {
	name := "open_quanta_last_30d"
	f, err := os.Open(walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Check{Name: name, Status: OK, Detail: "no .wal yet (fresh install)"}
		}
		return Check{Name: name, Status: WARN, Detail: err.Error()}
	}
	defer f.Close()
	cutoff := now.AddDate(0, 0, -openQuantaWindowDays).Format("2006-01-02")
	injectSess := map[string]int{} // session_id → inject count
	closedSess := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}
		if parts[0] < cutoff {
			continue
		}
		event, sess := parts[1], parts[3]
		if event == "inject" {
			injectSess[sess]++
			continue
		}
		if closingEvents[event] {
			closedSess[sess] = true
		}
	}
	if err := sc.Err(); err != nil {
		return Check{Name: name, Status: WARN, Detail: "scan error: " + err.Error()}
	}
	total := len(injectSess)
	if total == 0 {
		return Check{
			Name:   name,
			Status: OK,
			Detail: fmt.Sprintf("no inject-carrying sessions in last %dd", openQuantaWindowDays),
		}
	}
	open, lostInjects := 0, 0
	for s, n := range injectSess {
		if !closedSess[s] {
			open++
			lostInjects += n
		}
	}
	ratio := float64(open) / float64(total)
	detail := fmt.Sprintf("%d/%d sessions without closing signal (%.0f%%); %d injects unclassified in last %dd",
		open, total, ratio*100, lostInjects, openQuantaWindowDays)
	status := OK
	if ratio >= openQuantaWarnThreshold {
		status = WARN
		detail += " — skews Bayesian `eff`; see docs/notes/measurement-bias.md"
	}
	return Check{Name: name, Status: status, Detail: detail}
}

// resolveDoctorNow mirrors the HYPOMNEMA_TODAY / HYPOMNEMA_NOW
// precedence used by internal/profile so doctor's WAL-window cutoffs
// freeze with the rest of the system in tests and fixture snapshots.
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
