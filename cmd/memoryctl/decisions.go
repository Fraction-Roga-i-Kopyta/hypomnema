package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/decisions"
)

// runDecisions dispatches `memoryctl decisions <subcommand>`. First
// subcommand: `review`. Structure mirrors the `fts` / `dedup` dispatch
// in main.go so additions (`list`, `diff`, …) are one case each.
func runDecisions(args []string) {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch args[0] {
	case "review":
		runDecisionsReview(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "memoryctl: unknown decisions subcommand %q\n", args[0])
		os.Exit(2)
	}
}

// runDecisionsReview reads ADR files, evaluates their review-triggers
// against the current metric snapshot, and prints a one-line summary
// per trigger. Exit 0 if every trigger is `ok`, 1 if any `pressure`
// or `overdue` fired.
//
// ADR source directory precedence:
//   1. --dir <path>  (explicit)
//   2. docs/decisions under CWD, if present (project-level ADRs)
//   3. $CLAUDE_MEMORY_DIR/decisions  (personal ADRs)
//
// Rationale: a developer running `memoryctl decisions review` from
// inside a repo that ships project-level ADRs gets them reviewed
// alongside their personal self-profile metrics, which is the whole
// point of the feature. Explicit --dir overrides so scripts can pin.
func runDecisionsReview(args []string) {
	dir := ""
	jsonOut := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--dir":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "memoryctl decisions review: --dir requires a path")
				os.Exit(2)
			}
			dir = args[i+1]
			i++
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Print(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "memoryctl decisions review: unknown flag %q\n", a)
			os.Exit(2)
		}
	}

	if dir == "" {
		dir = resolveDecisionsDir()
	}
	if _, err := os.Stat(dir); err != nil {
		fmt.Fprintf(os.Stderr, "memoryctl decisions review: %s: %v\n", dir, err)
		os.Exit(2)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memoryctl decisions review: read %s: %v\n", dir, err)
		os.Exit(2)
	}

	now := resolveToday()
	snap, err := decisions.BuildSnapshot(memoryDir(), now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memoryctl decisions review: snapshot: %v\n", err)
		os.Exit(2)
	}

	// Also inject per-file ref_count so `source: file` triggers work.
	snap.FileRefCount = collectRefCounts(dir, entries)

	var results []decisions.Result
	var parseErrors []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "README.md" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		slug, triggers, err := decisions.ReadADR(path)
		if err != nil {
			parseErrors = append(parseErrors, err.Error())
			continue
		}
		if len(triggers) == 0 {
			// No structured triggers — emit a single `ok` line so the
			// operator sees the ADR was considered, not silently
			// skipped. Surfaces the adoption gap (N ADRs without
			// review-triggers: yet).
			results = append(results, decisions.Result{
				Slug: slug, Status: decisions.StatusOK,
				Message: "no review-triggers (prose only)",
			})
			continue
		}
		results = append(results, decisions.Evaluate(triggers, slug, snap)...)
	}
	// Deterministic ordering: pressure/overdue first, then by slug.
	sort.SliceStable(results, func(i, j int) bool {
		si, sj := results[i].Status, results[j].Status
		if si != sj {
			return statusPriority(si) < statusPriority(sj)
		}
		return results[i].Slug < results[j].Slug
	})

	if jsonOut {
		emitDecisionsJSON(os.Stdout, results, parseErrors)
	} else {
		emitDecisionsText(os.Stdout, results, parseErrors)
	}
	if anyFired(results) {
		os.Exit(1)
	}
}

func resolveDecisionsDir() string {
	// Project-level ADRs win if visible from CWD.
	if cwd, err := os.Getwd(); err == nil {
		proj := filepath.Join(cwd, "docs", "decisions")
		if info, err := os.Stat(proj); err == nil && info.IsDir() {
			return proj
		}
	}
	return filepath.Join(memoryDir(), "decisions")
}

func resolveToday() time.Time {
	if s := os.Getenv("HYPOMNEMA_TODAY"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t
		}
	}
	return time.Now()
}

// collectRefCounts walks the decisions directory, grabs each file's
// `ref_count:` value from frontmatter (one-shot grep), returns slug→
// int map. Best-effort: unreadable files contribute nothing.
func collectRefCounts(dir string, entries []os.DirEntry) map[string]int {
	out := map[string]int{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		path := filepath.Join(dir, e.Name())
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		// Trivial line scan — no need to pull in parse-memory here.
		buf := make([]byte, 4096)
		n, _ := f.Read(buf)
		f.Close()
		for _, line := range strings.Split(string(buf[:n]), "\n") {
			if strings.HasPrefix(line, "ref_count:") {
				v := strings.TrimSpace(strings.TrimPrefix(line, "ref_count:"))
				var ref int
				if _, err := fmt.Sscanf(v, "%d", &ref); err == nil {
					out[slug] = ref
				}
				break
			}
		}
	}
	return out
}

// statusPriority orders pressure/overdue first so the operator sees
// actionable items at the top.
func statusPriority(s decisions.Status) int {
	switch s {
	case decisions.StatusOverdue:
		return 0
	case decisions.StatusPressure:
		return 1
	case decisions.StatusSkipped:
		return 2
	case decisions.StatusOK:
		return 3
	}
	return 4
}

func anyFired(rs []decisions.Result) bool {
	for _, r := range rs {
		if r.Status == decisions.StatusPressure || r.Status == decisions.StatusOverdue {
			return true
		}
	}
	return false
}

func emitDecisionsText(w io.Writer, rs []decisions.Result, errs []string) {
	for _, r := range rs {
		if r.Message == "" {
			fmt.Fprintf(w, "[%s] %s\n", r.Status, r.Slug)
		} else {
			fmt.Fprintf(w, "[%s] %s — %s\n", r.Status, r.Slug, r.Message)
		}
	}
	for _, e := range errs {
		fmt.Fprintf(w, "[error] %s\n", e)
	}
}

func emitDecisionsJSON(w io.Writer, rs []decisions.Result, errs []string) {
	// Lightweight inline JSON to avoid encoding/json just for this.
	// Stable key order; escape quotes in messages.
	fmt.Fprint(w, "{\n  \"results\": [")
	for i, r := range rs {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, "\n    {\"slug\": %q, \"status\": %q, \"message\": %q}",
			r.Slug, r.Status.String(), r.Message)
	}
	fmt.Fprint(w, "\n  ],\n  \"parse_errors\": [")
	for i, e := range errs {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, "\n    %q", e)
	}
	fmt.Fprint(w, "\n  ]\n}\n")
}
