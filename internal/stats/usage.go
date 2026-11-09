package stats

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// Key identifies a fact in the WAL: project tag plus slug with ".md".
// Legacy unqualified targets keep Project == "" for the caller to resolve.
type Key struct{ Project, Slug string }

type row struct {
	date   string
	event  string
	key    Key
	parent string // session id with any ":<agentId>" suffix removed
}

type cite struct {
	date   string
	useful bool
}

type citeID struct {
	key    Key
	parent string
}

// Usage is a one-pass aggregate of the WAL relative to a fixed "today".
type Usage struct {
	today time.Time
	rows  []row // inject / recall / cite-* rows in WAL order
	cites map[citeID]cite
}

// LoadUsage reads the WAL once. A missing WAL yields an empty Usage.
func LoadUsage(walPath, today string) (*Usage, error) {
	t, err := time.Parse("2006-01-02", today)
	if err != nil {
		return nil, fmt.Errorf("stats.LoadUsage: bad today %q: %w", today, err)
	}
	u := &Usage{today: t, cites: map[citeID]cite{}}
	f, err := os.Open(walPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return u, nil
		}
		return nil, fmt.Errorf("stats.LoadUsage: open: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		p := strings.SplitN(sc.Text(), "|", 4)
		if len(p) < 4 {
			continue
		}
		switch p[1] {
		case "inject", "recall", "cite-useful", "cite-silent", "cite-undelivered":
		default:
			continue
		}
		proj, slug, _ := native.ParseQKey(p[2])
		parent := p[3]
		if i := strings.IndexByte(parent, ':'); i >= 0 {
			parent = parent[:i]
		}
		r := row{date: p[0], event: p[1], key: Key{proj, slug}, parent: parent}
		u.rows = append(u.rows, r)
		if p[1] == "cite-useful" || p[1] == "cite-silent" {
			id := citeID{r.key, parent}
			c, ok := u.cites[id]
			useful := p[1] == "cite-useful"
			switch {
			case !ok:
				u.cites[id] = cite{r.date, useful}
			case useful && !c.useful: // useful wins over silent
				u.cites[id] = cite{r.date, true}
			case useful == c.useful && r.date < c.date:
				u.cites[id] = cite{r.date, useful}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("stats.LoadUsage: scan: %w", err)
	}
	return u, nil
}

// age returns whole days between date and today; ok is false for a bad date.
func (u *Usage) age(date string) (int, bool) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0, false
	}
	return int(u.today.Sub(t).Hours() / 24), true
}

func (u *Usage) inWindow(date string, days int) bool {
	a, ok := u.age(date)
	return ok && a >= 0 && a < days
}

// Fact returns the injection-row count and the count of sessions with a
// useful citation over the last 30 days.
func (u *Usage) Fact(k Key) (injected30d, useful30d int) {
	for _, r := range u.rows {
		if r.key == k && r.event == "inject" && u.inWindow(r.date, 30) {
			injected30d++
		}
	}
	for id, c := range u.cites {
		if id.key == k && c.useful && u.inWindow(c.date, 30) {
			useful30d++
		}
	}
	return
}

// Session aggregates rows of session sid and its subagents.
func (u *Usage) Session(sid string) SessionStats {
	s := SessionStats{SessionID: sid, Delivered: []FactKey{}, UsefulFacts: []FactKey{}, Top: []FactCount{}}
	injCount := map[Key]int{}
	seenInj := map[Key]bool{}
	seenRec := map[Key]bool{}
	lastIdx := map[Key]int{}
	for i, r := range u.rows {
		if r.parent != sid {
			continue
		}
		switch r.event {
		case "inject":
			s.Injected++
			injCount[r.key]++
			seenInj[r.key] = true
			lastIdx[r.key] = i
		case "recall":
			seenRec[r.key] = true
			lastIdx[r.key] = i
		case "cite-undelivered":
			s.Undelivered++
		}
	}
	s.InjectedFacts = len(seenInj)
	s.Recalled = len(seenRec)

	usefulKeys := map[Key]bool{}
	for id, c := range u.cites {
		if id.parent != sid {
			continue
		}
		if c.useful {
			s.Useful++
			usefulKeys[id.key] = true
		} else {
			s.Silent++
		}
	}
	if s.Useful+s.Silent > 0 {
		s.UsefulShare = float64(s.Useful) / float64(s.Useful+s.Silent)
	}

	delivered := make([]Key, 0, len(lastIdx))
	for k := range lastIdx {
		delivered = append(delivered, k)
	}
	sort.Slice(delivered, func(i, j int) bool {
		if lastIdx[delivered[i]] != lastIdx[delivered[j]] {
			return lastIdx[delivered[i]] > lastIdx[delivered[j]]
		}
		return delivered[i].Slug < delivered[j].Slug
	})
	for _, k := range delivered {
		s.Delivered = append(s.Delivered, FactKey(k))
	}
	uk := sortedKeys(usefulKeys)
	for _, k := range uk {
		s.UsefulFacts = append(s.UsefulFacts, FactKey(k))
	}

	top := sortedKeys(seenInj)
	sort.SliceStable(top, func(i, j int) bool { return injCount[top[i]] > injCount[top[j]] })
	if len(top) > 10 {
		top = top[:10]
	}
	for _, k := range top {
		fc := FactCount{Injected: injCount[k]}
		fc.Project, fc.Slug = k.Project, k.Slug
		fc.Marks = []string{}
		if usefulKeys[k] {
			fc.Useful = 1
		}
		s.Top = append(s.Top, fc)
	}
	return s
}

func sortedKeys(m map[Key]bool) []Key {
	ks := make([]Key, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool {
		if ks[i].Slug != ks[j].Slug {
			return ks[i].Slug < ks[j].Slug
		}
		return ks[i].Project < ks[j].Project
	})
	return ks
}

// Days returns n daily buckets ending today, oldest first. A non-nil sessions
// set restricts rows to those parent sessions.
func (u *Usage) Days(sessions map[string]bool, n int) []Day {
	days := make([]Day, n)
	idx := map[string]int{}
	for i := 0; i < n; i++ {
		d := u.today.AddDate(0, 0, -(n - 1 - i)).Format("2006-01-02")
		days[i] = Day{Date: d}
		idx[d] = i
	}
	ok := func(parent string) bool { return sessions == nil || sessions[parent] }
	for _, r := range u.rows {
		if r.event != "inject" || !ok(r.parent) {
			continue
		}
		if i, found := idx[r.date]; found {
			days[i].Injected++
		}
	}
	for id, c := range u.cites {
		if !ok(id.parent) {
			continue
		}
		if i, found := idx[c.date]; found {
			if c.useful {
				days[i].Useful++
			} else {
				days[i].Silent++
			}
		}
	}
	return days
}

// DistinctInjected counts distinct facts injected within the last days days.
func (u *Usage) DistinctInjected(days int) int {
	seen := map[Key]bool{}
	for _, r := range u.rows {
		if r.event == "inject" && u.inWindow(r.date, days) {
			seen[r.key] = true
		}
	}
	return len(seen)
}

// DistinctUseful counts distinct facts with a useful citation in the window.
func (u *Usage) DistinctUseful(days int) int {
	n := 0
	for _, ks := range u.usefulKeys(days) {
		n += len(ks)
	}
	return n
}

// UsefulByProject maps project tag to distinct useful facts in the window.
func (u *Usage) UsefulByProject(days int) map[string]int {
	out := map[string]int{}
	for p, ks := range u.usefulKeys(days) {
		out[p] = len(ks)
	}
	return out
}

func (u *Usage) usefulKeys(days int) map[string]map[Key]bool {
	out := map[string]map[Key]bool{}
	for id, c := range u.cites {
		if c.useful && u.inWindow(c.date, days) {
			if out[id.key.Project] == nil {
				out[id.key.Project] = map[Key]bool{}
			}
			out[id.key.Project][id.key] = true
		}
	}
	return out
}
