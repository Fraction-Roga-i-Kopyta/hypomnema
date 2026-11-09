package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/wal"
)

// runWal dispatches `memoryctl wal <subcommand>` to the right handler.
// Subcommands: `validate`, `compact`.
func runWal(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "memoryctl wal: missing subcommand (try `validate` or `compact`)")
		os.Exit(2)
	}
	switch args[0] {
	case "validate":
		runWalValidate(args[1:])
	case "compact":
		runWalCompact(args[1:])
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

// runWalCompact drops the WAL rows no reader consumes any more
// (wal.LegacyEvents), keeps the original as .wal.pre-compact-<today>, and
// re-projects the sidecar so its recorded WAL offset matches the new file.
// --dry-run reports the same numbers and writes nothing.
func runWalCompact(args []string) {
	dryRun := false
	for _, a := range args {
		switch a {
		case "--dry-run":
			dryRun = true
		case "-h", "--help":
			fmt.Print(`Usage: memoryctl wal compact [--dry-run]

Rewrites $CLAUDE_MEMORY_DIR/.wal without the event types no reader uses
any more (trigger-useful, trigger-silent, trigger-silent-retro,
session-metrics). Every other row is kept byte-for-byte and in order; the
original stays as .wal.pre-compact-<date> (an existing backup is never
replaced; a second one that day gets a -2 suffix). The sidecar is then
re-projected so its WAL offset matches the new file. Run it when no session is mid-turn.

Exit codes: 0 — done (or nothing to drop); 1 — failed (if only the sidecar
re-projection failed, the WAL is already compacted — run memoryctl sidecar
rebuild); 2 — command misuse or WAL missing.
`)
			return
		default:
			fmt.Fprintf(os.Stderr, "memoryctl wal compact: unknown flag %q\n", a)
			os.Exit(2)
		}
	}
	rep, err := wal.Compact(memoryDir(), today(), dryRun)
	if errors.Is(err, wal.ErrWALMissing) {
		fmt.Fprintf(os.Stderr, "memoryctl wal compact: no WAL at %s\n", filepath.Join(memoryDir(), ".wal"))
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "memoryctl wal compact: %v\n", err)
		os.Exit(1)
	}
	dropped := 0
	events := make([]string, 0, len(rep.Dropped))
	for ev, n := range rep.Dropped {
		dropped += n
		events = append(events, ev)
	}
	sort.Strings(events)
	verb := "compacted"
	if dryRun {
		verb = "dry run"
	}
	fmt.Printf("%s: kept %d, dropped %d rows; %d → %d bytes\n", verb, rep.Kept, dropped, rep.BytesBefore, rep.BytesAfter)
	for _, ev := range events {
		fmt.Printf("  %-22s %d\n", ev, rep.Dropped[ev])
	}
	if dryRun {
		return
	}
	if rep.Backup == "" {
		return // nothing dropped: the WAL and the sidecar are untouched
	}
	fmt.Printf("backup: %s\n", rep.Backup)
	r := resolveStore("")
	s, err := sidecar.Open(sidecarPath())
	if err == nil {
		err = sidecar.ReprojectAll(s, collectNative(r.Store), filepath.Join(memoryDir(), ".wal"), r.Scope(), storeDirs(r.Store))
		s.Close()
	}
	if err != nil {
		// The WAL is already compacted and valid; only the projection lags.
		fmt.Fprintf(os.Stderr, "memoryctl wal compact: sidecar re-projection failed (%v) — run `memoryctl sidecar rebuild`\n", err)
		os.Exit(1)
	}
}
