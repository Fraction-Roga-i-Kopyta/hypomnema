package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/memindex"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// runReindex regenerates <project>/memory/MEMORY.md from the native files for
// the current project. v2 owns this index; v1 left orphans with ../../../
// links over the harness's size limit. The Stop hook also regenerates it every
// session; this verb is the on-demand path (e.g. after a bulk import or to
// replace a stale v1 orphan immediately). Resolves the project from
// CLAUDE_PROJECT_CWD, else the process working directory. Best-effort: a
// missing project memory dir is a no-op, exit 0.
func runReindex(args []string) {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Print(usage)
			return
		}
		fmt.Fprintf(os.Stderr, "memoryctl reindex: unknown flag %q\n", a)
		os.Exit(2)
	}
	cwd := os.Getenv("CLAUDE_PROJECT_CWD")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	osHome := filepath.Dir(claudeDir())
	projDir := native.ProjectMemoryDir(osHome, cwd)
	files, err := native.List(projDir)
	if err != nil || len(files) == 0 {
		fmt.Fprintf(os.Stderr, "memoryctl reindex: no project memory at %s\n", projDir)
		os.Exit(0)
	}
	content := memindex.Render(files, memindex.DefaultMaxBytes)
	if err := memindex.Write(projDir, content); err != nil {
		fmt.Fprintf(os.Stderr, "memoryctl reindex: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "memoryctl reindex: wrote %s (%d files, %d bytes)\n",
		filepath.Join(projDir, "MEMORY.md"), len(files), len(content))
}
