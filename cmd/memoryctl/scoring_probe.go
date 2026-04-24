package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// scoringProbeResult is the JSON shape emitted by `memoryctl scoring-probe`.
// Mirrors the fields a fixture's expected.json assertion might check —
// in particular the dormancy markers the cold-start ADR exposes.
type scoringProbeResult struct {
	Slug               string  `json:"slug"`
	FilePath           string  `json:"file_path,omitempty"`
	BayesianEff        float64 `json:"bayesian_eff"`
	BayesianDormant    bool    `json:"bayesian_dormant"`
	BayesianReason     string  `json:"bayesian_reason,omitempty"`
	OutcomesInWindow   int     `json:"outcomes_in_window"`
	PositivesInWindow  int     `json:"positives_in_window"`
	NegativesInWindow  int     `json:"negatives_in_window"`
	WindowDays         int     `json:"window_days"`
	MinSamples         int     `json:"min_samples"`
	TfidfDormant       bool    `json:"tfidf_dormant"`
	TfidfVocabSize     int     `json:"tfidf_vocab_size"`
	TfidfMinVocab      int     `json:"tfidf_min_vocab"`
	WalInjectCount30d  int     `json:"wal_inject_count_30d"`
	WalSpreadDays      int     `json:"wal_spread_days"`
	WalLastInjectAge   int     `json:"wal_last_inject_age_days"`
	StrategyUsed       int     `json:"strategy_used_count"`
}

// runScoringProbe implements `memoryctl scoring-probe <slug>`.
//
// Intended use: fixture harness assertions (`expected.json`'s
// scoring_probe block) and one-off debugging ("why isn't this slug
// ranking?"). Reads the canonical WAL + tfidf-index, applies the same
// cold-start gate the hook pipeline uses, and emits a single JSON
// object on stdout.
//
// The probe does NOT mutate state — no WAL writes, no index rebuild.
// It's a read-only snapshot of what SessionStart would see for this
// slug against the current corpus state.
func runScoringProbe(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: memoryctl scoring-probe <slug>")
		os.Exit(2)
	}
	slug := args[0]

	memory := memoryDir()
	walPath := filepath.Join(memory, ".wal")
	tfidfPath := filepath.Join(memory, ".tfidf-index")

	minSamples := envIntDefault("HYPOMNEMA_BAYESIAN_MIN_SAMPLES", 5)
	windowDays := envIntDefault("HYPOMNEMA_OUTCOME_WINDOW_DAYS", 14)
	minVocab := envIntDefault("HYPOMNEMA_TFIDF_MIN_VOCAB", 100)

	now := time.Now()
	if ts := os.Getenv("HYPOMNEMA_TODAY"); ts != "" {
		if parsed, err := time.Parse("2006-01-02", ts); err == nil {
			now = parsed
		}
	}
	windowCutoff := now.AddDate(0, 0, -windowDays).Format("2006-01-02")
	thirtyDayCutoff := now.AddDate(0, 0, -30).Format("2006-01-02")

	var res scoringProbeResult
	res.Slug = slug
	res.WindowDays = windowDays
	res.MinSamples = minSamples
	res.TfidfMinVocab = minVocab
	res.FilePath = findMemoryFileForSlug(memory, slug)

	// Pass 1: count slug events against both cutoff windows.
	lastInject := ""
	injectDays := map[string]bool{}
	if f, err := os.Open(walPath); err == nil {
		defer f.Close()
		s := bufio.NewScanner(f)
		s.Buffer(make([]byte, 64*1024), 1024*1024)
		for s.Scan() {
			fields := strings.Split(s.Text(), "|")
			if len(fields) != 4 {
				continue
			}
			if fields[2] != slug {
				continue
			}
			date := fields[0]
			event := fields[1]

			// 30-day window: inject/spread stats.
			if date >= thirtyDayCutoff && event == "inject" {
				injectDays[date] = true
				if date > lastInject {
					lastInject = date
				}
				res.WalInjectCount30d++
			}
			if date >= thirtyDayCutoff && event == "strategy-used" {
				res.StrategyUsed++
			}

			// Gate window: outcome counts for Bayesian dormancy.
			if date >= windowCutoff {
				switch event {
				case "outcome-positive":
					res.PositivesInWindow++
				case "outcome-negative":
					res.NegativesInWindow++
				}
			}
		}
	}
	res.OutcomesInWindow = res.PositivesInWindow + res.NegativesInWindow
	res.WalSpreadDays = len(injectDays)
	if lastInject != "" {
		if lastT, err := time.Parse("2006-01-02", lastInject); err == nil {
			res.WalLastInjectAge = int(now.Sub(lastT).Hours() / 24)
			if res.WalLastInjectAge < 0 {
				res.WalLastInjectAge = 0
			}
		}
	}

	// Bayesian gate. Matches hooks/lib/score-records.sh logic.
	if res.OutcomesInWindow < minSamples {
		res.BayesianEff = 1.0
		res.BayesianDormant = true
		res.BayesianReason = fmt.Sprintf("need >=%d outcomes in %dd, have %d",
			minSamples, windowDays, res.OutcomesInWindow)
	} else {
		// (pos + 1) / (pos + neg + 2) — same Laplace pseudo-counts.
		pc := res.PositivesInWindow
		nc := res.NegativesInWindow
		res.BayesianEff = math.Round(float64(pc+1)/float64(pc+nc+2)*1000) / 1000
	}

	// TF-IDF vocab-count gate. Matches hooks/session-start.sh:MIN_TFIDF_VOCAB.
	res.TfidfVocabSize = countTfidfLines(tfidfPath)
	res.TfidfDormant = res.TfidfVocabSize < minVocab

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(res)
}

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

func countTfidfLines(path string) int {
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

// findMemoryFileForSlug walks the canonical memory subdirectories
// looking for <slug>.md. First hit wins; returns "" if absent.
// Matches the probe contract: we still emit a useful JSON object
// for phantom slugs (those that only appear in WAL events but
// have no file on disk), with file_path left empty.
func findMemoryFileForSlug(memoryDir, slug string) string {
	name := slug + ".md"
	subs := []string{
		"mistakes", "feedback", "knowledge", "strategies",
		"notes", "decisions", "projects", "continuity",
	}
	for _, sub := range subs {
		p := filepath.Join(memoryDir, sub, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
