package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/promote"
)

// runPromote: memoryctl promote — report-only promotion-ladder scan. Prints
// facts that have outgrown prose memory with the suggested durable owner and
// the closing command. Exit 0 always (informational, cron-safe).
func runPromote(args []string) {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Print(usage)
			return
		}
		fmt.Fprintf(os.Stderr, "memoryctl promote: unknown flag %q\n", a)
		os.Exit(2)
	}
	files, err := collectNative()
	if err != nil {
		fmt.Fprintf(os.Stderr, "memoryctl promote: %v\n", err)
		os.Exit(1)
	}
	sugg := promote.Analyze(files, filepath.Join(memoryDir(), ".wal"), promote.Defaults())
	if len(sugg) == 0 {
		fmt.Println("no promotion candidates")
		return
	}
	fmt.Printf("%d promotion candidate(s):\n\n", len(sugg))
	for _, s := range sugg {
		fmt.Printf("- %s [%s] (%s)\n    evidence: %s\n    next: %s\n",
			s.Slug, s.Kind, s.Project, s.Evidence, s.Action)
	}
}
