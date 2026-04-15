# Hypomnema — Agent Protocol

You are working inside the hypomnema project: a file-based long-term memory system for Claude Code agents. This file teaches you how memory is structured, how it gets injected into your sessions, and when to write to it.

## What hypomnema does

At session start, a hook reads `~/.claude/memory/`, ranks files by relevance (keywords + WAL spaced repetition + TF-IDF), and injects a curated subset into your context. At session stop, another hook reminds you to record what you learned. Other hooks dedupe, track outcomes, and decay stale entries.

The point: you remember things across sessions without a database, embeddings, or cloud.

## Memory layout

`~/.claude/memory/` contains nine subdirectories:

| Directory | Contents | Injected at start | Decay |
|---|---|:-:|---|
| `mistakes/` | Bugs you should not repeat | yes (3 global + 3 per project) | slow (180d) |
| `strategies/` | Proven approaches that worked | yes (cap 4) | medium (90d) |
| `feedback/` | Behavioral rules from the user | yes (cap 6) | slow (180d) |
| `knowledge/` | Domain facts, API docs | yes (cap 4) | medium (90d) |
| `decisions/` | Architecture & technology choices | yes (cap 3) | slow (365d) |
| `notes/` | Long-form references | yes (cap 2) | slow (180d) |
| `projects/` | Project descriptions | yes (current only) | none |
| `continuity/` | "Where we left off" last session | yes (current only) | fast (14d) |
| `journal/` | Session logs | no | fast (30d) |

`feedback/`, `knowledge/`, `strategies/`, `decisions/`, `notes/` share an adaptive 12-slot pool — if one type has fewer relevant files, the budget flows to others.

## Frontmatter schema (universal)

Every memory file is a markdown document with YAML frontmatter:

```yaml
---
type: mistake | strategy | feedback | knowledge | project | continuity | decision | note
project: global | <project-slug>
created: YYYY-MM-DD
injected: YYYY-MM-DD       # auto-updated by SessionStart
referenced: YYYY-MM-DD     # auto-updated when read by hooks
status: active | pinned | archived
description: <one-line description>   # used in MEMORY.md index
keywords: [tag1, tag2, tag3]
domains: [domain1, domain2]            # use kebab-case for multi-word
---
```

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
success_count: <integer>
```

**feedback** (additional):
```yaml
evidence:                  # phrases that signal the rule applies (case-insensitive)
  - "phrase one"
  - "phrase two"
```

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

The Stop hook rotates files based on age and decay rate:
- `status: active` files older than the type's stale threshold → `status: archived` (still on disk, not injected).
- `status: pinned` files never archive automatically.
- Archived files are kept indefinitely; you can manually re-pin them.

## How injection ranks files

1. **Project filter** — files matching the current project rank above `project: global`.
2. **Status** — `pinned` > `active`.
3. **Severity / recurrence** — for mistakes, higher = injected first.
4. **WAL spaced repetition** — files that were injected and then proved useful (positive outcome) bubble up.
5. **TF-IDF body match** against current keywords — relevance to the current task.

You do NOT need to set ranks manually. Just write good frontmatter and the hooks do the rest.

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
