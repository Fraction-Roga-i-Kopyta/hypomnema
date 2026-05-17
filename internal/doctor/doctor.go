// Package doctor collects on-demand health diagnostics for a hypomnema
// install. It complements the SessionStart health-check block (which runs
// only at session start and only for three conditions) with a broader
// inspection an operator can invoke any time: installation sanity, corpus
// counts, recent WAL errors, derived-index freshness.
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
	"strconv"
	"strings"
	"time"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/decisions"
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
// context that shells can parse (see `scoring_components_active`,
// which exposes active/total/dormant lists instead of packing them
// into Detail); omitted when empty to keep the JSON output stable
// for checks that don't need it.
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
func Run(claudeDir, memoryDir string) Report {
	now := resolveDoctorNow()
	r := Report{ClaudeDir: claudeDir, MemoryDir: memoryDir, Now: now}
	r.Checks = append(r.Checks,
		checkClaudeDir(claudeDir),
		checkMemoryDir(memoryDir),
		checkSettings(filepath.Join(claudeDir, "settings.json")),
		checkBrokenSymlinks(filepath.Join(claudeDir, "hooks"), "hooks"),
		checkBrokenSymlinks(filepath.Join(claudeDir, "bin"), "bin"),
		checkMemoryctl(claudeDir),
		checkCorpus(memoryDir),
		checkWALErrors(filepath.Join(memoryDir, ".wal"), now),
		checkOpenQuanta(filepath.Join(memoryDir, ".wal"), now),
		checkDecisionsPressure(memoryDir, now),
		checkIndices(memoryDir, now),
		checkScoringComponents(memoryDir, now),
		checkCorpusQuality(memoryDir),
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

func checkMemoryDir(dir string) Check {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return Check{
			Name:   "memory_dir_exists",
			Status: FAIL,
			Detail: fmt.Sprintf("%s missing — run install.sh", dir),
		}
	}
	missing := []string{}
	for _, sub := range []string{"mistakes", "strategies", "feedback", "knowledge"} {
		if _, err := os.Stat(filepath.Join(dir, sub)); err != nil {
			missing = append(missing, sub)
		}
	}
	if len(missing) > 0 {
		return Check{
			Name:   "memory_dir_exists",
			Status: WARN,
			Detail: fmt.Sprintf("%s present but subdirs missing: %s", dir, strings.Join(missing, ",")),
		}
	}
	return Check{Name: "memory_dir_exists", Status: OK, Detail: dir}
}

// requiredHookCommands is the set install.sh wires up on a fresh install,
// in suffix form so we don't care whether the command string uses
// ~/.claude/... or an absolute path. Kept in one place so the list stays
// in lockstep with install.sh.
var requiredHookCommands = []string{
	"memory-session-start.sh",
	"memory-stop.sh",
	"memory-user-prompt-submit.sh",
	"memory-dedup.sh",
	"memory-outcome.sh",
	"memory-error-detect.sh",
	"memory-precompact.sh",
	// v0.13 addition — install.sh registers this on PreToolUse:Write|Edit.
	"memory-secrets-detect.sh",
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
	// extend it between releases), substring-match each required command
	// across the whole settings.json. Positive matches are enough because
	// install.sh always emits the suffixes literally.
	text := string(data)
	missing := []string{}
	for _, cmd := range requiredHookCommands {
		if !strings.Contains(text, cmd) {
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

// memoryTypes enumerates the directories checkCorpus reports on, in the
// order they appear in docs/FORMAT.md §2.
var memoryTypes = []string{
	"mistakes", "strategies", "feedback", "knowledge",
	"decisions", "notes", "projects", "continuity",
}

func checkCorpus(memoryDir string) Check {
	counts := map[string]int{}
	pinned, stale, archived := 0, 0, 0
	total := 0
	for _, t := range memoryTypes {
		dir := filepath.Join(memoryDir, t)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			counts[t]++
			total++
			status := quickReadStatus(filepath.Join(dir, e.Name()))
			switch status {
			case "pinned":
				pinned++
			case "stale":
				stale++
			}
		}
	}
	// archive/ has a nested layout (archive/<type>/*.md).
	archDir := filepath.Join(memoryDir, "archive")
	if entries, err := os.ReadDir(archDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sub, err := os.ReadDir(filepath.Join(archDir, e.Name()))
			if err != nil {
				continue
			}
			for _, f := range sub {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
					archived++
				}
			}
		}
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{}
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", k, counts[k]))
	}
	detail := fmt.Sprintf("%d total (%s); %d pinned, %d stale, %d archived",
		total, strings.Join(parts, " "), pinned, stale, archived)
	status := OK
	if total == 0 {
		status = WARN
		detail = "corpus is empty — hypomnema has nothing to inject yet"
	}
	return Check{Name: "corpus_counts", Status: status, Detail: detail}
}

// fmFacts captures the narrow frontmatter properties checkCorpusQuality
// needs. No general-purpose parse; just the flags we actually inspect.
type fmFacts struct {
	hasStatus      bool
	statusValue    string
	hasTriggers    bool
	hasEvidence    bool
	ambient        bool
	supersededFlag bool
}

// readFMQuick reads the first YAML frontmatter block and returns the
// subset of fields checkCorpusQuality cares about. Stops at the second
// `---` sentinel.
func readFMQuick(path string) fmFacts {
	var facts fmFacts
	f, err := os.Open(path)
	if err != nil {
		return facts
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8*1024), 64*1024)
	fmCount := 0
	for sc.Scan() {
		line := sc.Text()
		if line == "---" {
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
			facts.hasStatus = true
			v := strings.TrimSpace(strings.TrimPrefix(line, "status:"))
			v = strings.Trim(v, `"'`)
			facts.statusValue = v
		}
		if strings.HasPrefix(line, "triggers:") || strings.HasPrefix(line, "trigger:") {
			facts.hasTriggers = true
		}
		if strings.HasPrefix(line, "evidence:") {
			facts.hasEvidence = true
		}
		if strings.HasPrefix(line, "precision_class:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "precision_class:"))
			v = strings.Trim(v, `"'`)
			if v == "ambient" {
				facts.ambient = true
			}
		}
		if strings.HasPrefix(line, "status:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "status:"))
			v = strings.Trim(v, `"'`)
			if v == "superseded" || v == "archived" {
				facts.supersededFlag = true
			}
		}
	}
	return facts
}

// checkCorpusQuality surfaces frontmatter issues the ranking pipeline
// cannot act on but the user probably didn't mean:
//   - `status:` present but empty → filter drops the file silently
//   - feedback / knowledge without `triggers:` → reactive injection
//     cannot fire (the keyword pipeline still works from SessionStart)
//
// Skipped (no WARN) when a file is one of:
//   - `precision_class: ambient` — silent rule by design, doesn't need
//     reactive triggers
//   - has `evidence:` — session-stop classifier picks it up reactively
//     even without substring triggers
//   - `status: superseded` / `status: archived` — historical record,
//     not expected to fire
//
// Always WARN-level — these are nudges, never hard failures, per round
// 2 decision on `triggers:` remaining optional.
func checkCorpusQuality(memoryDir string) Check {
	const name = "corpus_frontmatter_quality"
	var emptyStatus, missingTriggers []string
	for _, t := range memoryTypes {
		dir := filepath.Join(memoryDir, t)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			fm := readFMQuick(path)
			if fm.hasStatus && fm.statusValue == "" {
				emptyStatus = append(emptyStatus, t+"/"+e.Name())
			}
			if (t == "feedback" || t == "knowledge") && !fm.hasTriggers &&
				!fm.ambient && !fm.hasEvidence && !fm.supersededFlag {
				missingTriggers = append(missingTriggers, t+"/"+e.Name())
			}
		}
	}
	sort.Strings(emptyStatus)
	sort.Strings(missingTriggers)

	status := OK
	var parts []string
	hint := ""
	if len(emptyStatus) > 0 {
		status = WARN
		parts = append(parts, fmt.Sprintf("%d with empty status:", len(emptyStatus)))
	}
	if len(missingTriggers) > 0 {
		status = WARN
		parts = append(parts, fmt.Sprintf("%d feedback/knowledge without triggers:", len(missingTriggers)))
	}
	detail := "all files have valid frontmatter"
	if len(parts) > 0 {
		detail = strings.Join(parts, ", ")
		hint = "set status: to 'active' (or remove the line); feedback/knowledge without triggers: won't fire reactively — add `triggers:`, `evidence:`, or `precision_class: ambient` (see CLAUDE.md § When to write feedback)"
	}

	extra := map[string]interface{}{
		"empty_status_count":     len(emptyStatus),
		"missing_triggers_count": len(missingTriggers),
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
	if len(missingTriggers) > 0 {
		if len(missingTriggers) > sampleCap {
			extra["missing_triggers_sample"] = missingTriggers[:sampleCap]
		} else {
			extra["missing_triggers_sample"] = missingTriggers
		}
	}

	return Check{
		Name:   name,
		Status: status,
		Detail: detail,
		Extra:  extra,
	}
}

// quickReadStatus scans at most the first ~40 lines of a file for a
// status: line. Full-file parse is unnecessary here.
func quickReadStatus(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8*1024), 64*1024)
	for i := 0; sc.Scan() && i < 40; i++ {
		line := sc.Text()
		if strings.HasPrefix(line, "status:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "status:"))
			v = strings.Trim(v, `"'`)
			return v
		}
	}
	return ""
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

// indexAgeThresholdDays defines how old a derived index can get before
// doctor nags the operator. Picked to match memory-analytics' weekly
// cadence plus some slack.
const indexAgeThresholdDays = 14

func checkIndices(memoryDir string, now time.Time) Check {
	type item struct {
		path, label string
		required    bool
	}
	items := []item{
		{filepath.Join(memoryDir, "index.db"), "fts", false},
		{filepath.Join(memoryDir, ".tfidf-index"), "tfidf", false},
		{filepath.Join(memoryDir, ".analytics-report"), "analytics", false},
	}
	parts := []string{}
	missing := []string{}
	stale := []string{}
	for _, it := range items {
		info, err := os.Stat(it.path)
		if err != nil {
			missing = append(missing, it.label)
			continue
		}
		days := int(now.Sub(info.ModTime()).Hours() / 24)
		parts = append(parts, fmt.Sprintf("%s:%dd", it.label, days))
		if days > indexAgeThresholdDays {
			stale = append(stale, fmt.Sprintf("%s(%dd)", it.label, days))
		}
	}
	// Derived indices are regenerated on demand — absence is expected on
	// a brand-new install, so treat missing as OK but surface it.
	status := OK
	detail := ""
	switch {
	case len(parts) == 0:
		detail = "no derived indices yet (fresh install? they'll build on first session)"
	case len(stale) > 0:
		status = WARN
		detail = fmt.Sprintf("ages %s; stale: %s", strings.Join(parts, " "), strings.Join(stale, ","))
	default:
		detail = "ages " + strings.Join(parts, " ")
		if len(missing) > 0 {
			detail += "; missing " + strings.Join(missing, ",")
		}
	}
	return Check{Name: "derived_indices", Status: status, Detail: detail}
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

// checkDecisionsPressure evaluates ADR review-triggers in
// ~/.claude/memory/decisions/ (personal ADRs — project-level ADRs in
// docs/decisions/ are outside this check's scope) against the current
// snapshot, and WARNs if any trigger fires. Not FAIL — a pressured
// decision is an advisory to the operator, not a broken install.
func checkDecisionsPressure(memoryDir string, now time.Time) Check {
	name := "decisions_review"
	dir := filepath.Join(memoryDir, "decisions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return Check{Name: name, Status: OK, Detail: "no personal decisions/ directory"}
		}
		return Check{Name: name, Status: WARN, Detail: err.Error()}
	}

	snap, err := decisions.BuildSnapshot(memoryDir, now)
	if err != nil {
		return Check{Name: name, Status: WARN, Detail: "snapshot: " + err.Error()}
	}

	fired := 0
	parseErrs := 0
	adrCount := 0
	var examples []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "README.md" {
			continue
		}
		adrCount++
		_, triggers, err := decisions.ReadADR(filepath.Join(dir, e.Name()))
		if err != nil {
			parseErrs++
			continue
		}
		if len(triggers) == 0 {
			continue
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		for _, r := range decisions.Evaluate(triggers, slug, snap) {
			if r.Status == decisions.StatusPressure || r.Status == decisions.StatusOverdue {
				fired++
				if len(examples) < 3 {
					examples = append(examples, slug)
				}
			}
		}
	}

	if adrCount == 0 {
		return Check{Name: name, Status: OK, Detail: "0 personal ADRs"}
	}
	if parseErrs > 0 {
		return Check{
			Name:   name,
			Status: WARN,
			Detail: fmt.Sprintf("%d ADR(s) have malformed review-triggers (`memoryctl decisions review` shows details)", parseErrs),
		}
	}
	if fired == 0 {
		return Check{
			Name:   name,
			Status: OK,
			Detail: fmt.Sprintf("%d ADR(s), no pressure", adrCount),
		}
	}
	return Check{
		Name:   name,
		Status: WARN,
		Detail: fmt.Sprintf("%d trigger(s) fired across %d ADR(s): %s; run `memoryctl decisions review` for detail",
			fired, adrCount, strings.Join(examples, ", ")),
	}
}

// checkScoringComponents reports how many of the five SessionStart
// scoring components are active vs dormant under the cold-start gates
// from `docs/decisions/cold-start-scoring.md`.
//
// Active components: keyword_hits, project_boost, recency — always live.
// Dormant when thresholds unmet: wal_spaced_repetition (Bayesian),
// tfidf_body_match.
//
// Thresholds mirror the ENV variables the hook scripts read:
//   - HYPOMNEMA_BAYESIAN_MIN_SAMPLES (default 5)
//   - HYPOMNEMA_OUTCOME_WINDOW_DAYS  (default 14)
//   - HYPOMNEMA_TFIDF_MIN_VOCAB      (default 100)
//
// The check always returns OK status — it's an INFO-level report on
// scoring-pipeline state, not a health problem. Extra carries the
// machine-readable breakdown so shells (and the fixture harness) can
// assert against specific fields.
func checkScoringComponents(memoryDir string, now time.Time) Check {
	const name = "scoring_components_active"
	minSamples := envInt("HYPOMNEMA_BAYESIAN_MIN_SAMPLES", 5)
	windowDays := envInt("HYPOMNEMA_OUTCOME_WINDOW_DAYS", 14)
	minVocab := envInt("HYPOMNEMA_TFIDF_MIN_VOCAB", 100)

	walPath := filepath.Join(memoryDir, ".wal")
	tfidfPath := filepath.Join(memoryDir, ".tfidf-index")

	cutoff := now.AddDate(0, 0, -windowDays).Format("2006-01-02")
	outcomeCount := countOutcomeEventsAfter(walPath, cutoff)
	vocabSize := countFileLines(tfidfPath)

	type dormantEntry struct {
		Component string `json:"component"`
		Reason    string `json:"reason"`
	}

	total := 5
	active := total
	var dormant []dormantEntry
	var dormantNames []string

	if outcomeCount < minSamples {
		active--
		dormant = append(dormant, dormantEntry{
			Component: "bayesian",
			Reason:    fmt.Sprintf("need >=%d outcomes in %dd, have %d", minSamples, windowDays, outcomeCount),
		})
		dormantNames = append(dormantNames, "bayesian")
	}
	if vocabSize < minVocab {
		active--
		dormant = append(dormant, dormantEntry{
			Component: "tfidf",
			Reason:    fmt.Sprintf("vocab <%d, have %d", minVocab, vocabSize),
		})
		dormantNames = append(dormantNames, "tfidf")
	}

	detail := fmt.Sprintf("%d/%d active", active, total)
	if len(dormant) > 0 {
		var parts []string
		for _, d := range dormant {
			parts = append(parts, fmt.Sprintf("%s (%s)", d.Component, d.Reason))
		}
		detail = fmt.Sprintf("%d/%d active; dormant: %s", active, total, strings.Join(parts, ", "))
	}

	// Explicit non-nil slices so JSON emits [] instead of null when the
	// corpus has all components active — stable shape for downstream jq.
	if dormant == nil {
		dormant = []dormantEntry{}
	}
	if dormantNames == nil {
		dormantNames = []string{}
	}

	return Check{
		Name:   name,
		Status: OK,
		Detail: detail,
		Extra: map[string]interface{}{
			"active":             active,
			"total":              total,
			"dormant":            dormant,
			"dormant_components": dormantNames,
			"thresholds": map[string]interface{}{
				"min_outcome_samples": minSamples,
				"outcome_window_days": windowDays,
				"min_tfidf_vocab":     minVocab,
			},
			"observed": map[string]interface{}{
				"outcome_events_in_window": outcomeCount,
				"tfidf_vocab_size":         vocabSize,
			},
		},
	}
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

func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

// countOutcomeEventsAfter counts outcome-positive + outcome-negative
// events whose WAL date column is >= cutoff (lexicographic compare on
// YYYY-MM-DD works for 4-column canonical WAL lines).
func countOutcomeEventsAfter(walPath, cutoff string) int {
	f, err := os.Open(walPath)
	if err != nil {
		return 0
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	count := 0
	for s.Scan() {
		line := s.Text()
		if line == "" {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) != 4 {
			continue
		}
		if fields[0] < cutoff {
			continue
		}
		if fields[1] == "outcome-positive" || fields[1] == "outcome-negative" {
			count++
		}
	}
	return count
}

// countFileLines returns the number of newline-terminated lines in
// path, or 0 if the file can't be opened. Used for TF-IDF vocab size
// (one term per line in .tfidf-index).
func countFileLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	n := 0
	for s.Scan() {
		n++
	}
	return n
}
