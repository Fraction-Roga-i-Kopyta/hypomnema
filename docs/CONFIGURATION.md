# Configuration

Hypomnema v2 has **no configuration file**. There is no `.config.sh`, no
`projects.json` to edit, no per-type `decay_rate:` knob. Everything the
system reads at runtime is either an **environment variable** or a small
on-disk allow-list (`.secretsignore`). Injection itself is not tunable:
a single ranker produces one **top-8** result set capped at **2.5 KB per
body / 8 KB total**, and that budget is fixed in the binary.

This document lists every knob that actually exists and where it lives.

## 1. Store resolution

hypomnema resolves the same memory dir Claude Code itself would, in this
order:

`CLAUDE_COWORK_MEMORY_PATH_OVERRIDE` → `autoMemoryDirectory` (first of
managed policy → `<project>/.claude/settings.local.json` → `<config
dir>/settings.json` that sets the key; a checked-in `.claude/settings.json`
is never consulted) → `<config dir>/projects/<sanitized canonical
root>/memory`, where `<config dir>` is `CLAUDE_HOME` (or `CLAUDE_CONFIG_DIR`,
see §2) or `~/.claude`.

- **Canonical root** is the nearest git root, walking up from the project
  anchor; a linked worktree resolves to its main checkout, not to its own
  `.git` pointer file, so a session opened inside a worktree reads and writes
  the same store as the main checkout. See `docs/ARCHITECTURE.md` § Stores
  for the exact structural checks this relies on (they exist so a planted
  pointer file — e.g. from an extracted archive — cannot redirect a session
  onto another project's memory).
- **Sanitizer** turns every character outside `[a-zA-Z0-9]` into `-`; a
  result longer than 200 characters is cut to 200 and suffixed with a hash,
  mirroring Claude Code's own slugging exactly.
- **Project anchor** — what the canonical root is computed from — follows:
  `CLAUDE_PROJECT_DIR` (set by Claude Code on every hook invocation) →
  `CLAUDE_PROJECT_CWD` (hypomnema-only override for CLI use) → the session
  pin (`~/.claude/memory/.runtime/project-<session id>.json`, written at
  `SessionStart` and read by CLI verbs run later in the same session through
  the Bash tool via `CLAUDE_CODE_SESSION_ID`) → the process's working
  directory. The pin file records **only the anchor path** (`{"anchor":
  "<abs project dir>"}`) — the store, project tag, and canonical root are
  re-derived from it on every read, the same as if that anchor had been
  passed as `CLAUDE_PROJECT_CWD`. This caps a pin's power at
  `CLAUDE_PROJECT_CWD`-level trust: since the pin is a file any process
  could in principle write, a forged one can only redirect resolution to a
  directory the forger already controls, not inject a false project tag or
  store path directly.

`autoMemoryDirectory` in a project's `.claude/settings.local.json` is honoured
exactly the way Claude Code honours it: Claude Code's workspace-trust dialog
shows that setting before a session — and its hooks — is allowed to run at
all. A manual `memoryctl` run inside an untrusted checkout still reads that
checkout's `settings.local.json`, so a one-off CLI invocation can resolve a
different store than a trusted session in the same directory would.

## 2. Environment variables

`memoryctl` and the v2 hook shims read configuration from the environment.
None of these are required for a normal install — the defaults are correct
out of the box. Set them only for non-standard installs, tests, or replay.

| Variable | Default | What it controls |
|---|---|---|
| `CLAUDE_MEMORY_DIR` | `~/.claude/memory` | Metadata root — holds `.wal`, `.sidecar.db`, `self-profile.md`. Not the content store. |
| `CLAUDE_HOME` | `~/.claude` | Test/parallel-install override for hypomnema's own state root (hooks, bin) **and**, because it is checked first, for native store resolution too. |
| `CLAUDE_CONFIG_DIR` | `~/.claude` | Claude Code's own config-dir override, checked only when `CLAUDE_HOME` is unset. Moves **only** `projects/` and `settings.json` resolution (§1) — the global store stays at `~/.claude/memory-global` (`HYPOMNEMA_GLOBAL_DIR` still overrides that independently). |
| `CLAUDE_PROJECT_DIR` | (unset) | Project anchor Claude Code sets for every hook invocation; outranks `CLAUDE_PROJECT_CWD` (§1). |
| `CLAUDE_PROJECT_CWD` | current working dir | hypomnema-only explicit override for CLI use; ignored inside hooks — `CLAUDE_PROJECT_DIR` always wins there. |
| `CLAUDE_COWORK_MEMORY_PATH_OVERRIDE` | unset | Highest-precedence store override: an absolute (or `~/`-prefixed) path used verbatim as the resolved memory dir. |
| `HYPOMNEMA_GLOBAL_DIR` | `~/.claude/memory-global` | Location of the global native store (facts that apply across every project). |
| `HYPOMNEMA_MEMORYCTL` | `~/.claude/bin/memoryctl` | Path to the `memoryctl` binary the shims invoke. |
| `HYPOMNEMA_SESSION_ID` | (from hook envelope) | Session id stamped into WAL entries. |
| `CLAUDE_CODE_SESSION_ID` | (exported by Claude Code) | Session id fallback — `memoryctl recall` uses it, and the store-resolution session pin is looked up by it, when `HYPOMNEMA_SESSION_ID` is unset. |
| `HYPOMNEMA_TODAY` | wall clock | Freeze "today" as `YYYY-MM-DD` (decay windows, WAL cutoffs) for tests / replay. |
| `HYPOMNEMA_NOW` | wall clock | Freeze the self-profile `generated:` stamp as `YYYY-MM-DD HH:MM`. |
| `HYPOMNEMA_ALLOW_SECRETS` | unset | Set to `1` to bypass the secrets gate for a single invocation (see §4). |

### Self-profile analytics knobs

These three affect only `memoryctl self-profile` (the WAL analytics pass).
They exist mostly for fixture tests; the defaults come from the ADRs and
are what production runs on.

| Variable | Default | What it controls |
|---|---|---|
| `HYPOMNEMA_OUTCOME_WINDOW_DAYS` | 14 | Rolling window (days) for the per-slug Bayesian outcome sampling. |
| `HYPOMNEMA_BAYESIAN_MIN_SAMPLES` | 5 | Minimum samples before a slug is counted in the corpus Bayesian fraction. |
| `HYPOMNEMA_INTUITION_WINDOW_DAYS` | 30 | Intuition-milestone window; `0` disables that filter entirely. |

## 3. Decay thresholds

Decay is **hardcoded in `internal/sidecar/decay.go`** — it is not
operator-configurable and there is no frontmatter override. When a fact
goes unused past its per-type threshold the sidecar flips its status
`active → stale` (a down-rank, never a file move — v2 has **no `archive/`
directory** and no second archival stage). Native content is never mutated.

| Type | Stale after (days) |
|---|---|
| `note` | 30 |
| `feedback` | 45 |
| `mistake` | 60 |
| `strategy` | 90 |
| `knowledge` | 90 |
| `decision` | 90 |
| `skill-learning` | 120 |
| `user` | 180 |
| `reference` | 90 |
| any other type | 90 (default) |

Rules:

- **Age counts from the last injection**, falling back to `created`. A fact
  in active rotation never goes stale just because its file is old.
- `status: pinned` files never decay.
- `type: continuity` and `type: project` facts are exempt from rotation
  entirely — they are "where we left off" markers.
- A stale fact is simply excluded from push injection. It still surfaces
  through `memoryctl recall` (marked `[stale]`), and recalling it revives it.

If you genuinely need different thresholds, edit the `staleDays` map in
`internal/sidecar/decay.go` and rebuild (`make build`).

## 4. Secrets gate

`memoryctl guard` (the `PreToolUse` matcher `Write|Edit`) blocks writes to
memory files whose body contains a recognised credential pattern. It is not
configured — it is always on — but it has two escape hatches:

- **Whitelist a path permanently.** Add a glob to
  `~/.claude/memory-global/.secretsignore` (one pattern per line; last
  matching pattern wins, `!` negates). The lookup chain also includes
  `$CLAUDE_MEMORY_DIR/.secretsignore.default` and
  `$CLAUDE_MEMORY_DIR/.secretsignore`.
- **Override for one write.** Set `HYPOMNEMA_ALLOW_SECRETS=1` for a single
  invocation.

Do not route around the gate by stripping the value — either whitelist the
path or replace the text with an obvious placeholder.

## What is NOT configurable

These were tunable (or claimed to be) in v1 and are **gone** in v2:

- **Injection caps** (`MAX_FILES`, `CAP_*`, per-type quotas). The ranker
  emits a fixed top-8 / 8 KB budget; there is no `.config.sh`.
- **`decay_rate:` frontmatter** and archive thresholds — decay is the single
  compiled table in §3.
- **ADR review-triggers / `memoryctl decisions review`** — no such verb.
  Revisit-conditions live only as prose in decision files.
- **`memoryctl evidence learn`** — no such verb. Author `evidence:` phrases
  by hand (see the repo `CLAUDE.md`).

Run `memoryctl --help` for the authoritative verb list.
