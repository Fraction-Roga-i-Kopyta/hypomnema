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

- `decisions/bash-go-gradual-port.md` — calendar-based (3 months after v1.0)
- `decisions/fts5-shadow-retrieval.md` — metric-based (shadow-miss vs outcome-negative correlation)
- `decisions/substring-triggers-with-negation.md` — metric-based (measurable precision)

To audit current triggers:

```bash
grep -A3 "## When to revisit" ~/.claude/memory/decisions/*.md
```

Automated evaluation — structured `review-triggers:` in frontmatter
plus a `memoryctl decisions review` subcommand — is planned but not yet
implemented. See `docs/plans/2026-04-22-v0.11-decisions-autoreflection.md`.

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
