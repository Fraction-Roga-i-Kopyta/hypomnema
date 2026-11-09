package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/doctor"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/stats"
)

const statsUsage = "Usage: memoryctl stats --json --scope session|fact|project|all [--session ID] [--project TAG] [<slug>]"

// statsArgs is the parsed command line of `memoryctl stats`.
type statsArgs struct {
	json                   bool
	scope, session, slug   string
	project                string
	sessionSet, projectSet bool
}

// runStats: memoryctl stats --json --scope session|fact|project|all [--session ID]
// [--project TAG] [<slug>]. Read-only JSON for the /memboard mod (the
// stats.Report contract, schema_version 1). Exit 0 ok, 1 runtime failure or
// no such fact, 2 misuse.
func runStats(args []string) {
	a, ok := parseStatsArgs(args)
	if !ok {
		return // --help
	}
	misuse := func(format string, v ...any) {
		fmt.Fprintf(os.Stderr, "memoryctl stats: "+format+"\n%s\n", append(v, statsUsage)...)
		os.Exit(2)
	}
	if !a.json {
		misuse("--json is required (there is no text output)")
	}
	sid := a.session
	if !a.sessionSet {
		sid = sessionIDFromEnv()
	}
	if sid != "" && !validSessionID(sid) {
		misuse("invalid session id %q", sid)
	}
	switch a.scope {
	case "":
		misuse("--scope session|fact|project|all is required")
	case "session":
		if a.slug != "" {
			misuse("unexpected argument %q", a.slug)
		}
		if a.projectSet {
			misuse("--project applies to --scope fact")
		}
		if sid == "" {
			misuse("--scope session needs a session id (--session, HYPOMNEMA_SESSION_ID or CLAUDE_CODE_SESSION_ID)")
		}
	case "fact":
		if a.slug == "" {
			misuse("--scope fact needs a <slug>")
		}
	case "project":
		if a.slug != "" {
			misuse("unexpected argument %q", a.slug)
		}
		if a.sessionSet {
			misuse("--session applies to --scope session|fact")
		}
	case "all":
		if a.slug != "" {
			misuse("unexpected argument %q", a.slug)
		}
		if a.sessionSet {
			misuse("--session applies to --scope session|fact")
		}
		if a.projectSet {
			misuse("--project applies to --scope project|fact")
		}
	default:
		misuse("unknown scope %q (session|fact|project|all)", a.scope)
	}

	r := resolveStore("")
	home, _ := os.UserHomeDir()
	day := today()
	rep := stats.Report{SchemaVersion: stats.SchemaVersion, Scope: a.scope, Today: day, Project: r.Project,
		Display: stats.Display(r.Project, home)}

	if a.scope == "project" {
		st, anchor := r.Store, r.Anchor
		if a.projectSet && a.project != r.Project {
			st = storeByTag(a.project)
			anchor = st.Root
		}
		u, err := stats.LoadUsage(filepath.Join(memoryDir(), ".wal"), day)
		if err != nil {
			fatalf("stats: %v", err)
		}
		recs, marks := sidecarView()
		ps := stats.BuildProject(st, native.Collect(claudeDir(), st), recs, u, marks,
			projectSessions(st.Project), doctorHealth(st, anchor), home)
		rep.ProjectStats = &ps
		writeStats(rep)
		return
	}

	if a.scope == "all" {
		u, err := stats.LoadUsage(filepath.Join(memoryDir(), ".wal"), day)
		if err != nil {
			fatalf("stats: %v", err)
		}
		recs, marks := sidecarView()
		stores := native.AllStores(configDir(), claudeDir(), r.Store)
		files := make(map[string][]native.MemFile, len(stores))
		for _, st := range stores {
			l, _ := native.List(st.Dir)
			for i := range l {
				l[i].Project = st.Project
			}
			files[st.Dir] = l
		}
		all := stats.BuildAll(stores, files, recs, u, marks, home)
		rep.All = &all
		writeStats(rep)
		return
	}

	// A missing fact exits before any WAL or sidecar work.
	var fact native.MemFile
	if a.scope == "fact" {
		fact, _ = resolveFactIn(a.slug, a.project)
	}
	u, err := stats.LoadUsage(filepath.Join(memoryDir(), ".wal"), day)
	if err != nil {
		fatalf("stats: %v", err)
	}
	recs, marks := sidecarView()

	switch a.scope {
	case "session":
		s := u.Session(sid)
		s.PromptInject = !promptInjectOff()
		stats.EnrichTop(&s, collectNative(r.Store), recs, u, marks, home)
		rep.Session = &s
	case "fact":
		k := stats.Key{Project: fact.Project, Slug: fact.Slug}
		var rec *sidecar.Record
		if rr, ok := recs[k]; ok {
			rec = &rr
		}
		fc := stats.FactCount{FactRef: stats.BuildFactRef(fact, rec, u, marks, home)}
		if sid != "" {
			fc.Injected, fc.Useful = u.SessionFact(sid, k)
		}
		rep.Fact = &fc
	}

	writeStats(rep)
}

func writeStats(rep stats.Report) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rep); err != nil {
		fatalf("stats: write: %v", err)
	}
}

// projectSessions is the set of parent session ids of project tag: the
// basenames of <configDir>/projects/<tag>/*.jsonl. A missing directory is
// the empty set (never nil: nil would mean "no restriction").
func projectSessions(tag string) map[string]bool {
	out := map[string]bool{}
	paths, _ := filepath.Glob(filepath.Join(configDir(), "projects", tag, "*.jsonl"))
	for _, p := range paths {
		out[strings.TrimSuffix(filepath.Base(p), ".jsonl")] = true
	}
	return out
}

// doctorHealth runs doctor in-process with the arguments `memoryctl doctor`
// uses and folds the checks into the panel's summary.
func doctorHealth(st native.Store, anchor string) stats.Health {
	h := stats.Health{Problems: []string{}}
	rep := doctor.Run(claudeDir(), memoryDir(), st, anchor)
	h.OK, h.Warn, h.Fail = rep.Counts()
	home, _ := os.UserHomeDir()
	for _, c := range rep.Checks {
		if c.Status == doctor.WARN || c.Status == doctor.FAIL {
			h.Problems = append(h.Problems, stats.Problem(scrubPaths(c.Detail, claudeDir(), home)))
		}
	}
	return h
}

// scrubPaths shortens absolute paths in a doctor detail (claude dir first,
// it is the longer one) so the text is shorter and independent of where
// the install lives, before it is cut to the display limit.
func scrubPaths(s, claude, home string) string {
	if claude != "" {
		s = strings.ReplaceAll(s, claude, "~/.claude")
	}
	if home != "" && home != "/" {
		s = strings.ReplaceAll(s, home, "~")
	}
	return s
}

// parseStatsArgs parses by hand (no flag package, like the other verbs).
// ok is false after --help; misuse exits 2.
func parseStatsArgs(args []string) (a statsArgs, ok bool) {
	value := func(i *int, name string) string {
		*i++
		if *i >= len(args) {
			fmt.Fprintf(os.Stderr, "memoryctl stats: %s needs a value\n%s\n", name, statsUsage)
			os.Exit(2)
		}
		return args[*i]
	}
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "-h" || arg == "--help":
			fmt.Print(usage)
			return a, false
		case arg == "--json":
			a.json = true
		case arg == "--scope":
			a.scope = value(&i, arg)
		case arg == "--session":
			a.session, a.sessionSet = value(&i, arg), true
		case arg == "--project":
			a.project, a.projectSet = value(&i, arg), true
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(os.Stderr, "memoryctl stats: unknown flag %q\n%s\n", arg, statsUsage)
			os.Exit(2)
		case a.slug != "":
			fmt.Fprintf(os.Stderr, "memoryctl stats: unexpected argument %q\n%s\n", arg, statsUsage)
			os.Exit(2)
		default:
			a.slug = arg
		}
	}
	return a, true
}

// validSessionID rejects ids that could never match a WAL session field:
// empty, a column delimiter, or a control character (the WAL writers
// sanitise those away).
func validSessionID(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c == '|' || c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// sidecarView reads the projected rows and the mark table. stats is
// read-only: a missing sidecar is not created (no rows, no marks — the
// builders fall back to the projection's defaults), and an unreadable one
// only warns.
func sidecarView() (map[stats.Key]sidecar.Record, []sidecar.Mark) {
	recs := map[stats.Key]sidecar.Record{}
	path := sidecarPath()
	if _, err := os.Stat(path); err != nil {
		return recs, nil
	}
	warn := func(err error) {
		fmt.Fprintf(os.Stderr, "memoryctl stats: warning: sidecar unreadable, using defaults: %v\n", err)
	}
	s, err := sidecar.Open(path)
	if err != nil {
		warn(err)
		return recs, nil
	}
	defer s.Close()
	all, err := s.All()
	if err != nil {
		warn(err)
		return recs, nil
	}
	marks, err := s.Marks()
	if err != nil {
		warn(err)
		marks = nil
	}
	return stats.IndexRecords(all), marks
}
