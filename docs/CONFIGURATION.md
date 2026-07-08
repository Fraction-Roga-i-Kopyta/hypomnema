# Configuration

Hypomnema v2 has **no configuration file**. There is no `.config.sh`, no
`projects.json` to edit, no per-type `decay_rate:` knob. Everything the
system reads at runtime is either an **environment variable** or a small
on-disk allow-list (`.secretsignore`). Injection itself is not tunable:
a single ranker produces one **top-8** result set capped at **2.5 KB per
body / 8 KB total**, and that budget is fixed in the binary.

This document lists every knob that actually exists and where it lives.

## 1. Environment variables

`memoryctl` and the v2 hook shims read configuration from the environment.
None of these are required for a normal install — the defaults are correct
out of the box. Set them only for non-standard installs, tests, or replay.

| Variable | Default | What it controls |
|---|---|---|
| `CLAUDE_MEMORY_DIR` | `~/.claude/memory` | Metadata root — holds `.wal`, `.sidecar.db`, `self-profile.md`. Not the content store. |
| `CLAUDE_HOME` | `~/.claude` | Claude Code state root. Used by `memoryctl doctor` and native-store resolution; lets parallel installs / fixtures coexist. |
| `CLAUDE_PROJECT_CWD` | current working dir | Working directory used to resolve the per-project native store (`<home>/.claude/projects/<slug>/memory`). |
| `HYPOMNEMA_GLOBAL_DIR` | `~/.claude/memory-global` | Location of the global native store (facts that apply across every project). |
| `HYPOMNEMA_MEMORYCTL` | `~/.claude/bin/memoryctl` | Path to the `memoryctl` binary the shims invoke. |
| `HYPOMNEMA_SESSION_ID` | (from hook envelope) | Session id stamped into WAL entries. |
| `CLAUDE_CODE_SESSION_ID` | (exported by Claude Code) | Session id fallback — `memoryctl recall` uses it when `HYPOMNEMA_SESSION_ID` is unset. |
| `HYPOMNEMA_TODAY` | wall clock | Freeze "today" as `YYYY-MM-DD` (decay windows, WAL cutoffs) for tests / replay. |
| `HYPOMNEMA_NOW` | wall clock | Freeze the self-profile `generated:` stamp as `YYYY-MM-DD HH:MM`. |
| `HYPOMNEMA_ALLOW_SECRETS` | unset | Set to `1` to bypass the secrets gate for a single invocation (see §3). |

### Self-profile analytics knobs

These three affect only `memoryctl self-profile` (the WAL analytics pass).
They exist mostly for fixture tests; the defaults come from the ADRs and
are what production runs on.

| Variable | Default | What it controls |
|---|---|---|
| `HYPOMNEMA_OUTCOME_WINDOW_DAYS` | 14 | Rolling window (days) for the per-slug Bayesian outcome sampling. |
| `HYPOMNEMA_BAYESIAN_MIN_SAMPLES` | 5 | Minimum samples before a slug is counted in the corpus Bayesian fraction. |
| `HYPOMNEMA_INTUITION_WINDOW_DAYS` | 30 | Intuition-milestone window; `0` disables that filter entirely. |

## 2. Decay thresholds

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

## 3. Secrets gate

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
  compiled table in §2.
- **ADR review-triggers / `memoryctl decisions review`** — no such verb.
  Revisit-conditions live only as prose in decision files.
- **`memoryctl evidence learn`** — no such verb. Author `evidence:` phrases
  by hand (see the repo `CLAUDE.md`).

Run `memoryctl --help` for the authoritative verb list.
