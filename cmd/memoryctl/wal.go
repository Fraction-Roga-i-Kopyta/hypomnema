package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/wal"
)

// runWal dispatches `memoryctl wal <subcommand>` to the right handler.
// Currently only `validate` is implemented; `migrate` lands in a
// follow-up PR with the v1→v2 session-metrics rewrite logic.
func runWal(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "memoryctl wal: missing subcommand (try `validate`)")
		os.Exit(2)
	}
	switch args[0] {
	case "validate":
		runWalValidate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "memoryctl: unknown wal subcommand %q\n", args[0])
		os.Exit(2)
	}
}

// runWalValidate scans the .wal in MemoryDir for grammar violations
// and reports a summary. Exit code 0 = clean; exit 1 = at least one
// failure surfaced (suitable for CI gating). Exit 2 = command misuse
// or filesystem error other than missing file.
//
// Output is plaintext designed for terminal reading, not machine
// parsing. A `--json` flavour can be added in a follow-up if a CI
// integration needs structured output.
func runWalValidate(args []string) {
	for _, a := range args {
		switch a {
		case "-h", "--help":
			fmt.Print(`Usage: memoryctl wal validate

Reads $CLAUDE_MEMORY_DIR/.wal (default ~/.claude/memory/.wal) and
checks every row against the four-column WAL grammar invariant:

  - exactly four pipe-delimited columns
  - column 1 matches YYYY-MM-DD
  - column 2 matches [a-z][a-z0-9-]*
  - column 3 contains no carriage return (writers sanitise via pathutil.SlugFromPath)
  - column 4 is non-empty

Optional v2 header on row 1 (` + "`# hypomnema-wal v2`" + `) is tolerated.

Exit codes:
  0 — clean
  1 — at least one row failed validation
  2 — command misuse or filesystem error other than missing file
`)
			return
		default:
			fmt.Fprintf(os.Stderr, "memoryctl wal validate: unknown flag %q\n", a)
			os.Exit(2)
		}
	}

	walPath := filepath.Join(memoryDir(), ".wal")
	report, err := wal.Validate(walPath)
	if err != nil {
		if errors.Is(err, wal.ErrWALMissing) {
			fmt.Fprintf(os.Stderr, "memoryctl wal validate: %s does not exist (no events recorded yet)\n", walPath)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "memoryctl wal validate: %v\n", err)
		os.Exit(2)
	}

	fmt.Printf("WAL validation report: %s\n", walPath)
	fmt.Printf("  total:    %d\n", report.Total)
	fmt.Printf("  passing:  %d\n", report.Passing)
	fmt.Printf("  failures: %d\n", len(report.Failures))

	if !report.Clean() {
		fmt.Println()
		fmt.Println("failures:")
		for _, f := range report.Failures {
			fmt.Printf("  line %d: %s\n", f.LineNum, f.Reason)
			fmt.Printf("    %s\n", f.Line)
		}
		os.Exit(1)
	}
}
