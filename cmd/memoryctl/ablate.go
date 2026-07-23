package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

const defaultHoldoutSessions = 5

// runAblate dispatches `memoryctl ablate ...`:
//
//	ablate <slug> [--sessions N]  start a holdout (default 5 sessions)
//	ablate stop <slug>            end it early
//	ablate report [<slug>]        summarize behaviour-without-injection
func runAblate(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: memoryctl ablate <slug> [--sessions N] | stop <slug> | report [<slug>]")
		os.Exit(2)
	}
	switch args[0] {
	case "stop":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "Usage: memoryctl ablate stop <slug>")
			os.Exit(2)
		}
		f := resolveFact(args[1])
		if err := emitLifecycleEvent("ablate-stop",
			native.QKey(f.Project, strings.TrimSuffix(f.Slug, ".md"))+":manual"); err != nil {
			fatalf("ablate stop: wal append: %v", err)
		}
		fmt.Printf("ablation stopped for %s\n", args[1])
	case "report":
		slug := ""
		if len(args) > 1 {
			slug = strings.TrimSuffix(args[1], ".md")
		}
		fmt.Print(ablateReport(filepath.Join(memoryDir(), ".wal"), slug))
	case "-h", "--help":
		fmt.Print(usage)
	default:
		slug := args[0]
		n := defaultHoldoutSessions
		for i := 1; i < len(args); i++ {
			if args[i] == "--sessions" && i+1 < len(args) {
				v, err := strconv.Atoi(args[i+1])
				if err != nil || v < 1 {
					fmt.Fprintf(os.Stderr, "memoryctl ablate: bad --sessions %q\n", args[i+1])
					os.Exit(2)
				}
				n = v
				i++
				continue
			}
			fmt.Fprintf(os.Stderr, "memoryctl ablate: unknown argument %q\n", args[i])
			os.Exit(2)
		}
		f := resolveFact(slug)
		if err := emitLifecycleEvent("ablate-start",
			fmt.Sprintf("%s:%d", native.QKey(f.Project, strings.TrimSuffix(f.Slug, ".md")), n)); err != nil {
			fatalf("ablate: wal append: %v", err)
		}
		fmt.Printf("holdout started for %s: %d would-have-injected sessions\n", slug, n)
	}
}

// holdoutRun is one slug's ablation history: the latest start + observations.
type holdoutRun struct {
	requested int
	skips     map[string]bool
	hits      map[string]bool
	stopped   string // "", "manual", "expired"
}

// ablateReport reads the WAL and renders one report per ablated slug (or
// just `only` when non-empty). Verdict thresholds: hit-rate ≥0.75 → likely
// internalized (retire/promote), ≤0.25 → memory is doing work, else
// inconclusive.
func ablateReport(walPath, only string) string {
	runs := map[string]*holdoutRun{}
	f, err := os.Open(walPath)
	if err != nil {
		return "no WAL — nothing ablated\n"
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		parts := strings.SplitN(strings.TrimRight(sc.Text(), "\r"), "|", 4)
		if len(parts) != 4 {
			continue
		}
		event, target, sess := parts[1], parts[2], parts[3]
		_, rest, _ := native.ParseQKey(target)
		slug := rest
		if i := strings.IndexAny(slug, ">:"); i >= 0 {
			slug = slug[:i]
		}
		slug = strings.TrimSuffix(slug, ".md")
		switch event {
		case "ablate-start":
			n := defaultHoldoutSessions
			if i := strings.LastIndexByte(rest, ':'); i >= 0 {
				if v, err := strconv.Atoi(rest[i+1:]); err == nil && v > 0 {
					n = v
				}
			}
			runs[slug] = &holdoutRun{requested: n,
				skips: map[string]bool{}, hits: map[string]bool{}}
		case "holdout-skip":
			if r := runs[slug]; r != nil {
				r.skips[sess] = true
			}
		case "holdout-hit":
			if r := runs[slug]; r != nil {
				r.hits[sess] = true
			}
		case "ablate-stop":
			if r := runs[slug]; r != nil {
				r.stopped = "manual"
				if strings.HasSuffix(rest, ":expired") {
					r.stopped = "expired"
				}
			}
		}
	}
	names := make([]string, 0, len(runs))
	for s := range runs {
		if only == "" || s == only {
			names = append(names, s)
		}
	}
	if len(names) == 0 {
		return "no ablation history\n"
	}
	sort.Strings(names)
	var b strings.Builder
	for _, s := range names {
		r := runs[s]
		observed := len(r.skips)
		hits := 0
		for sess := range r.hits {
			if r.skips[sess] {
				hits++
			}
		}
		state := "running"
		if r.stopped != "" {
			state = "stopped (" + r.stopped + ")"
		}
		fmt.Fprintf(&b, "## %s — %s\nholdout: %d sessions requested, %d observed (would-have-injected)\n",
			s, state, r.requested, observed)
		if observed == 0 {
			b.WriteString("no observations yet — the fact has not ranked into the top-K\n\n")
			continue
		}
		rate := float64(hits) / float64(observed)
		fmt.Fprintf(&b, "behavior without injection: applied %d/%d (holdout-hit)\n", hits, observed)
		switch {
		case rate >= 0.75:
			b.WriteString("verdict hint: rule applied without memory — likely internalized; consider \"memoryctl retire\" (or promote to CLAUDE.md if it must never regress)\n")
		case rate <= 0.25:
			b.WriteString("verdict hint: behavior degraded without injection — the memory is doing work; keep it\n")
		default:
			b.WriteString("verdict hint: inconclusive — extend with \"memoryctl ablate <slug> --sessions N\"\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}
