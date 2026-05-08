package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/invariants"
)

// runAudit dispatches `memoryctl audit <subcommand>`. Currently only
// `invariants` is implemented; future subcommands could include
// `corpus` (memory file health), `wal` (alias for `wal validate`),
// or `parity` (run scripts/parity-check.sh from Go).
func runAudit(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "memoryctl audit: missing subcommand (try `invariants`)")
		os.Exit(2)
	}
	switch args[0] {
	case "invariants":
		runAuditInvariants(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "memoryctl: unknown audit subcommand %q\n", args[0])
		os.Exit(2)
	}
}

// runAuditInvariants runs every checker in invariants.All() against
// a repo root (default: current working directory; override with
// `--repo PATH`). Reports one line per violation grouped by ID. Exit
// codes: 0 clean, 1 violations present, 2 command misuse.
//
// The repo-root convention (not memoryDir) reflects the function's
// scope: this scans source code, not user memory data. Most users
// will run it from the project checkout; CI will run it from the
// repo workspace.
func runAuditInvariants(args []string) {
	repoRoot := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			fmt.Print(`Usage: memoryctl audit invariants [--repo PATH]

Runs every automated invariant check from internal/invariants
against the repo at PATH (default: current working directory).
See docs/INVARIANTS.md for the catalog of rules; the automated
checks cover I3 (atomic writes) and I5 (hook fail-safe) so far.

Exit codes:
  0 — clean
  1 — at least one violation
  2 — command misuse (bad flag, repo missing required dirs)
`)
			return
		case "--repo":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "memoryctl audit invariants: --repo requires a path")
				os.Exit(2)
			}
			repoRoot = args[i+1]
			i++
		default:
			fmt.Fprintf(os.Stderr, "memoryctl audit invariants: unknown flag %q\n", args[i])
			os.Exit(2)
		}
	}
	if repoRoot == "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "memoryctl audit invariants: %v\n", err)
			os.Exit(2)
		}
		repoRoot = wd
	}

	// Sanity check: the rooty directories must exist or we'll silently
	// scan nothing and report "0 violations" misleadingly.
	for _, sub := range []string{"internal", "hooks"} {
		if _, err := os.Stat(filepath.Join(repoRoot, sub)); err != nil {
			fmt.Fprintf(os.Stderr, "memoryctl audit invariants: %s/%s missing — is %s a hypomnema repo root?\n",
				repoRoot, sub, repoRoot)
			os.Exit(2)
		}
	}

	var allViolations []invariants.Violation
	for _, c := range invariants.All() {
		v, err := c.Check(repoRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "memoryctl audit invariants: %s checker failed: %v\n", c.ID(), err)
			os.Exit(2)
		}
		allViolations = append(allViolations, v...)
	}

	fmt.Printf("Invariants audit: %s\n", repoRoot)
	fmt.Printf("  checkers: %d\n", len(invariants.All()))
	fmt.Printf("  violations: %d\n", len(allViolations))

	if len(allViolations) == 0 {
		return
	}

	// Group by ID for readability.
	sort.SliceStable(allViolations, func(i, j int) bool {
		if allViolations[i].ID != allViolations[j].ID {
			return allViolations[i].ID < allViolations[j].ID
		}
		return allViolations[i].Path < allViolations[j].Path
	})

	fmt.Println()
	fmt.Println("violations:")
	currentID := ""
	for _, v := range allViolations {
		if v.ID != currentID {
			fmt.Printf("  [%s] %s\n", v.ID, lookupCheckerName(v.ID))
			currentID = v.ID
		}
		fmt.Printf("    %s — %s\n", v.Path, v.Message)
	}

	hasError := false
	for _, v := range allViolations {
		if strings.EqualFold(v.Severity, "error") {
			hasError = true
			break
		}
	}
	if hasError {
		os.Exit(1)
	}
}

// lookupCheckerName resolves a checker ID back to a human-readable
// name without exporting Checker through the package boundary. Tiny
// helper to keep the output aligned with the catalog.
func lookupCheckerName(id string) string {
	for _, c := range invariants.All() {
		if c.ID() == id {
			return c.Name()
		}
	}
	return ""
}
