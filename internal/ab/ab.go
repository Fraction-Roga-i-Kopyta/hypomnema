// Package ab is the A/B null-hypothesis harness: it replays the historical
// WAL and measures whether the ranker concentrates each session's
// trigger-useful slugs into a small budget better than random selection.
// Signals are aggregated under a strict temporal holdout (events before the
// session's date only) so the replay is leakage-free.
package ab

import (
	"bufio"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Event is one parsed WAL row: DATE|KIND|SLUG|FIELD4.
type Event struct {
	Date  string
	Kind  string
	Slug  string
	Field string // session id for most events; aggregated count for inject-agg
}

// EvalSession is a historical session usable as ground truth: its date and
// the set of slugs that proved useful in it.
type EvalSession struct {
	Date   string
	Useful map[string]bool
}

// Signal holds the temporal-holdout aggregates for one slug.
type Signal struct {
	RefCount   int
	Pos        int
	Neg        int
	LastInject string
}

// ParseWAL reads a WAL file into events (file order). Blank/comment/malformed
// lines are skipped. A missing file yields (nil, nil).
func ParseWAL(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}
		out = append(out, Event{Date: parts[0], Kind: parts[1], Slug: parts[2], Field: parts[3]})
	}
	return out, sc.Err()
}

// EvalSessions groups trigger-useful events by session id (Field), attaching
// each session's earliest trigger-useful date. Returned sorted by date then
// useful-set size for determinism.
func EvalSessions(events []Event) []EvalSession {
	type acc struct {
		date   string
		useful map[string]bool
	}
	bySession := map[string]*acc{}
	for _, e := range events {
		if e.Kind != "trigger-useful" {
			continue
		}
		a := bySession[e.Field]
		if a == nil {
			a = &acc{date: e.Date, useful: map[string]bool{}}
			bySession[e.Field] = a
		}
		if e.Date < a.date {
			a.date = e.Date
		}
		a.useful[e.Slug] = true
	}
	out := make([]EvalSession, 0, len(bySession))
	for _, a := range bySession {
		out = append(out, EvalSession{Date: a.date, Useful: a.useful})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return len(out[i].Useful) < len(out[j].Useful)
	})
	return out
}

// SignalsBefore aggregates per-slug signals from events with Date strictly
// before `date`. inject-agg's Field is its aggregated count (wal-compact.sh).
func SignalsBefore(events []Event, date string) map[string]Signal {
	out := map[string]Signal{}
	for _, e := range events {
		if e.Date >= date {
			continue
		}
		s := out[e.Slug]
		switch e.Kind {
		case "inject":
			s.RefCount++
			if e.Date > s.LastInject {
				s.LastInject = e.Date
			}
		case "inject-agg":
			n, err := strconv.Atoi(e.Field)
			if err != nil || n < 1 {
				n = 1
			}
			s.RefCount += n
			if e.Date > s.LastInject {
				s.LastInject = e.Date
			}
		case "outcome-positive":
			s.Pos++
		case "outcome-negative":
			s.Neg++
		}
		out[e.Slug] = s
	}
	return out
}

// CandidatePool returns the distinct slugs appearing in events, sorted.
func CandidatePool(events []Event) []string {
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.Slug] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
