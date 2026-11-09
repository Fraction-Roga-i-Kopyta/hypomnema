package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/pathutil"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

// runPin: memoryctl pin|unpin <slug> [--project TAG]. status is the target
// frontmatter status ("pinned" for pin, "active" for unpin).
func runPin(args []string, status string) {
	verb := "pin"
	if status != "pinned" {
		verb = "unpin"
	}
	var slug, project string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-h" || args[i] == "--help":
			fmt.Print(usage)
			return
		case args[i] == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintf(os.Stderr, "memoryctl %s: --project needs a value\n", verb)
				os.Exit(2)
			}
			project = args[i]
		case strings.HasPrefix(args[i], "-"), slug != "":
			fmt.Fprintf(os.Stderr, "Usage: memoryctl %s <slug> [--project TAG]\n", verb)
			os.Exit(2)
		default:
			slug = args[i]
		}
	}
	if slug == "" {
		fmt.Fprintf(os.Stderr, "Usage: memoryctl %s <slug> [--project TAG]\n", verb)
		os.Exit(2)
	}
	doPin(verb, slug, status, project)
	fmt.Printf("%sned %s\n", verb, slug)
}

func doPin(verb, slug, status, project string) {
	f, st := resolveFactIn(slug, project)
	raw, err := os.ReadFile(f.Path)
	if err != nil {
		fatalf("%s: read %s: %v", verb, f.Path, err)
	}
	updated, err := native.SetStatus(string(raw), status)
	if err != nil {
		fatalf("%s: %s: %v", verb, f.Path, err)
	}
	if err := pathutil.WriteFileAtomic(f.Path, []byte(updated), 0o644); err != nil {
		fatalf("%s: write %s: %v", verb, f.Path, err)
	}
	target := native.QKey(f.Project, strings.TrimSuffix(f.Slug, ".md"))
	if err := emitLifecycleEvent(verb, target); err != nil {
		// All-or-nothing, same rule as doRetire.
		_ = pathutil.WriteFileAtomic(f.Path, raw, 0o644)
		fatalf("%s: wal append: %v (fact left untouched)", verb, err)
	}
	refreshStoreIndex(st)
	refreshSidecar(st)
}

// refreshSidecar re-projects the sidecar after a successful write so cards and
// lists show the new state at once, the mark table included (ReprojectAll
// over every store, the session's own among them). Best effort: the WAL is
// the source of truth, so failures only warn. MarkStale follows the
// projection exactly as closer.Close does, because Reproject alone resets
// 'stale' to the frontmatter status.
func refreshSidecar(st native.Store) {
	warn := func(err error) {
		fmt.Fprintf(os.Stderr, "memoryctl: warning: sidecar not refreshed: %v\n", err)
	}
	s, err := sidecar.Open(filepath.Join(memoryDir(), ".sidecar.db"))
	if err != nil {
		warn(err)
		return
	}
	defer s.Close()
	files := native.Collect(claudeDir(), st)
	dirs := storeDirs(resolveStore("").Store)
	if err := sidecar.ReprojectAll(s, files, filepath.Join(memoryDir(), ".wal"), st.Scope(), dirs); err != nil {
		warn(err)
		return
	}
	if _, err := s.MarkStale(today()); err != nil {
		warn(err)
	}
}
