// Package profile reimplements bin/memory-self-profile.sh in Go.
//
// The bash version makes ~10 awk passes over the WAL, plus directory scans
// for mistakes/ and strategies/ frontmatter, plus a recursive grep for
// precision_class: ambient. This package folds the WAL into a single pass
// and parses frontmatter inline, matching the bash output byte-for-byte
// (verified by scripts/parity-check.sh).
//
// Contract: produces memoryDir/self-profile.md exactly as the bash script
// would. Same markdown structure, same ordering, same "n/a" fallback when
// trigger_total is zero, same English body strings.
package profile

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Generate writes memoryDir/self-profile.md. Errors are returned to the
// caller; the CLI wrapper treats any error as a no-op per the hook
// fail-safe contract (self-profile is observational, must not break
// session-stop).
func Generate(memoryDir string) error {
	walPath := filepath.Join(memoryDir, ".wal")
	if _, err := os.Stat(walPath); err != nil {
		return nil // WAL absent → quiet no-op, matches bash `[ -f ] || exit 0`
	}

	ambient, err := collectAmbientSlugs(memoryDir)
	if err != nil {
		return err
	}

	// Outcome cutoff for the per-slug Bayesian sampling window. Mirrors
	// the constants `internal/doctor` uses for its scoring_components_active
	// gate so the two stay synchronized.
	windowDays := envIntDefault("HYPOMNEMA_OUTCOME_WINDOW_DAYS", 14)
	minSamples := envIntDefault("HYPOMNEMA_BAYESIAN_MIN_SAMPLES", 5)
	cutoff := windowCutoffDate(windowDays)
	// Intuition-milestone window. Default 30 days per ADR
	// intuition-milestone.md; ENV-overrideable for fixture tests
	// (`HYPOMNEMA_INTUITION_WINDOW_DAYS=0` disables the filter).
	intuitionDays := envIntDefault("HYPOMNEMA_INTUITION_WINDOW_DAYS", 30)
	intuitionCutoff := windowCutoffDate(intuitionDays)

	sig, err := collectWALSignals(walPath, ambient, cutoff, intuitionCutoff)
	if err != nil {
		return err
	}

	// Aggregate ADR review-trigger metrics.
	computeAmbientFraction(sig)
	if err := computeCorpusBayesianFraction(memoryDir, sig, minSamples); err != nil {
		return err
	}

	weaknesses, err := collectWeaknesses(filepath.Join(memoryDir, "mistakes"))
	if err != nil {
		return err
	}

	strengths, err := collectStrengths(filepath.Join(memoryDir, "strategies"))
	if err != nil {
		return err
	}

	calibration := summariseCalibration(sig.domainTotal, sig.domainErr)

	ts := now()
	out := renderProfile(ts, sig, weaknesses, strengths, calibration)

	return atomicWriteSelfProfile(memoryDir, out)
}

// atomicWriteSelfProfile mirrors the tmp+rename pattern in
// tfidf.Rebuild — defensive against crash-mid-write leaving a
// half-rendered self-profile.md. See external review 2026-05-08
// finding E2 for the rationale.
func atomicWriteSelfProfile(memoryDir, content string) error {
	target := filepath.Join(memoryDir, "self-profile.md")
	tmp, err := os.CreateTemp(memoryDir, ".self-profile.*.tmp")
	if err != nil {
		return fmt.Errorf("profile: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("profile: write tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("profile: close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return fmt.Errorf("profile: rename to %s: %w", target, err)
	}
	cleanup = false
	return nil
}

// envIntDefault reads an integer env var, falling back to def when the
// value is missing or unparsable. Mirrors `internal/doctor.envInt`.
func envIntDefault(key string, def int) int {
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

// windowCutoffDate returns YYYY-MM-DD `windowDays` before the resolved
// "today" (HYPOMNEMA_TODAY → HYPOMNEMA_NOW → real). Empty windowDays
// disables the filter and the function returns "".
func windowCutoffDate(windowDays int) string {
	if windowDays <= 0 {
		return ""
	}
	var anchor time.Time
	if s := os.Getenv("HYPOMNEMA_TODAY"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			anchor = t
		}
	}
	if anchor.IsZero() {
		if s := os.Getenv("HYPOMNEMA_NOW"); s != "" {
			if t, err := time.Parse("2006-01-02 15:04", s); err == nil {
				anchor = t
			}
		}
	}
	if anchor.IsZero() {
		anchor = time.Now()
	}
	return anchor.AddDate(0, 0, -windowDays).Format("2006-01-02")
}

// computeAmbientFraction derives the share of ambient activations among
// all reactive trigger fires. Denominator is `triggerUsefulAll +
// triggerSilentAll`; numerator is `ambientActivations`. When the
// denominator is 0 the fraction is left undefined so the renderer
// emits "n/a%" instead of a misleading 0%.
func computeAmbientFraction(sig *walSignals) {
	total := sig.triggerUsefulAll + sig.triggerSilentAll
	if total <= 0 {
		return
	}
	sig.ambientFraction = float64(sig.ambientActivations) / float64(total)
	sig.ambientFractionDefined = true
}

// computeCorpusBayesianFraction walks every memory record (mistakes,
// feedback, strategies, knowledge, notes, decisions) and counts how
// many have ≥ minSamples outcome events inside the window already
// loaded into sig.outcomesPerSlug. The fraction is what the cold-
// start-scoring ADR's review-trigger watches for the corpus-level
// "Bayesian gate is dormant for too many slugs" signal.
func computeCorpusBayesianFraction(memoryDir string, sig *walSignals, minSamples int) error {
	memoryTypes := []string{"mistakes", "feedback", "strategies", "knowledge", "notes", "decisions"}
	var total, active int
	for _, t := range memoryTypes {
		dir := filepath.Join(memoryDir, t)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".md") {
				continue
			}
			slug := strings.TrimSuffix(name, ".md")
			total++
			if sig.outcomesPerSlug[slug] >= minSamples {
				active++
			}
		}
	}
	sig.corpusBayesianTotal = total
	sig.corpusBayesianActive = active
	if total > 0 {
		sig.corpusBayesianFraction = float64(active) / float64(total)
		sig.corpusBayesianFractionDefined = true
	}
	return nil
}

// now returns the formatted timestamp. HYPOMNEMA_NOW overrides for tests
// and parity — must be "YYYY-MM-DD HH:MM" literal.
func now() string {
	if v := os.Getenv("HYPOMNEMA_NOW"); v != "" {
		return v
	}
	return time.Now().Local().Format("2006-01-02 15:04")
}

// walSignals holds every counter derived from the WAL, computed in a
// single pass. Ambient-aware buckets (trigger-useful measurable etc.)
// require the ambient slug set ahead of time, so the WAL pass is not
// fully standalone — but all other counters are.
type walSignals struct {
	totalSessions       int
	cleanSessions       int
	outcomePositive     int
	outcomeNegative     int
	strategyUsed        int
	strategyGap         int
	triggerUsefulAll    int
	triggerSilentAll    int
	ambientActivations  int
	triggerUsefulMeas   int
	triggerSilentMeas   int
	silentApplied       int
	silentNoise         int
	evidenceEmptyUnique int
	outcomeNewCount     int
	precisionPct        string // "N" or "n/a"

	// Per-slug outcome counts in last OUTCOME_WINDOW days. Used to
	// compute corpus_fraction_with_active_bayesian.
	outcomesPerSlug map[string]int

	// Aggregate metrics for ADR review-triggers (cold-start-scoring,
	// quota-pool). Fractions stay as 0 when denominator is 0 — we
	// distinguish "fraction is 0" from "metric undefined" in the
	// render layer via the separate `*Defined` flags.
	ambientFraction              float64
	ambientFractionDefined       bool
	corpusBayesianActive         int
	corpusBayesianTotal          int
	corpusBayesianFraction       float64
	corpusBayesianFractionDefined bool

	// Intuition-milestone metric (ADR intuition-milestone.md, v1.1).
	// Windowed counters over the last INTUITION_WINDOW_DAYS (default 30)
	// for the silent-applied / trigger-useful ratio. Crossing > 1.0
	// sustained over the window is the operational definition of the
	// "intuition tier" — rules applied silently more often than they
	// are cited explicitly.
	silentAppliedRecent    int
	triggerUsefulMeasRecent int
	intuitionRatio         float64
	intuitionRatioDefined  bool

	domainTotal map[string]int
	domainErr   map[string]int
}

// collectWALSignals streams the WAL once. ambient maps slug→true; lookups
// drive the ambient-exclusion for precision calculation.
//
//   - outcomeCutoff is the YYYY-MM-DD date below which outcome events do
//     not count toward per-slug Bayesian-gate sampling (empty string
//     disables the filter).
//   - intuitionCutoff is the YYYY-MM-DD date below which trigger-useful
//     and trigger-silent events do not count toward the intuition-
//     milestone ratio. Same empty-string-disables semantics.
func collectWALSignals(walPath string, ambient map[string]bool, outcomeCutoff, intuitionCutoff string) (*walSignals, error) {
	f, err := os.Open(walPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sig := &walSignals{
		domainTotal:     map[string]int{},
		domainErr:       map[string]int{},
		outcomesPerSlug: map[string]int{},
	}

	// For silent-applied correlation we need to cross-reference two event
	// types per (slug, session) key, so buffer the keys from each side.
	silent := map[string]bool{}   // slug|session seen as trigger-silent (non-ambient)
	positive := map[string]bool{} // slug|session seen as outcome-positive
	evidenceEmpty := map[string]bool{}

	// Intuition-milestone windowed buckets — same shape as the
	// total-corpus silent / positive maps above, but only filled when
	// the row's date is within the intuition window. Empty cutoff
	// disables the filter (treat all rows as in-window). See ADR
	// intuition-milestone.md.
	silentRecent := map[string]bool{}

	sc := bufio.NewScanner(f)
	// WAL lines can be long if the bash side buffered arguments. Bump max
	// line from the default 64KiB to be safe against future growth.
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		fields := strings.Split(line, "|")
		if len(fields) < 2 {
			continue
		}
		event := fields[1]

		// Cheap counters first (most events land here).
		switch event {
		case "session-metrics":
			sig.totalSessions++
			if len(fields) >= 4 {
				domainsCSV, metricsBlob := splitSessionMetrics(fields[2], fields[3])
				ec := parseErrorCount(metricsBlob)
				for _, d := range strings.Split(domainsCSV, ",") {
					if d == "" || d == "unknown" {
						continue
					}
					sig.domainTotal[d]++
					if ec > 0 {
						sig.domainErr[d] += ec
					}
				}
			}
		case "clean-session":
			sig.cleanSessions++
		case "outcome-positive":
			sig.outcomePositive++
			if len(fields) >= 4 {
				positive[fields[2]+"|"+fields[3]] = true
			}
			if len(fields) >= 3 && (outcomeCutoff == "" || fields[0] >= outcomeCutoff) {
				sig.outcomesPerSlug[fields[2]]++
			}
		case "outcome-negative":
			sig.outcomeNegative++
			if len(fields) >= 3 && (outcomeCutoff == "" || fields[0] >= outcomeCutoff) {
				sig.outcomesPerSlug[fields[2]]++
			}
		case "strategy-used":
			sig.strategyUsed++
		case "strategy-gap":
			sig.strategyGap++
		case "trigger-useful":
			sig.triggerUsefulAll++
			slug := ""
			if len(fields) >= 3 {
				slug = fields[2]
			}
			if ambient[slug] {
				sig.ambientActivations++
			} else {
				sig.triggerUsefulMeas++
				if intuitionCutoff == "" || fields[0] >= intuitionCutoff {
					sig.triggerUsefulMeasRecent++
				}
			}
		case "trigger-silent":
			sig.triggerSilentAll++
			slug := ""
			if len(fields) >= 3 {
				slug = fields[2]
			}
			if ambient[slug] {
				sig.ambientActivations++
			} else {
				sig.triggerSilentMeas++
				if len(fields) >= 4 {
					silent[slug+"|"+fields[3]] = true
					if intuitionCutoff == "" || fields[0] >= intuitionCutoff {
						silentRecent[slug+"|"+fields[3]] = true
					}
				}
			}
		case "evidence-empty":
			if len(fields) >= 3 {
				evidenceEmpty[fields[2]] = true
			}
		case "outcome-new":
			sig.outcomeNewCount++
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	// Windowed silent-applied correlation for intuition milestone.
	// Uses the same positive[] map (positive events that lie outside
	// the intuition window are still legitimate observations of
	// "silent rule applied" — what we filter is the trigger-silent
	// side, which is the noisier signal that benefits from windowing).
	for k := range silentRecent {
		if positive[k] {
			sig.silentAppliedRecent++
		}
	}
	if sig.triggerUsefulMeasRecent > 0 {
		sig.intuitionRatio = float64(sig.silentAppliedRecent) / float64(sig.triggerUsefulMeasRecent)
		sig.intuitionRatioDefined = true
	}

	// Correlate silent (non-ambient) with positive of same (slug, session).
	for k := range silent {
		if positive[k] {
			sig.silentApplied++
		}
	}
	sig.silentNoise = sig.triggerSilentMeas - sig.silentApplied
	sig.evidenceEmptyUnique = len(evidenceEmpty)

	total := sig.triggerUsefulMeas + sig.triggerSilentMeas
	if total > 0 {
		// Match bash's `printf "%.0f"` semantics — round half to even is fine,
		// numbers involved are whole-percent anyway.
		num := float64(sig.triggerUsefulMeas+sig.silentApplied) * 100.0 / float64(total)
		sig.precisionPct = strconv.FormatFloat(num, 'f', 0, 64)
	} else {
		sig.precisionPct = "n/a"
	}

	return sig, nil
}

// parseErrorCount extracts the integer from "error_count:N,..." — the
// bash code uses `match($4, /error_count:[0-9]+/)`.
func parseErrorCount(s string) int {
	i := strings.Index(s, "error_count:")
	if i < 0 {
		return 0
	}
	rest := s[i+len("error_count:"):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, _ := strconv.Atoi(rest[:end])
	return n
}

// splitSessionMetrics demultiplexes the v1 and v2 session-metrics WAL
// shapes into a uniform (domains_csv, metrics_blob) pair so callers
// don't have to branch on format.
//
//	v1: col3 = "backend,testing"           col4 = "error_count:0,..."
//	v2: col3 = "domains:backend,testing,error_count:0,..."  col4 = session_id
//
// The detector keys on the `domains:` prefix in col3 — v1 emitted the
// raw comma-separated domain list with no key prefix, v2 always opens
// the structured blob with `domains:`. When the prefix is present we
// peel `domains:<csv>` off the front and return the remainder as the
// metrics blob; when absent we treat the inputs as v1 verbatim.
func splitSessionMetrics(col3, col4 string) (domainsCSV, metricsBlob string) {
	if !strings.HasPrefix(col3, "domains:") {
		return col3, col4
	}
	rest := col3[len("domains:"):]
	// Domains run until the first comma that introduces another
	// `key:value` pair. v2 always emits `domains:<csv>,error_count:...`,
	// so we look for the next "<word>:" substring.
	for i := 0; i < len(rest); i++ {
		if rest[i] != ',' {
			continue
		}
		// Peek ahead for `<alnum_underscore>+:`
		j := i + 1
		for j < len(rest) && (isWordByte(rest[j])) {
			j++
		}
		if j > i+1 && j < len(rest) && rest[j] == ':' {
			return rest[:i], rest[i+1:]
		}
	}
	// No metrics blob found — domains-only payload (defensive).
	return rest, ""
}

func isWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// --- Ambient set ----------------------------------------------------------

// collectAmbientSlugs walks memoryDir for .md files that contain a literal
// `precision_class: ambient` line (frontmatter or body — bash uses `grep`).
func collectAmbientSlugs(memoryDir string) (map[string]bool, error) {
	out := map[string]bool{}
	err := filepath.WalkDir(memoryDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			// Ignore per-file errors: bash `grep -r` with redirection to
			// /dev/null keeps going, so must we.
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		if hasAmbientLine(path) {
			out[strings.TrimSuffix(d.Name(), ".md")] = true
		}
		return nil
	})
	if err != nil {
		return out, nil // non-fatal; profile is observational
	}
	return out, nil
}

func hasAmbientLine(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if sc.Text() == "precision_class: ambient" {
			return true
		}
	}
	return false
}

// --- Weaknesses (mistakes) ------------------------------------------------

type weakness struct {
	slug       string
	severity   string
	recurrence int
	scope      string
}

// collectWeaknesses scans mistakesDir for .md files; for each, parses the
// frontmatter block between the first two `---` delimiters to extract
// recurrence / scope / severity. Emits only mistakes with recurrence > 0.
// Top 5 by recurrence descending, matches bash `sort -rn | head -5`.
func collectWeaknesses(mistakesDir string) ([]weakness, error) {
	entries, err := os.ReadDir(mistakesDir)
	if err != nil {
		// Bash tolerates missing directory silently.
		return nil, nil
	}
	all := make([]weakness, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		w := parseWeakness(filepath.Join(mistakesDir, e.Name()))
		if w.recurrence > 0 {
			w.slug = strings.TrimSuffix(e.Name(), ".md")
			all = append(all, w)
		}
	}

	// sort -rn on a numeric key is stable on POSIX `sort -s -rn` only;
	// GNU sort default is not guaranteed stable. Bash script uses `sort
	// -rn` which on GNU & BSD both sort descending by the first column.
	// Ties can surface in any order — we use a secondary key (slug) to
	// keep the output deterministic across runs. Bash output may vary
	// between ties; the parity check tolerates that via fixture design
	// (no ties).
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].recurrence != all[j].recurrence {
			return all[i].recurrence > all[j].recurrence
		}
		return all[i].slug < all[j].slug
	})
	if len(all) > 5 {
		all = all[:5]
	}
	return all, nil
}

func parseWeakness(path string) weakness {
	w := weakness{scope: "domain", severity: "?"}
	f, err := os.Open(path)
	if err != nil {
		return w
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	fm := 0
	for sc.Scan() {
		line := sc.Text()
		if line == "---" {
			fm++
			if fm == 2 {
				break
			}
			continue
		}
		if fm != 1 {
			continue
		}
		switch {
		case strings.HasPrefix(line, "recurrence:"):
			v := strings.TrimSpace(strings.TrimPrefix(line, "recurrence:"))
			n, _ := strconv.Atoi(v)
			w.recurrence = n
		case strings.HasPrefix(line, "scope:"):
			w.scope = strings.TrimSpace(strings.TrimPrefix(line, "scope:"))
		case strings.HasPrefix(line, "severity:"):
			w.severity = strings.TrimSpace(strings.TrimPrefix(line, "severity:"))
		}
	}
	return w
}

// --- Strengths (strategies) -----------------------------------------------

type strength struct {
	slug         string
	successCount int
}

func collectStrengths(strategiesDir string) ([]strength, error) {
	entries, err := os.ReadDir(strategiesDir)
	if err != nil {
		return nil, nil
	}
	all := make([]strength, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		s := parseStrength(filepath.Join(strategiesDir, e.Name()))
		s.slug = strings.TrimSuffix(e.Name(), ".md")
		all = append(all, s)
	}
	// Bash script sorts ALL strategies (including zero success_count), not
	// only those with success > 0. Match that for parity.
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].successCount != all[j].successCount {
			return all[i].successCount > all[j].successCount
		}
		return all[i].slug < all[j].slug
	})
	if len(all) > 5 {
		all = all[:5]
	}
	return all, nil
}

func parseStrength(path string) strength {
	s := strength{}
	f, err := os.Open(path)
	if err != nil {
		return s
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	fm := 0
	for sc.Scan() {
		line := sc.Text()
		if line == "---" {
			fm++
			if fm == 2 {
				break
			}
			continue
		}
		if fm != 1 {
			continue
		}
		if strings.HasPrefix(line, "success_count:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "success_count:"))
			n, _ := strconv.Atoi(v)
			s.successCount = n
		}
	}
	return s
}

// --- Calibration ----------------------------------------------------------

type domainErr struct {
	domain   string
	rate     float64
	sessions int
}

func summariseCalibration(total, errCount map[string]int) []domainErr {
	out := make([]domainErr, 0, len(total))
	for d, n := range total {
		rate := 0.0
		if n > 0 {
			rate = float64(errCount[d]) / float64(n)
		}
		out = append(out, domainErr{domain: d, rate: rate, sessions: n})
	}
	// Match bash `sort -rn`: primary key is the first column (rate) which
	// the awk END block prints as "%.2f". Tie-break on domain name to keep
	// output deterministic (bash's sort is unstable on ties).
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].rate != out[j].rate {
			return out[i].rate > out[j].rate
		}
		return out[i].domain < out[j].domain
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// --- Render ---------------------------------------------------------------

func renderProfile(ts string, sig *walSignals, weak []weakness, strong []strength, calib []domainErr) string {
	var b strings.Builder

	fmt.Fprintf(&b, "---\n")
	fmt.Fprintf(&b, "type: self-profile\n")
	fmt.Fprintf(&b, "generated: %s\n", ts)
	fmt.Fprintf(&b, "source: derived from .wal + mistakes/ + strategies/\n")
	fmt.Fprintf(&b, "---\n\n")

	fmt.Fprintf(&b, "# Self-Profile\n\n")
	fmt.Fprintf(&b, "**Do not edit manually.** This file is generated from WAL metrics.\n\n")

	fmt.Fprintf(&b, "## Meta-signals\n")
	fmt.Fprintf(&b, "| signal | count |\n")
	fmt.Fprintf(&b, "|---|---|\n")
	fmt.Fprintf(&b, "| total sessions (logged) | %d |\n", sig.totalSessions)
	fmt.Fprintf(&b, "| clean sessions (0 errors) | %d |\n", sig.cleanSessions)
	fmt.Fprintf(&b, "| outcome-positive (mistake not repeated) | %d |\n", sig.outcomePositive)
	fmt.Fprintf(&b, "| outcome-negative (mistake repeated) | %d |\n", sig.outcomeNegative)
	fmt.Fprintf(&b, "| strategy-used (clean session + strategy injected) | %d |\n", sig.strategyUsed)
	fmt.Fprintf(&b, "| strategy-gap (clean session, no strategy) | %d |\n", sig.strategyGap)
	fmt.Fprintf(&b, "| ambient activations (rules excluded from precision by design) | %d |\n", sig.ambientActivations)
	fmt.Fprintf(&b, "| trigger-useful measurable (referenced explicitly) | %d |\n", sig.triggerUsefulMeas)
	fmt.Fprintf(&b, "| silent-applied measurable (silent + outcome-positive) | %d |\n", sig.silentApplied)
	fmt.Fprintf(&b, "| silent-noise (silent, no application signal — **tuning targets**) | %d |\n", sig.silentNoise)
	fmt.Fprintf(&b, "| **measurable precision** (useful + applied) / (useful + silent) | **%s%%** |\n", sig.precisionPct)
	fmt.Fprintf(&b, "| evidence-empty rules (unique, cannot match trigger by design) | %d |\n", sig.evidenceEmptyUnique)
	fmt.Fprintf(&b, "| outcome-new (fresh mistakes recorded — learning rate) | %d |\n", sig.outcomeNewCount)
	fmt.Fprintf(&b, "| **ambient_fraction** (ambient activations / all reactive fires) | **%s** |\n", formatFractionPct(sig.ambientFractionDefined, sig.ambientFraction))
	fmt.Fprintf(&b, "| **corpus_fraction_with_active_bayesian** (slugs with ≥min outcomes / total) | **%s** (%d/%d) |\n", formatFractionPct(sig.corpusBayesianFractionDefined, sig.corpusBayesianFraction), sig.corpusBayesianActive, sig.corpusBayesianTotal)

	// Intuition signal — operational definition of v1.0 milestone
	// (ADR intuition-milestone.md). Ratio > 1.0 sustained over the
	// 30-day window means rules are applied more often than cited;
	// `memoryctl decisions review` reports `intuition-milestone:
	// reached` on sustained crossing.
	fmt.Fprintf(&b, "\n## Intuition signal\n")
	fmt.Fprintf(&b, "| signal | value |\n")
	fmt.Fprintf(&b, "|---|---|\n")
	fmt.Fprintf(&b, "| silent-applied (last 30d) | %d |\n", sig.silentAppliedRecent)
	fmt.Fprintf(&b, "| trigger-useful measurable (last 30d) | %d |\n", sig.triggerUsefulMeasRecent)
	fmt.Fprintf(&b, "| **silent_applied_to_useful_ratio_30d** | **%s** |\n", formatRatio(sig.intuitionRatioDefined, sig.intuitionRatio))
	fmt.Fprintf(&b, "| interpretation | %s |\n", interpretIntuitionRatio(sig.intuitionRatioDefined, sig.intuitionRatio))

	fmt.Fprintf(&b, "\n## Strengths (top strategies by success_count)\n")
	if len(strong) == 0 {
		fmt.Fprintf(&b, "_no data_\n")
	} else {
		for _, s := range strong {
			fmt.Fprintf(&b, "- `%s` (success: %d)\n", s.slug, s.successCount)
		}
	}

	fmt.Fprintf(&b, "\n## Weaknesses (top mistakes by recurrence)\n")
	if len(weak) == 0 {
		fmt.Fprintf(&b, "_no data_\n")
	} else {
		for _, w := range weak {
			marker := ""
			if w.scope == "universal" {
				marker = " 🔴"
			}
			fmt.Fprintf(&b, "- `%s` [%s, x%d, %s]%s\n", w.slug, w.severity, w.recurrence, w.scope, marker)
		}
	}

	fmt.Fprintf(&b, "\n🔴 = scope:universal (systemic tendency, not project-specific)\n")

	fmt.Fprintf(&b, "\n## Calibration (error-prone domains)\n")
	if len(calib) == 0 {
		fmt.Fprintf(&b, "_not enough data for calibration_\n")
	} else {
		for _, c := range calib {
			fmt.Fprintf(&b, "- `%s`: %s err/session avg (n=%d)\n", c.domain, formatRate(c.rate), c.sessions)
		}
	}

	fmt.Fprintf(&b, "\n---\n")
	fmt.Fprintf(&b, "_Accuracy improves with WAL volume. Refreshed on: clean-session, outcome-positive/negative, strategy-used._\n")

	return b.String()
}

// formatRate mimics awk's "%.2f" — trailing zeros preserved.
func formatRate(r float64) string {
	return strconv.FormatFloat(r, 'f', 2, 64)
}

// formatFractionPct renders a fraction in the same shape as
// `measurable_precision` ("NN%" or "n/a%"). The defined flag lets us
// distinguish a real 0% from "denominator was 0 — fraction undefined"
// without forcing a sentinel into the float.
func formatFractionPct(defined bool, frac float64) string {
	if !defined {
		return "n/a%"
	}
	return strconv.Itoa(int(frac*100+0.5)) + "%"
}

// formatRatio renders the intuition ratio with two decimal places
// when defined, "n/a" when the denominator (trigger-useful) is 0.
// Distinct from formatFractionPct because a ratio is unbounded above
// 1.0; rendering as a percentage would mislead.
func formatRatio(defined bool, r float64) string {
	if !defined {
		return "n/a"
	}
	return strconv.FormatFloat(r, 'f', 2, 64)
}

// interpretIntuitionRatio maps the ratio into a one-line operator
// reading. Boundaries are deliberately fuzzy — the metric is meant to
// signal a tier crossing, not a precise diagnostic.
func interpretIntuitionRatio(defined bool, r float64) string {
	if !defined {
		return "no measurable trigger-useful events in window — cold start"
	}
	switch {
	case r >= 1.0:
		return "✓ rules applied more often than cited (intuition tier — see intuition-milestone ADR)"
	case r >= 0.6:
		return "rules applied silently in a meaningful fraction of cases — approaching tier"
	case r >= 0.2:
		return "rules cited more than applied silently — typical mid-corpus state"
	default:
		return "citations dominate — rules not yet internalised"
	}
}
