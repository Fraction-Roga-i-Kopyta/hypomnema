# Configuration

Hypomnema has three separate layers of tunable parameters. They live in
different places, are edited by different people, and exist for
different reasons. This document lists what each layer controls, where
it lives, and how to change it.

## 1. Runtime config — `~/.claude/memory/.config.sh`

Operator-facing. Controls injection caps and session-level behaviour.
Hooks source this file at startup; missing values fall back to built-in
defaults.

Copy from `seeds/` (or your existing install) and edit integers:

| Variable | Default | What it controls |
|---|---|---|
| `MAX_FILES` | 22 | Total files injected per session (hard ceiling across all types) |
| `MAX_SCORED_TOTAL` | 12 | Adaptive pool shared by feedback / knowledge / strategies / decisions / notes |
| `MAX_CLUSTER` | 4 | Max related-link expansion per seed file |
| `MAX_GLOBAL_MISTAKES` | 3 | Mistakes injected with `project: global` |
| `MAX_PROJECT_MISTAKES` | 3 | Mistakes injected for the current project |
| `CAP_FEEDBACK` | 6 | Per-type cap inside the adaptive pool |
| `CAP_KNOWLEDGE` | 4 | Per-type cap inside the adaptive pool |
| `CAP_STRATEGIES` | 4 | Per-type cap inside the adaptive pool |
| `CAP_DECISIONS` | 3 | Per-type cap inside the adaptive pool |
| `CAP_NOTES` | 2 | Per-type cap inside the adaptive pool |
| `MAX_TRIGGERED` | 4 | Max files injected per prompt match (UserPromptSubmit) |
| `MIN_SESSION_SECONDS` | 120 | Below this, skip the session-stop checkpoint reminder |

Changes take effect from the next session (hooks re-source the file on
start). No restart of Claude Code is required — just start a new
conversation.

**Tuning guidance.** If you raise `MAX_FILES` above ~30, expect context
dilution: more memory also means more noise, and the `-3` noise penalty
in scoring stops filtering effectively. If you lower it below ~10,
expect frequent `shadow-miss` events for relevant-but-cut files.

## 2. Decay thresholds — hardcoded in `hooks/session-stop.sh`

Not currently operator-configurable. Days after `created` before a file
transitions `active → stale`, and days before `stale → archived`
(moved to `~/.claude/memory/archive/<type>/`).

| Type | Stale (days) | Archive (days) |
|---|---|---|
| mistakes | 60 | 180 |
| strategies | 90 | 180 |
| feedback | 45 | 120 |
| knowledge | 90 | 365 |
| decisions | 90 | 365 |
| notes | 30 | 90 |
| journal | 30 | 90 |
| projects | — | — (exempt from rotation) |
| continuity | — | — (auto-generated, not rotated) |

A file can override its type default via the `decay_rate:` frontmatter
field:

| `decay_rate` | Stale (days) | Archive (days) |
|---|---|---|
| `slow` | 180 | 365 |
| `normal` | type default | type default |
| `fast` | 14 | 45 |

`status: pinned` files never decay automatically, regardless of type or
`decay_rate`.

**Source of truth:** `hooks/session-stop.sh:92-104`. If you need
different thresholds, edit that file directly until operator override
lands (see `docs/plans/` for the tracking plan).

## 3. ADR review-triggers — prose in `decisions/*.md`

Each architecture decision record carries a `## When to revisit`
section describing an observable signal that would re-open the
decision. These are **not enforced automatically** — an agent reads
them and compares to `self-profile.md` and WAL metrics by hand.

Examples of triggers currently live in:

- [`docs/decisions/bash-go-gradual-port.md`](decisions/bash-go-gradual-port.md) — pure-bash install becoming untenable, or a parity divergence that can't be scoped to a fixture subset.
- [`docs/decisions/fts5-shadow-retrieval.md`](decisions/fts5-shadow-retrieval.md) — metric-based (user needs an FTS-surfaced record that can't be expressed as a trigger phrase).
- [`docs/decisions/substring-triggers-with-negation.md`](decisions/substring-triggers-with-negation.md) — metric-based (trigger-match persistently below ~10% on a well-populated corpus, or high shadow-miss on slugs with non-empty triggers).

See [`docs/decisions/README.md`](decisions/README.md) for the full index.

To audit current triggers:

```bash
grep -A3 "## When to revisit" docs/decisions/*.md
```

### Automated evaluation (v0.11+)

Each ADR may also declare structured `review-triggers:` in its
frontmatter. Two shapes:

```yaml
review-triggers:
  - metric: measurable_precision   # self-profile | wal | file
    operator: "<"                  # <, <=, >, >=, ==, !=
    threshold: 0.40
    source: self-profile
  - after: "2027-04-22"            # calendar trigger
```

`memoryctl decisions review` evaluates them against the current
`self-profile.md` + WAL snapshot and prints one line per trigger:

```
$ memoryctl decisions review
[pressure] fts5-shadow-retrieval — shadow_miss_ratio = 0.34 > 0.30
[overdue]  bash-go-gradual-port — after 2027-04-22 passed (14 days ago)
[ok]       substring-triggers-with-negation — shadow_miss_ratio = 0.21 (trigger: > 0.60)
```

Exit code 0 if every trigger is `ok`, 1 if any `pressure` / `overdue`.
`memoryctl self-profile` appends a `## Decisions under pressure`
section when triggers fire. `memoryctl doctor` surfaces a single-line
summary.

Directory precedence: `--dir <path>` → `./docs/decisions/` (project-
level ADRs if run from a repo root) → `$CLAUDE_MEMORY_DIR/decisions/`
(personal ADRs). Supported metrics:

| Metric | Source |
|---|---|
| `measurable_precision` | `self-profile.md` |
| `recurrence_top_mistake` | `self-profile.md` |
| `shadow_miss_ratio` | `.wal` (rolling 14-day window) |
| `ref_count` | the ADR file's own frontmatter (`source: file`) |

Unknown metrics route the trigger to `skipped` — the review run
continues rather than failing on a newer schema.

## Why three layers, not one

The three layers exist for different audiences:

- **Runtime config** is for operators who want to tune the system's
  behaviour right now — more files, fewer files, tighter caps.
- **Decay thresholds** are architectural choices embedded in the
  lifecycle model. They shape the project's opinion of what "stale"
  means per type. Operator override is a gap, not a feature.
- **ADR review-triggers** are meta: they decide when the architecture
  itself should change. They are slower-moving, less frequent, and
  (for now) read by agents, not hooks.

Do not conflate them. A runtime-config variable that wants to become an
ADR review-trigger is a signal that you're turning a knob into a
principle. A decay threshold that wants to become runtime-config is a
signal that your type taxonomy is leaking into operator decisions.
