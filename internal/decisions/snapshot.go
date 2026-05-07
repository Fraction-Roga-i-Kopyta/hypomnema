package decisions

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ShadowMissWindowDays is the rolling window over which shadow_miss
// events are averaged to compute `shadow_miss_ratio`. The v0.11 plan
// picks 14 days — balances responsiveness against session-count noise.
// Hardcoded initially; if dogfooding data later shows the window needs
// tuning, promote to a .config.sh knob.
const ShadowMissWindowDays = 14

// BuildSnapshot assembles a Snapshot for Evaluate by reading
// ~/.claude/memory/self-profile.md (metric values) and .wal (shadow
// miss ratio). A missing file is not an error — the corresponding
// metric is simply absent from Snapshot.Metrics and Evaluate returns
// StatusSkipped for triggers that need it.
//
// `now` is passed in (not `time.Now()`) so tests can freeze it and
// callers can inherit HYPOMNEMA_TODAY semantics.
func BuildSnapshot(memoryDir string, now time.Time) (Snapshot, error) {
	snap := Snapshot{
		Today:        now.Format("2006-01-02"),
		Metrics:      map[string]float64{},
		FileRefCount: map[string]int{},
	}

	if err := loadSelfProfileMetrics(filepath.Join(memoryDir, "self-profile.md"), snap.Metrics); err != nil {
		return snap, err
	}
	if err := loadShadowMissRatio(filepath.Join(memoryDir, ".wal"), now, snap.Metrics); err != nil {
		return snap, err
	}
	return snap, nil
}

// --- self-profile parsing ---

// Line shapes the self-profile.md generator emits (see bin/memory-
// self-profile.sh and internal/profile). We read the handful of
// metrics v0.11 cares about; anything else is forward-compat-ignored.
var (
	// Self-profile generators (Go + bash) emit metrics as markdown
	// table rows:
	//   | **measurable precision** ... | **67%** |
	// The previous regex stopped at `[^|]*?` and never crossed the
	// pipe into the value cell. Match digits anywhere on the line —
	// each metric label appears once, so a line-wide search is
	// unambiguous and survives both table and inline formats.
	// "n/a%" cells correctly produce no match (metric undefined).
	reMeasurablePrecision    = regexp.MustCompile(`(?i)measurable precision.*?(\d+(?:\.\d+)?)\s*%`)
	reAmbientFraction        = regexp.MustCompile(`(?i)ambient_fraction.*?(\d+(?:\.\d+)?)\s*%`)
	reCorpusBayesianFraction = regexp.MustCompile(`(?i)corpus_fraction_with_active_bayesian.*?(\d+(?:\.\d+)?)\s*%`)
	// "top recurrence: 6" / "recurrence peak: 6" — accept a few wordings.
	reRecurrenceTop = regexp.MustCompile(`(?i)(?:top recurrence|recurrence peak|highest recurrence)[^0-9]+(\d+)`)
)

func loadSelfProfileMetrics(path string, out map[string]float64) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("self-profile: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := reMeasurablePrecision.FindStringSubmatch(line); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				// self-profile emits whole percents (53%); normalize
				// to 0..1 so triggers compare like "< 0.40".
				out["measurable_precision"] = v / 100.0
			}
		}
		if m := reAmbientFraction.FindStringSubmatch(line); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				out["ambient_fraction"] = v / 100.0
			}
		}
		if m := reCorpusBayesianFraction.FindStringSubmatch(line); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				out["corpus_fraction_with_active_bayesian"] = v / 100.0
			}
		}
		if m := reRecurrenceTop.FindStringSubmatch(line); m != nil {
			if v, err := strconv.Atoi(m[1]); err == nil {
				out["recurrence_top_mistake"] = float64(v)
			}
		}
	}
	return sc.Err()
}

// --- WAL-derived shadow_miss_ratio ---

// loadShadowMissRatio scans .wal for `shadow-miss` and `trigger-match`
// events inside the last ShadowMissWindowDays. Ratio is
//
//   shadow_miss / (trigger_match + shadow_miss)
//
// i.e. "fraction of would-have-been-useful recall the substring
// pipeline missed". A window with zero prompts returns absent metric
// (Evaluate → Skipped).
func loadShadowMissRatio(walPath string, now time.Time, out map[string]float64) error {
	f, err := os.Open(walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("wal: %w", err)
	}
	defer f.Close()
	cutoff := now.AddDate(0, 0, -ShadowMissWindowDays).Format("2006-01-02")
	var matched, missed int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 2 {
			continue
		}
		if parts[0] < cutoff {
			continue
		}
		switch parts[1] {
		case "trigger-match":
			matched++
		case "shadow-miss":
			missed++
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	total := matched + missed
	if total == 0 {
		return nil // no data — leave metric absent
	}
	out["shadow_miss_ratio"] = float64(missed) / float64(total)
	return nil
}
