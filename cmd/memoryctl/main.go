// memoryctl is the Go CLI for the hypomnema memory system. First subcommand
// shipped: `fts shadow`, a drop-in replacement for bin/memory-fts-shadow.sh.
//
// The contract this binary obeys is docs/FORMAT.md + docs/hooks-contract.md.
// Anything here is implementation detail and may change between releases, but
// the on-disk layout (frontmatter, WAL, index.db schema) and the WAL events
// this binary emits must match the reference bash implementation exactly.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/fts"
)

const usage = `memoryctl — hypomnema memory CLI

Usage:
  memoryctl fts shadow <prompt> <session-id> [injected-slugs]
      Run the FTS5 recall-shadow pass. Drop-in replacement for
      bin/memory-fts-shadow.sh. injected-slugs is a newline-separated string
      of slugs already injected by the primary pipeline this prompt
      (pass "" if nothing).
  memoryctl fts sync
      Bring ~/.claude/memory/index.db in sync with the markdown tree.
  memoryctl fts query <prompt> [limit]
      Free-form FTS5 search. TSV output: path<TAB>score, best-first.

Environment:
  CLAUDE_MEMORY_DIR  Memory root (default: ~/.claude/memory).
  HYPOMNEMA_TODAY    Freeze "today" in YYYY-MM-DD (for tests/replay).
  MEMORY_FTS_SHADOW_MAX  Cap shadow-miss events per pass (default 4).
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	// Top-level dispatch. `memoryctl --help` / `-h` print usage and exit 0
	// so it's safe to call in shell completion.
	switch os.Args[1] {
	case "-h", "--help":
		fmt.Print(usage)
		return
	case "fts":
		if len(os.Args) < 3 {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		switch os.Args[2] {
		case "shadow":
			runShadow(os.Args[3:])
		case "sync":
			runSync(os.Args[3:])
		case "query":
			runQuery(os.Args[3:])
		default:
			fmt.Fprintf(os.Stderr, "memoryctl: unknown fts subcommand %q\n", os.Args[2])
			os.Exit(2)
		}
	default:
		fmt.Fprintf(os.Stderr, "memoryctl: unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}

// memoryDir resolves the memory root from $CLAUDE_MEMORY_DIR, falling back to
// ~/.claude/memory. Mirrors bash behaviour.
func memoryDir() string {
	if d := os.Getenv("CLAUDE_MEMORY_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "memory")
}

// runShadow is the primary pilot subcommand — byte-for-byte equivalent output
// to bin/memory-fts-shadow.sh (up to non-determinism in FTS5 tie-breaking).
// Fail-safe: any error produces exit 0 with no stderr.
func runShadow(args []string) {
	if len(args) < 2 {
		os.Exit(0) // guard matches bash; missing args = no-op
	}
	prompt := args[0]
	sessionID := args[1]
	var injected []string
	if len(args) >= 3 && args[2] != "" {
		injected = strings.Split(args[2], "\n")
	}

	cfg := fts.ShadowConfig{
		MaxShadow: parseShadowMaxEnv(),
		Today:     os.Getenv("HYPOMNEMA_TODAY"),
	}
	// Shadow is observational — always exit 0 even if the call errored.
	_ = fts.Shadow(context.Background(), memoryDir(), prompt, sessionID, injected, cfg)
	os.Exit(0)
}

func runSync(args []string) {
	// flag set exists so future flags (timeout, etc.) plug in cleanly.
	fs := flag.NewFlagSet("fts sync", flag.ExitOnError)
	_ = fs.Parse(args)

	dbPath := filepath.Join(memoryDir(), "index.db")
	ix, err := fts.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memoryctl fts sync: %v\n", err)
		os.Exit(1)
	}
	defer ix.Close()
	if err := ix.Sync(context.Background(), memoryDir()); err != nil {
		fmt.Fprintf(os.Stderr, "memoryctl fts sync: %v\n", err)
		os.Exit(1)
	}
}

func runQuery(args []string) {
	if len(args) < 1 {
		os.Exit(2)
	}
	limit := 10
	if len(args) >= 2 {
		fmt.Sscanf(args[1], "%d", &limit)
	}

	dbPath := filepath.Join(memoryDir(), "index.db")
	ix, err := fts.Open(dbPath)
	if err != nil {
		os.Exit(0) // graceful — no results
	}
	defer ix.Close()
	_ = ix.Sync(context.Background(), memoryDir())

	hits, err := ix.Query(context.Background(), args[0], limit)
	if err != nil {
		os.Exit(0)
	}
	for _, h := range hits {
		fmt.Printf("%s\t%.3f\n", h.Path, h.Score)
	}
}

func parseShadowMaxEnv() int {
	v := os.Getenv("MEMORY_FTS_SHADOW_MAX")
	if v == "" {
		return 0 // let ShadowConfig default apply
	}
	var n int
	_, err := fmt.Sscanf(v, "%d", &n)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
