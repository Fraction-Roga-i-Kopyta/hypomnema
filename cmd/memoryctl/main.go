// memoryctl is the Go CLI for the hypomnema memory system.
//
// The contract this binary obeys is docs/FORMAT.md + docs/hooks-contract.md.
// Anything here is implementation detail and may change between releases, but
// the on-disk layout (frontmatter, WAL) and the WAL events this binary emits
// must match the reference bash implementation exactly.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/dedup"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/doctor"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/profile"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/wal"
)

const usage = `memoryctl — hypomnema memory CLI

Usage:
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
      availability, native corpus counts, WAL-error events in the last 7
      days, sidecar freshness. Exit 0 on OK/WARN only, 1 on any FAIL.
  memoryctl wal validate
      Scan $CLAUDE_MEMORY_DIR/.wal for grammar violations against the
      four-column invariant from FORMAT.md § 5: column count, date
      format, event name, slug sanitisation, non-empty session.
      Exit 0 = clean, 1 = at least one row failed, 2 = WAL missing or
      command misuse.
  memoryctl audit invariants [--repo PATH]
      Run every automated invariant check from internal/invariants
      against the repo at PATH (default: current working directory).
      Catalog in docs/INVARIANTS.md; automated coverage at v1.x is
      I3 (atomic writes) and I5 (hook fail-safe). Exit 0 = clean,
      1 = violations, 2 = command misuse or repo not detected.
  memoryctl sidecar rebuild
      Rebuild the SQLite sidecar projection from native memory + WAL
      ($CLAUDE_HOME/projects/<slug>/memory + ~/.claude/memory/.wal). Writes
      ~/.claude/memory/.sidecar.db. Run after bulk imports or when the
      projection is suspected stale.
  memoryctl sidecar show
      Print sidecar memory records (slug, type, ref_count, effectiveness).
      Reads the existing .sidecar.db; run "sidecar rebuild" first if the
      file is absent or stale.
  memoryctl reindex
      Regenerate <project>/memory/MEMORY.md from native files with sibling
      links, bounded under the harness size limit. v2 owns this index; the
      Stop hook regenerates it each session, this is the on-demand path.
      Resolves the project from CLAUDE_PROJECT_CWD, else the working dir.
  memoryctl recall <query words...> [--k N]
      Pull-side retrieval: rank current project + global memory against an
      ad-hoc query; print the best fact's body (2.5KB cap) plus an index of
      runner-ups with file paths (default 6 results total). Writes a
      "recall" WAL event for the delivered fact and unions it into the
      session's injected list. Includes stale facts (marked [stale]) —
      recalling one revives it.
  memoryctl skill-inject
      PostToolUse hook verb. Reads a hook envelope from stdin, extracts
      tool_input.skill, retrieves skill-learning facts bound to that skill,
      and prints a hookSpecificOutput JSON envelope with additionalContext.
      Writes a recall WAL event per delivered learning. Fail-safe: exits 0
      silently on bad input, missing skill, or no learnings.
  memoryctl skill-active
      PreToolUse hook verb. Reads a hook envelope from stdin, extracts
      tool_input.skill and session_id, and writes an active-skill marker
      file to memoryDir/.runtime/active-skill-<sid>. Lets the capture path
      tag skill-learnings with the correct skill after context compaction.
      Fail-safe: exits 0 on bad input or missing skill/session.

Environment:
  CLAUDE_MEMORY_DIR       Memory root (default: ~/.claude/memory).
  CLAUDE_PROJECT_CWD      Working dir for per-project native memory; defaults to cwd.
  HYPOMNEMA_TODAY         Freeze "today" in YYYY-MM-DD (for tests/replay).
  HYPOMNEMA_NOW           Freeze self-profile "generated:" stamp (YYYY-MM-DD HH:MM).
  HYPOMNEMA_SESSION_ID    Session id stamped into WAL entries.
  CLAUDE_CODE_SESSION_ID  Session id exported by Claude Code into Bash; recall
                          falls back to it when HYPOMNEMA_SESSION_ID is unset.
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
	case "wal":
		runWal(os.Args[2:])
	case "audit":
		runAudit(os.Args[2:])
	case "sidecar":
		if len(os.Args) < 3 {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		switch os.Args[2] {
		case "rebuild":
			runSidecarRebuild(os.Args[3:])
		case "show":
			runSidecarShow(os.Args[3:])
		default:
			fmt.Fprintf(os.Stderr, "memoryctl: unknown sidecar subcommand %q\n", os.Args[2])
			os.Exit(2)
		}
	case "migrate":
		runMigrate(os.Args[2:])
	case "close":
		runClose(os.Args[2:])
	case "inject":
		runInject(os.Args[2:])
	case "rank":
		runRank(os.Args[2:])
	case "recall":
		runRecall(os.Args[2:])
	case "skill-inject":
		runSkillInject(os.Args[2:])
	case "skill-active":
		runSkillActive(os.Args[2:])
	case "ab":
		runAB(os.Args[2:])
	case "guard":
		runGuard(os.Args[2:])
	case "reindex":
		runReindex(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "memoryctl: unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}

// runSelfProfile regenerates memoryDir/self-profile.md. Fail-safe: any
// error is silent so session-stop's backgrounded invocation can't leak
// noise into the user's terminal — matches the bash script's `|| true`
// posture.
//
// Generate is serialised under a dedicated lock (P2.12) so concurrent
// session-stops cannot clobber each other's writes. The lock file is
// separate from the WAL lock so profile regen cannot block WAL appends.
func runSelfProfile(_ []string) {
	lockPath := filepath.Join(memoryDir(), ".self-profile.lockd")
	lock, _ := wal.AcquirePath(lockPath, wal.DefaultLockConfig)
	// Follow the bash script's policy: if the lock cannot be acquired,
	// run anyway — losing a race is strictly better than skipping the
	// regen entirely. Release is a no-op on a nil handle.
	defer lock.Release()

	// v2: content (mistakes/strategies/ambient) comes from native memory, not
	// memoryDir subdirs. Resolve the merged per-project + global corpus and
	// pass it to Generate; the WAL + output file still live in memoryDir.
	files, err := collectNative()
	if err != nil {
		fmt.Fprintf(os.Stderr, "memoryctl self-profile: %v\n", err)
		os.Exit(1)
	}
	if err := profile.Generate(memoryDir(), files); err != nil {
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
	report := doctor.Run(claudeDir(), memoryDir(), projectCWD())
	if jsonOut {
		report.PrintJSON(os.Stdout)
	} else {
		report.Print(os.Stdout)
	}
	os.Exit(report.ExitCode())
}

// runDedupCheck implements `memoryctl dedup check <file-path>`. The exit
// code protocol matches bin/memory-dedup.py and the contract hooks/
// memory-dedup.sh expects:
//
//	0 — allow (default), also used for medium-similarity warning
//	1 — posttool merge happened (legacy Python behaviour; shell wrapper
//	    treats non-zero non-2 exits as "not blocking")
//	2 — pretool block; wrapper propagates stderr message to Claude
func runDedupCheck(args []string) {
	if len(args) < 1 {
		os.Exit(0) // graceful no-op; matches Python policy
	}

	// All human-readable messages go to stderr: block messages need to
	// surface through the shell wrapper (which captures 2>&1 and re-emits
	// on stderr for exit 2), and candidate warnings are informational —
	// stderr keeps stdout clean for any future scripted consumer of this
	// subcommand.
	// v2: the dedup comparison corpus is the native mistake store, not a
	// MemoryDir/mistakes/ subdir. Resolve the merged per-project + global
	// native corpus; on failure pass nil (dedup degrades to Allow).
	files, _ := collectNative()
	opts := dedup.Options{
		MemoryDir: memoryDir(),
		Files:     files,
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
