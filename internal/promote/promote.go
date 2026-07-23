// Package promote is the report-only promotion-ladder detector: it reads
// native frontmatter + WAL history and suggests which prose memories have
// outgrown prose — a recurring mistake wants a mechanical owner (hook/lint/
// test), an always-useful fact wants CLAUDE.md, an internalized or
// never-corroborated fact wants retirement. It never mutates anything; the
// human builds the new owner and closes the loop with `memoryctl retire
// --superseded-by`.
package promote

import (
	"bufio"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// Suggestion is one promotion candidate, ordered mechanical > claudemd >
// retire (highest-consequence owner wins when several signals fire).
type Suggestion struct {
	Slug     string
	Project  string
	Kind     string // "mechanical" | "claudemd" | "retire"
	Evidence string // human-readable signal summary
	Action   string // the closing command hint
}

// Thresholds tune the detector; Defaults matches the design spec.
type Thresholds struct {
	MinRecurrence   int     // mistakes repeated ≥ this → mechanical owner
	UsefulStreak    int     // last N classified sessions all useful → claudemd
	AblateHitRate   float64 // completed-ablation hit rate ≥ this → retire
	CandidateSilent int     // candidate: ≥ this silent, 0 useful → retire
}

// Defaults returns the spec thresholds.
func Defaults() Thresholds {
	return Thresholds{MinRecurrence: 2, UsefulStreak: 5, AblateHitRate: 0.75, CandidateSilent: 5}
}

// history is one slug's WAL story, keyed the same way reproject aggregates:
// qualified project\x1fslug when available, bare slug as legacy fallback.
type history struct {
	sessOrder  []string        // session ids in first-appearance (WAL append) order
	sessUseful map[string]bool // per-session verdict, useful wins (close fires per turn)
	ablStarted int
	ablStopped bool
	ablSkips   map[string]bool
	ablHits    map[string]bool
	confirmed  bool
}

// add records one trigger classification, dedup per session, useful wins —
// mirrors the sidecar agg's classify() semantics so promote and ranking
// read the same session verdicts.
func (h *history) add(sess string, useful bool) {
	if h.sessUseful == nil {
		h.sessUseful = map[string]bool{}
	}
	if _, seen := h.sessUseful[sess]; !seen {
		h.sessOrder = append(h.sessOrder, sess)
	}
	if useful {
		h.sessUseful[sess] = true
	} else if _, seen := h.sessUseful[sess]; !seen {
		h.sessUseful[sess] = false
	}
}

// ordered returns per-session verdicts in first-appearance order.
func (h *history) ordered() []bool {
	out := make([]bool, 0, len(h.sessOrder))
	for _, s := range h.sessOrder {
		out = append(out, h.sessUseful[s])
	}
	return out
}

// Analyze scans the WAL once and evaluates every fact against the four
// promotion signals, one suggestion per slug (highest consequence wins),
// ordered mechanical > claudemd > retire, then by slug.
func Analyze(files []native.MemFile, walPath string, th Thresholds) []Suggestion {
	hist := readHistory(walPath)
	var out []Suggestion
	for _, f := range files {
		bare := strings.TrimSuffix(f.Slug, ".md")
		h := hist[native.QKey(f.Project, bare)]
		if h == nil {
			h = hist[bare] // legacy bare-slug events
		}
		if h == nil {
			h = &history{}
		}
		if s, ok := judge(f, h, th); ok {
			out = append(out, s)
		}
	}
	rank := map[string]int{"mechanical": 0, "claudemd": 1, "retire": 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Kind] != rank[out[j].Kind] {
			return rank[out[i].Kind] < rank[out[j].Kind]
		}
		return out[i].Slug < out[j].Slug
	})
	return out
}

// judge evaluates one fact; signals are checked in consequence order so the
// first match is the highest-consequence suggestion (one per slug).
func judge(f native.MemFile, h *history, th Thresholds) (Suggestion, bool) {
	bare := strings.TrimSuffix(f.Slug, ".md")
	base := Suggestion{Slug: bare, Project: f.Project}

	if f.Type == "mistake" && f.Recurrence >= th.MinRecurrence {
		base.Kind = "mechanical"
		base.Evidence = "recurrence " + strconv.Itoa(f.Recurrence) + " — the mistake repeated despite the memory"
		base.Action = "build a hook/lint/test, then: memoryctl retire " + bare + " --superseded-by \"<owner>\""
		return base, true
	}

	// Useful streak: the last th.UsefulStreak classified sessions (first-
	// appearance order, one verdict per session, useful wins) were ALL useful.
	verdicts := h.ordered()
	if len(verdicts) >= th.UsefulStreak {
		streak := true
		for _, u := range verdicts[len(verdicts)-th.UsefulStreak:] {
			if !u {
				streak = false
				break
			}
		}
		if streak {
			base.Kind = "claudemd"
			base.Evidence = "useful in the last " + strconv.Itoa(th.UsefulStreak) + " classified sessions — needed every time"
			base.Action = "move the rule into CLAUDE.md, then: memoryctl retire " + bare + " --superseded-by CLAUDE.md"
			return base, true
		}
	}

	// Completed ablation with a high hit rate → already internalized.
	if h.ablStarted > 0 && (h.ablStopped || len(h.ablSkips) >= h.ablStarted) && len(h.ablSkips) > 0 {
		hits := 0
		for s := range h.ablHits {
			if h.ablSkips[s] {
				hits++
			}
		}
		if rate := float64(hits) / float64(len(h.ablSkips)); rate >= th.AblateHitRate {
			base.Kind = "retire"
			base.Evidence = "ablation " + strconv.Itoa(hits) + "/" + strconv.Itoa(len(h.ablSkips)) + " applied without injection — internalized"
			base.Action = "memoryctl retire " + bare + " --reason internalized"
			return base, true
		}
	}

	// Never-corroborated candidate.
	if f.Status == "candidate" && !h.confirmed {
		useful, silent := 0, 0
		for _, u := range h.sessUseful {
			if u {
				useful++
			} else {
				silent++
			}
		}
		if useful == 0 && silent >= th.CandidateSilent {
			base.Kind = "retire"
			base.Evidence = strconv.Itoa(silent) + " silent sessions, never corroborated"
			base.Action = "memoryctl retire " + bare + " --reason never-corroborated (or rewrite keywords/evidence)"
			return base, true
		}
	}
	return Suggestion{}, false
}

func readHistory(walPath string) map[string]*history {
	out := map[string]*history{}
	f, err := os.Open(walPath)
	if err != nil {
		return out
	}
	defer f.Close()
	get := func(key string) *history {
		h := out[key]
		if h == nil {
			h = &history{ablSkips: map[string]bool{}, ablHits: map[string]bool{}}
			out[key] = h
		}
		return h
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		parts := strings.SplitN(strings.TrimRight(sc.Text(), "\r"), "|", 4)
		if len(parts) != 4 {
			continue
		}
		event, target, sess := parts[1], parts[2], parts[3]
		project, rest, qualified := native.ParseQKey(target)
		slug := rest
		if i := strings.IndexAny(slug, ">:"); i >= 0 {
			slug = slug[:i]
		}
		slug = strings.TrimSuffix(slug, ".md")
		key := slug
		if qualified {
			key = native.QKey(project, slug)
		}
		h := get(key)
		switch event {
		case "trigger-useful":
			h.add(sess, true)
		case "trigger-silent":
			h.add(sess, false)
		case "candidate-confirmed":
			h.confirmed = true
		case "ablate-start":
			n := 5
			if i := strings.LastIndexByte(rest, ':'); i >= 0 {
				if v, err := strconv.Atoi(rest[i+1:]); err == nil && v > 0 {
					n = v
				}
			}
			h.ablStarted = n
			h.ablStopped = false
			h.ablSkips = map[string]bool{}
			h.ablHits = map[string]bool{}
		case "ablate-stop":
			h.ablStopped = true
		case "holdout-skip":
			h.ablSkips[sess] = true
		case "holdout-hit":
			h.ablHits[sess] = true
		}
	}
	return out
}
