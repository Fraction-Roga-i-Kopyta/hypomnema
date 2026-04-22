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
	"strings"
	"time"
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

// Check is a single diagnostic result.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
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
	now := time.Now()
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
		checkIndices(memoryDir, now),
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
