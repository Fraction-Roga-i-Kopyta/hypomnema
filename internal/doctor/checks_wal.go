package doctor

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

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

// closingEvents are the WAL event types that signal "Stop hook reached a
// classification pass for this session_id" — i.e. the behavioural quantum
// closed in the TFS sense (inject → some posterior). See
// docs/notes/measurement-bias.md for why session-metrics is intentionally
// excluded (it doesn't carry session_id in column 4, a FORMAT.md §5.2
// violation).
//
// cite-useful / cite-silent / cite-none are the v2.14+ citation-based
// closing signals (close writes these now, never trigger-*); the
// trigger-*/outcome-* entries stay so sessions closed before that cutover
// still count as closed — old WAL history must not suddenly read as open.
var closingEvents = map[string]bool{
	"trigger-useful":       true,
	"trigger-silent":       true,
	"trigger-silent-retro": true, // v0.13+ retroactive classification
	"outcome-positive":     true,
	"outcome-negative":     true,
	"clean-session":        true,
	"cite-useful":          true,
	"cite-silent":          true,
	"cite-none":            true,
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

// citationSignalWindowDays bounds the recent-activity window
// checkCitationSignal examines — a short horizon so the check reacts to a
// citation channel that just broke, not one that broke months ago.
const citationSignalWindowDays = 7

// citationSignalMinSessions is the minimum count of distinct inject-carrying
// sessions in the window before an all-silent outcome is worth a WARN. Below
// this, low volume alone could explain zero citations.
const citationSignalMinSessions = 3

// citationSubagentMinSessions / citationSubagentMinRate: subagent sessions
// are judged on their own — they cite in a final report, not to the user,
// and they outnumber parent sessions ~30:1, so mixing the two hid both.
const (
	citationSubagentMinSessions = 20
	citationSubagentMinRate     = 0.05
)

// isSubagentKey: v2.15 keys a subagent's observations <session>:<agent>;
// parent session ids never contain ':'.
func isSubagentKey(sess string) bool { return strings.Contains(sess, ":") }

// checkCitationSignal watches for the citation channel going dark: facts get
// injected but the model's <cc-memory> citations never reach the transcript
// (or are never written), so close has nothing to classify as useful. Distinct
// from open_quanta_last_30d, which asks "did a closing event fire at all" —
// this asks "when it fired, did any citation land."
//
// A row with ANY cite- prefix (cite-useful, cite-silent, cite-none,
// cite-undelivered) proves the v2.14+ citation channel has run at least
// once — cite-none alone (close reached a classification pass but resolved
// no delivered citation) is exactly the "channel went dark" case this check
// exists to catch, so it must count as history the same as the others.
// Pre-v2.14 WAL history carries no cite-* rows at all, so an install that
// hasn't seen a Stop hook since the cutover reports OK rather than a false
// WARN.
//
// The window stats stay narrower: N is sessions in the window that injected
// something AND carry a cite-* row — i.e. sessions a citation-aware close
// actually classified. Every such close that saw injections writes one of
// cite-useful/cite-silent/cite-none, so this excludes exactly the sessions
// closed before the cutover, whose model was never told to cite; counting
// them would raise a false WARN for the first week after an upgrade. M is
// the subset of N that ALSO carries a cite-useful — the intersection, not a
// raw cite-useful session count. A session with a citation but no inject
// this window (e.g. recall-only) must not inflate M; it never showed the
// channel doing its job on an injected fact.
//
// N and M are computed separately for parent sessions and for subagent keys
// (<session>:<agent>). The parent WARN rule is unchanged (N >= 3 and M == 0);
// subagents WARN at >= 20 sessions with under 5% cited. Parent fact counts
// are one observation per (slug, session), useful winning.
func checkCitationSignal(walPath string, now time.Time) Check {
	const name = "citation_signal"
	f, err := os.Open(walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Check{Name: name, Status: OK, Detail: "no citation data yet (pre-v2.14 history)"}
		}
		return Check{Name: name, Status: WARN, Detail: err.Error()}
	}
	defer f.Close()

	cutoff := now.AddDate(0, 0, -citationSignalWindowDays).Format("2006-01-02")
	hasCiteHistory := false
	injSess := map[string]bool{}
	classifiedSess := map[string]bool{}
	usefulSess := map[string]bool{}
	factSess := map[string]bool{} // parent "slug|session" → cited useful

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "|", 4)
		if len(parts) < 4 {
			continue
		}
		event, sess := parts[1], parts[3]
		if strings.HasPrefix(event, "cite-") {
			hasCiteHistory = true
		}
		if parts[0] < cutoff {
			continue
		}
		if strings.HasPrefix(event, "cite-") {
			classifiedSess[sess] = true
		}
		switch event {
		case "inject":
			injSess[sess] = true
		case "cite-useful":
			usefulSess[sess] = true
			if !isSubagentKey(sess) {
				factSess[parts[2]+"|"+sess] = true
			}
		case "cite-silent":
			if !isSubagentKey(sess) {
				if _, seen := factSess[parts[2]+"|"+sess]; !seen {
					factSess[parts[2]+"|"+sess] = false
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Check{Name: name, Status: WARN, Detail: "scan error: " + err.Error()}
	}
	factUseful, factSilent := 0, 0
	for _, useful := range factSess {
		if useful {
			factUseful++
		} else {
			factSilent++
		}
	}

	if !hasCiteHistory {
		return Check{Name: name, Status: OK, Detail: "no citation data yet (pre-v2.14 history)"}
	}
	type tally struct{ n, m int }
	var par, sub tally
	for sess := range injSess {
		if !classifiedSess[sess] {
			continue
		}
		t := &par
		if isSubagentKey(sess) {
			t = &sub
		}
		t.n++
		if usefulSess[sess] {
			t.m++
		}
	}
	detail := fmt.Sprintf("parent %d/%d sessions cited (facts %d useful / %d silent); subagent %d/%d sessions cited (%dd)",
		par.m, par.n, factUseful, factSilent, sub.m, sub.n, citationSignalWindowDays)
	if par.n >= citationSignalMinSessions && par.m == 0 {
		return Check{Name: name, Status: WARN,
			Detail: fmt.Sprintf("facts were injected in %d parent session(s) over %d days but never cited — the usefulness signal is off (the model is not citing, or citations are not reaching the transcript); see TROUBLESHOOTING. %s",
				par.n, citationSignalWindowDays, detail)}
	}
	if sub.n >= citationSubagentMinSessions && float64(sub.m) < citationSubagentMinRate*float64(sub.n) {
		return Check{Name: name, Status: WARN,
			Detail: "subagents rarely cite injected facts in their final report — " + detail +
				"; see TROUBLESHOOTING (may persist up to 7 days after upgrading)"}
	}
	return Check{Name: name, Status: OK, Detail: detail}
}
