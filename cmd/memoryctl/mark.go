package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

const markUsage = "Usage: memoryctl mark <slug> hide|less|clear|rewrite|split|merge|review|done [--project TAG]"

// runMark: memoryctl mark <slug> <directive> [--project TAG]. Writes one
// `mark` WAL row — target <qslug>:<directive>[:<arg>] — then refreshes the
// sidecar so the mark table shows it at once. The file itself is never
// touched. Interactive verb: exit 1 failure / 2 misuse.
func runMark(args []string) {
	var pos []string
	var project string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-h" || args[i] == "--help":
			fmt.Print(usage)
			return
		case args[i] == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "memoryctl mark: --project needs a value")
				os.Exit(2)
			}
			project = args[i]
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(os.Stderr, "memoryctl mark: unknown flag %q\n%s\n", args[i], markUsage)
			os.Exit(2)
		default:
			pos = append(pos, args[i])
		}
	}
	if len(pos) != 2 {
		fmt.Fprintln(os.Stderr, markUsage)
		os.Exit(2)
	}
	slug, directive := pos[0], pos[1]
	if !sidecar.IsDirective(directive) {
		fmt.Fprintf(os.Stderr, "memoryctl mark: unknown directive %q\n%s\n", directive, markUsage)
		os.Exit(2)
	}
	doMark(slug, directive, project)
	fmt.Printf("marked %s %s\n", slug, directive)
}

func doMark(slug, directive, project string) {
	f, st := resolveFactIn(slug, project)
	var arg string
	switch {
	case directive == "hide":
		// hide is per project: it hides the fact from this session's
		// project, so it only makes sense for facts that inject here.
		here := resolveStore("").Store.Project
		if f.Project != native.GlobalProject && f.Project != here {
			fmt.Fprintln(os.Stderr, "memoryctl mark: hide applies to global facts and facts of this project")
			os.Exit(2)
		}
		arg = here
	case sidecar.IsTask(directive):
		// The anchor: an edit of the file after the mark closes the task;
		// a status-only change (pin/unpin) does not.
		raw, err := os.ReadFile(f.Path)
		if err != nil {
			fatalf("mark: read %s: %v", f.Path, err)
		}
		arg = native.BodySHA(string(raw))
	}
	target := native.QKey(f.Project, strings.TrimSuffix(f.Slug, ".md")) + ":" + directive
	if arg != "" {
		target += ":" + arg
	}
	if err := emitLifecycleEvent("mark", target); err != nil {
		fatalf("mark: wal append: %v", err)
	}
	refreshSidecar(st)
}
