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
	"strconv"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/dedup"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/doctor"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/fts"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/profile"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/tfidf"
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
  memoryctl dedup check <file-path>
      Fuzzy dedup for mistake files. Pretool (file absent) reads content
      from stdin; posttool (file present) reads it from disk. Exits 2 on
      ≥80% similarity in pretool mode, 1 on merge in posttool mode, 0
      otherwise. Drop-in replacement for bin/memory-dedup.py.
  memoryctl self-profile
      Regenerate ~/.claude/memory/self-profile.md from WAL + mistakes +
      strategies. Single-pass WAL aggregation; drop-in replacement for
      bin/memory-self-profile.sh.
  memoryctl doctor [--json]
      On-demand health check of the install: claude_dir, hook registrations
      in settings.json, broken symlinks in hooks/ and bin/, memoryctl
      availability, corpus counts, WAL-error events in the last 7 days,
      derived-index freshness. Exit 0 on OK/WARN only, 1 on any FAIL.
  memoryctl tfidf rebuild
      Unicode-aware rebuild of ~/.claude/memory/.tfidf-index. Replaces
      hooks/memory-index.sh's Latin-only awk tokenizer with
      unicode.IsLetter, so Cyrillic / CJK / Greek / Arabic bodies
      contribute real tokens. Writes atomically via tmp+rename.

Environment:
  CLAUDE_MEMORY_DIR       Memory root (default: ~/.claude/memory).
  HYPOMNEMA_TODAY         Freeze "today" in YYYY-MM-DD (for tests/replay).
  HYPOMNEMA_NOW           Freeze self-profile "generated:" stamp (YYYY-MM-DD HH:MM).
  HYPOMNEMA_SESSION_ID    Session id stamped into WAL entries.
  MEMORY_FTS_SHADOW_MAX   Cap shadow-miss events per pass (default 4).
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
	case "dedup":
		if len(os.Args) < 3 {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		switch os.Args[2] {
		case "check":
			runDedupCheck(os.Args[3:])
		default:
			fmt.Fprintf(os.Stderr, "memoryctl: unknown dedup subcommand %q\n", os.Args[2])
			os.Exit(2)
		}
	case "self-profile":
		runSelfProfile(os.Args[2:])
	case "doctor":
		runDoctor(os.Args[2:])
	case "tfidf":
		if len(os.Args) < 3 {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		switch os.Args[2] {
		case "rebuild":
			runTfidfRebuild(os.Args[3:])
		default:
			fmt.Fprintf(os.Stderr, "memoryctl: unknown tfidf subcommand %q\n", os.Args[2])
			os.Exit(2)
		}
	default:
		fmt.Fprintf(os.Stderr, "memoryctl: unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}

// runSelfProfile regenerates memoryDir/self-profile.md. Fail-safe: any
// error is silent so session-stop's backgrounded invocation can't leak
// noise into the user's terminal — matches the bash script's `|| true`
// posture.
func runSelfProfile(_ []string) {
	if err := profile.Generate(memoryDir()); err != nil {
		// Non-zero exit would get swallowed by session-stop's `2>/dev/null &`
		// anyway; still, propagate for tests that call the binary directly.
		fmt.Fprintf(os.Stderr, "memoryctl self-profile: %v\n", err)
		os.Exit(1)
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

// claudeDir resolves the Claude Code state root. Defaults to ~/.claude
// but respects $CLAUDE_HOME for parallel installs and test fixtures. Used
// only by doctor — the hook scripts don't rely on this variable today.
func claudeDir() string {
	if d := os.Getenv("CLAUDE_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// runTfidfRebuild regenerates the TF-IDF index. Fail-safe: printed to
// stderr and non-zero exit on error, but the calling bash hook treats
// any non-zero as "fall back to the awk path" rather than aborting.
func runTfidfRebuild(_ []string) {
	if err := tfidf.Rebuild(memoryDir()); err != nil {
		fmt.Fprintf(os.Stderr, "memoryctl tfidf rebuild: %v\n", err)
		os.Exit(1)
	}
}

// runDoctor runs the read-only health check and prints a summary. Exits 1
// if any check is FAIL so CI / shell scripts can gate on `memoryctl doctor`.
func runDoctor(args []string) {
	jsonOut := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Print(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "memoryctl doctor: unknown flag %q\n", a)
			os.Exit(2)
		}
	}
	report := doctor.Run(claudeDir(), memoryDir())
	if jsonOut {
		report.PrintJSON(os.Stdout)
	} else {
		report.Print(os.Stdout)
	}
	os.Exit(report.ExitCode())
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
		parsed, err := strconv.Atoi(args[1])
		if err != nil || parsed < 1 {
			fmt.Fprintf(os.Stderr, "memoryctl fts query: invalid limit %q (want positive integer)\n", args[1])
			os.Exit(2)
		}
		limit = parsed
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

// runDedupCheck implements `memoryctl dedup check <file-path>`. The exit
// code protocol matches bin/memory-dedup.py and the contract hooks/
// memory-dedup.sh expects:
//   0 — allow (default), also used for medium-similarity warning
//   1 — posttool merge happened (legacy Python behaviour; shell wrapper
//       treats non-zero non-2 exits as "not blocking")
//   2 — pretool block; wrapper propagates stderr message to Claude
func runDedupCheck(args []string) {
	if len(args) < 1 {
		os.Exit(0) // graceful no-op; matches Python policy
	}

	// All human-readable messages go to stderr: block messages need to
	// surface through the shell wrapper (which captures 2>&1 and re-emits
	// on stderr for exit 2), and candidate warnings are informational —
	// stderr keeps stdout clean for any future scripted consumer of this
	// subcommand.
	opts := dedup.Options{
		MemoryDir: memoryDir(),
		SessionID: os.Getenv("HYPOMNEMA_SESSION_ID"),
		Today:     os.Getenv("HYPOMNEMA_TODAY"),
		Stdin:     os.Stdin,
		Out:       os.Stderr,
	}

	decision, err := dedup.Run(args[0], opts)
	if err != nil {
		// Any internal error is a no-op per hook fail-safe contract.
		os.Exit(0)
	}

	switch decision {
	case dedup.Blocked:
		os.Exit(2)
	case dedup.Merged:
		os.Exit(1)
	default:
		os.Exit(0)
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
