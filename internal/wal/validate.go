package wal

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// ValidationFailure describes one row that violates the WAL grammar
// invariants. LineNum is 1-indexed; Line is the raw row content
// (newline stripped).
type ValidationFailure struct {
	LineNum int
	Reason  string
	Line    string
}

// ValidationReport summarises a Validate run. Failures is empty on a
// clean WAL; in that case Total == Passing.
type ValidationReport struct {
	Total    int
	Passing  int
	Failures []ValidationFailure
}

// Clean reports whether the WAL passed every check.
func (r ValidationReport) Clean() bool { return len(r.Failures) == 0 }

var (
	dateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	eventRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// ErrWALMissing is returned when the file does not exist. Distinct
// from a malformed WAL so callers can choose between "no WAL is
// fine" (fresh install) and "WAL should exist by now".
var ErrWALMissing = errors.New("wal: file does not exist")

// Validate reads walPath and checks every row against the four-column
// grammar invariant from FORMAT.md § 5. Returns a ValidationReport
// listing each failure encountered. The function does not exit early;
// a malformed row at the top of the file does not prevent later rows
// from being checked.
//
// Header line (`# hypomnema-wal v2`) — see ADR `wal-header-v2-marker`
// (planned) — is tolerated when present and not counted as a row.
//
// Failures detected:
//   - column count != 4 (split by `|`)
//   - date does not match YYYY-MM-DD
//   - event does not match `^[a-z][a-z0-9-]*$`
//   - target contains `|` (impossible after correct split, but
//     defensive — embedded `\n` would split a row in two and we'd
//     never see it as one line; the bufio.Scanner already drops the
//     trailing `\n`)
//   - session is empty (writers always sanitise to a non-empty string;
//     an empty session indicates a corrupted or malicious row)
//
// Empty rows are silently skipped (zero columns). Whitespace-only
// rows are reported as malformed.
func Validate(walPath string) (ValidationReport, error) {
	f, err := os.Open(walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ValidationReport{}, ErrWALMissing
		}
		return ValidationReport{}, fmt.Errorf("wal: open %s: %w", walPath, err)
	}
	defer f.Close()

	report := ValidationReport{}
	sc := bufio.NewScanner(f)
	// WAL lines can be long if a target carries a large blob (rare —
	// the bash side capped most blob fields at 4 KiB long ago — but
	// session-metrics v2 with many domains can approach that). Bump
	// the buffer to avoid silent truncation.
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)

	lineNum := 0
	for sc.Scan() {
		lineNum++
		line := sc.Text()
		if line == "" {
			continue
		}
		// Tolerate optional v2 header on the very first non-empty line.
		// Anything after row 1 that starts with `#` is a malformed row
		// (events never start with `#`).
		if lineNum == 1 && strings.HasPrefix(line, "# hypomnema-wal ") {
			continue
		}

		report.Total++
		fields := strings.Split(line, "|")
		if len(fields) != 4 {
			report.Failures = append(report.Failures, ValidationFailure{
				LineNum: lineNum,
				Reason:  fmt.Sprintf("column count = %d, want 4", len(fields)),
				Line:    line,
			})
			continue
		}

		date, event, target, session := fields[0], fields[1], fields[2], fields[3]

		if !dateRe.MatchString(date) {
			report.Failures = append(report.Failures, ValidationFailure{
				LineNum: lineNum,
				Reason:  fmt.Sprintf("date %q does not match YYYY-MM-DD", date),
				Line:    line,
			})
			continue
		}
		if !eventRe.MatchString(event) {
			report.Failures = append(report.Failures, ValidationFailure{
				LineNum: lineNum,
				Reason:  fmt.Sprintf("event %q does not match [a-z][a-z0-9-]*", event),
				Line:    line,
			})
			continue
		}
		// strings.Split already separated on `|`; if any column had a
		// literal `|`, the row would have failed the column count check
		// above. Surface remaining target hazards: \r left over from
		// CRLF-naïve writers (writers sanitise CR to `_` per E1, but
		// historical WAL rows could carry it).
		if strings.ContainsAny(target, "\r") {
			report.Failures = append(report.Failures, ValidationFailure{
				LineNum: lineNum,
				Reason:  "target contains a carriage-return — sanitise via pathutil.SlugFromPath",
				Line:    line,
			})
			continue
		}
		if session == "" {
			report.Failures = append(report.Failures, ValidationFailure{
				LineNum: lineNum,
				Reason:  "session is empty — writers always sanitise to a non-empty string (use \"unknown\")",
				Line:    line,
			})
			continue
		}

		report.Passing++
	}
	if err := sc.Err(); err != nil {
		return report, fmt.Errorf("wal: scan %s: %w", walPath, err)
	}
	return report, nil
}
