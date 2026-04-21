# Hypomnema — Agent Protocol

You are working inside the hypomnema project: a file-based long-term memory system for Claude Code agents. This file teaches you how memory is structured, how it gets injected into your sessions, and when to write to it.

## What hypomnema does

At session start, a hook reads `~/.claude/memory/`, ranks files by relevance (keywords + WAL spaced repetition + TF-IDF), and injects a curated subset into your context. At session stop, another hook reminds you to record what you learned. Other hooks dedupe, track outcomes, and decay stale entries.

The point: you remember things across sessions without a database, embeddings, or cloud.

## Memory layout

`~/.claude/memory/` contains nine subdirectories. Decay column shows **stale → archive** in days: files flip `status: active → stale` at the stale threshold and move to `archive/` at the archive threshold.

| Directory | Contents | Injected at start | Stale → Archive |
|---|---|:-:|---|
| `mistakes/` | Bugs you should not repeat | yes (3 global + 3 per project) | 60 → 180 |
| `strategies/` | Proven approaches that worked | yes (cap 4) | 90 → 180 |
| `feedback/` | Behavioral rules from the user | yes (cap 6) | 45 → 120 |
| `knowledge/` | Domain facts, API docs | yes (cap 4) | 90 → 365 |
| `decisions/` | Architecture & technology choices | yes (cap 3) | 90 → 365 |
| `notes/` | Long-form references | yes (cap 2) | 30 → 90 |
| `projects/` | Project descriptions | yes (current only) | no rotation |
| `continuity/` | "Where we left off" last session | yes (current only) | auto-generated, not rotated |
| `journal/` | Session logs | no | 30 → 90 |

`feedback/`, `knowledge/`, `strategies/`, `decisions/`, `notes/` share an adaptive 12-slot pool — if one type has fewer relevant files, the budget flows to others. The pool shrinks to 8 or 10 slots when keyword signal is strong (≥10 or ≥5 keywords respectively), so noise does not dilute high-confidence matches.

## Frontmatter schema (universal)

Every memory file is a markdown document with YAML frontmatter. Fields marked **auto** are managed by hooks; set everything else yourself.

```yaml
---
type: mistake | strategy | feedback | knowledge | project | continuity | decision | note
project: global | <project-slug>
created: YYYY-MM-DD          # you set — used by recency-boost ranking
injected: YYYY-MM-DD          # auto — first-injected timestamp, SessionStart writes it
referenced: YYYY-MM-DD        # auto — SessionStart refreshes it on every inject
ref_count: 0                  # auto — SessionStart increments on every inject
status: active | pinned | stale | archived
description: <one-line description>   # used in MEMORY.md index
keywords: [tag1, tag2, tag3]
domains: [domain1, domain2]            # use kebab-case for multi-word
---
```

`ref_count` feeds the `log₁₀(ref_count)` bucket of the priority key (see "How injection ranks files"). New templates ship with `ref_count: 0` so the field exists for the auto-increment pass to find.

**Tolerated syntax variants** (parser normalises them):
- Tab after a key (`status:\tactive`) works the same as a space
- Quoted scalars (`status: "active"`) are unquoted on read
- Block-style arrays (`keywords:\n  - api\n  - auth`) collapse into a single list the same way as inline `[api, auth]`
- Multi-line block scalars (`root-cause: |`) are **not** supported — use a single-line scalar or put the prose in the body

## Type-specific fields

**mistake** (additional):
```yaml
severity: minor | major | critical
recurrence: <integer>
scope: universal | domain | narrow     # narrow = inject only on keyword match
root-cause: "..."
prevention: "..."
```

**strategy** (additional):
```yaml
trigger: "..."             # one situation cue (substring-matched against prompt)
# — OR — block-style array for multiple triggers:
triggers:
  - "situation cue A"
  - "situation cue B"
success_count: <integer>
```

Both singular `trigger:` (legacy) and `triggers:` (array) are accepted; UserPromptSubmit merges them into a single phrase list.

**feedback** (additional):
```yaml
evidence:                  # phrases that signal the rule applies (case-insensitive)
  - "phrase one"
  - "phrase two"
precision_class: ambient   # optional (v0.8.1+) — excludes file from precision denominator
```

`evidence:` is listed here because feedback files use it most, but the field is type-agnostic — the session-stop feedback detector reads it from any injected memory file (mistake, strategy, knowledge, …). If body mining would give ambiguous tokens for a given rule, add explicit `evidence:` regardless of type.

`precision_class: ambient` marks rules that shape behaviour silently and cannot produce `trigger-useful` events by design (e.g. language preference, security baseline, meta-philosophy). Such files continue to be injected and ranked normally; they are simply excluded from the self-profile precision denominator so they do not drag the ratio down as false noise. Use it only when the rule is applied without being referenced in text — not to mask genuinely noisy triggers.

**knowledge / decision / note** (additional):
```yaml
related: [other-slug-1, other-slug-2]   # optional cluster links
```

## Writing protocol

### When to write a `mistake`

You (or a similar agent) made a bug, took a wrong approach, or repeated an error. Record root cause and prevention. Do not record one-off implementation details.

```markdown
---
type: mistake
project: global
created: 2026-04-15
status: pinned
severity: major
recurrence: 1
scope: universal
keywords: [api, auth, planning, rewrite]
domains: [backend, integration]
root-cause: "Built the API client without checking auth flow first; ended up rewriting after discovering the service rejects unsigned requests."
prevention: "Read auth section of API docs before writing the first request."
---

## Context
Wrote a generic HTTP wrapper, then discovered the service requires HMAC-signed requests. Three hours of refactoring.

## Why this happened
Skipped reading the security section assuming "API key is enough."

## How to avoid
First read auth/security section. List required headers, signing scheme, token lifecycle. Then write the first request.
```

### When to write a `strategy`

You discovered an approach that worked for a class of problems. `trigger` should be the situation cue that should make a future agent reach for this strategy.

```markdown
---
type: strategy
project: global
created: 2026-04-15
status: active
trigger: "flaky test in CI but passes locally"
success_count: 1
keywords: [testing, ci, flaky, async, race]
domains: [testing]
---

## Steps
1. Run the test 50 times locally — if it goes flaky, it is genuinely racy.
2. If green locally and red on CI, suspect environment: clock skew, slower CPU, network.
3. Add explicit waits on observable conditions (event/state), not arbitrary sleeps.

## Outcome
Used three times; replaced 12 sleep-based waits with event waits, eliminated the flake.
```

### When to write `feedback`

The user corrected your approach OR validated a non-obvious choice. Record the rule, the reason (Why), and when to apply it.

```markdown
---
type: feedback
project: global
created: 2026-04-15
status: pinned
keywords: [tests, integration, mocking]
domains: [testing, backend]
evidence:
  - "do not mock the database"
  - "real database in tests"
  - "integration tests"
---

Integration tests must hit a real database, not a mock.

**Why:** A previous incident — mocked tests passed but the production migration failed because the mock did not reflect the actual schema constraints.

**How to apply:** When writing a test that touches the persistence layer, spin up the real database (Docker, ephemeral schema) instead of substituting a mock object.
```

### When to write `knowledge`

Domain facts, API specifics, infrastructure details that future sessions need to remember and that are not obvious from the code.

### When to write `continuity`

At session end, if you stopped mid-task. Three lines max: what you were doing, current status, next step. File path: `continuity/{project-slug}.md`.

## When NOT to write

- Code patterns and conventions — read the code instead.
- Git history — `git log` and `git blame` are authoritative.
- Debugging solutions specific to one bug — fix lives in the code; explanation in the commit message.
- Conversation context — that is what the current session is for.
- Anything already documented in CLAUDE.md files (project or global).

## Lifecycle

The Stop hook rotates files using the two-stage thresholds from the memory-layout table:
- `status: active` files older than the type's **stale** threshold → `status: stale` (still on disk, still injected if nothing fresher matches).
- `status: stale` files older than the **archive** threshold → moved to `~/.claude/memory/archive/<type>/` (not injected).
- `status: pinned` files never decay automatically.
- Archived files are kept indefinitely; manually move one back and set `status: active` to revive it.
- `continuity/` and `projects/` are exempt from rotation — they represent per-project state, not decay-able records.

## How injection ranks files

Two call sites use two different rankings — keep them straight:

**UserPromptSubmit** (trigger match → priority key). Higher wins, ties broken left-to-right:

1. **Project** — current project > `global` > other project.
2. **Status** — `pinned` > `active`.
3. **Severity** — `critical` > `major` > `minor` > unset.
4. **Recency (by `created`)** — ≤7d > ≤30d > ≤90d > older.
5. **log₁₀(`ref_count`)** — diminishing-returns bucket (0, 1, 2, 3+).
6. **`recurrence`** — higher wins.
7. **`ref_count`** — raw value as last tiebreaker.

**SessionStart** (context assembly) uses a composite **score**, not a pure priority key:

```
score = keyword_hits × 3
      + project_boost × 1              (file.project == current project)
      + wal_spaced_repetition (0–10)   (files that proved useful recur faster)
      + strategy_bonus (min(success_count × 2, 6))
      + tfidf_body_match × 2 (capped)  (only for records with no keywords)
      − 3 if record matches no active signals (noise penalty)
```

Filter first (project ∈ {current, global}; status ∈ {active, pinned}; domains intersect current; recurrence ≥ threshold), then score, then apply adaptive quotas (3+3 mistakes; 12/10/8-slot flex pool depending on keyword signal strength).

You do NOT need to set ranks manually. Write good frontmatter, let the hooks do the rest.

## Shadow retrieval signal (v0.8)

Parallel to the substring-trigger pipeline, a background pass runs FTS5/BM25 search over memory body content for every user prompt. Files the shadow pass surfaces but the primary pipeline never triggered are logged as `shadow-miss|<slug>|<session_id>` WAL events — a recall signal for trigger tuning.

What this means for you as an agent:

- **Do not edit `~/.claude/memory/index.db`** — it is a derivative SQLite FTS5 store, rebuilt automatically on drift by `bin/memory-fts-sync.sh`. Delete it and it will regenerate from markdown content on next query.
- **`shadow-miss` events are diagnostic, not prescriptive** — they do not appear in your context, they only accumulate in the WAL. When you see many `shadow-miss` hits for the same slug in `.wal` during analysis, it means that file's `triggers:` are too narrow and should be expanded to match how users actually phrase the problem.
- **The shadow pass is fail-safe** — any error inside `memory-fts-shadow.sh` exits silently. It never blocks the main hook or changes what gets injected. Treat it as observational instrumentation.

## Reading what was injected

Look at the start of your context for `# Memory Context` and `## Active Mistakes` / `## Feedback` / etc. sections. Files marked `[ПРИОРИТЕТ]` are flagged as contradicting another active record — read both before acting.

## Subagent context

If you spawn a subagent, point it at `~/.claude/memory/_agent_context.md` — it is an auto-generated compact summary regenerated each Stop hook.

## Source of truth

- Schema, scoring, full hook reference — `README.md`
- Hook implementations — `hooks/`
- Templates for each type — `templates/`
- Seed examples (starter mistakes) — `seeds/`
- Onboarding — `docs/QUICKSTART.md`
- Common issues — `docs/TROUBLESHOOTING.md`
- Cross-machine sync, upgrades — `docs/FAQ.md`
- Version migration — `docs/MIGRATION.md`
