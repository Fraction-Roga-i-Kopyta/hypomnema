# Hypomnema — Agent Protocol

You are working inside the hypomnema project: a governance + ranking layer for Claude Code's native file memory. This file teaches you how memory is structured, how it gets injected into your sessions, and when to write to it.

## What hypomnema does

Claude Code v2.1.59+ ships native file memory: the harness stores markdown files in `~/.claude/projects/<slug>/memory/` and injects only a `MEMORY.md` index. Hypomnema adds what native lacks:

- **Ranked auto-injection** — `memoryctl inject` (SessionStart + UserPromptSubmit shims) ranks native facts by relevance and injects the top-K via `additionalContext`, not just a table of contents.
- **Effectiveness measurement** — WAL tracks `trigger-useful`/`trigger-silent` per session; Bayesian effectiveness feeds back into ranking.
- **Decay + lifecycle** — `memoryctl close` (Stop shim) down-ranks stale facts in the sidecar; content is never mutated by hooks.
- **Secrets gate** — `memoryctl guard` (PreToolUse:Write shim) blocks credential patterns before they land in a memory file.
- **Global store** — `~/.claude/memory-global/` for facts that apply across every project (native memory is per-project only).

The point: you remember things across sessions, and the things most likely to matter surface first.

## Memory layout

Native memory files live in `~/.claude/projects/<slug>/memory/` (project-local) and `~/.claude/memory-global/` (global). There are no fixed subdirectories — type is encoded in the file's `type:` frontmatter field. The **sidecar** (`memory.db`, SQLite, rebuildable from WAL + frontmatter) holds metadata: ref_count, effectiveness, status, keywords, domains. The **WAL** (`.wal`, append-only text) is the source of truth for all events.

Logical types:

| Type | Contents |
|---|---|
| `mistake` | Bugs you should not repeat |
| `strategy` | Proven approaches that worked |
| `feedback` | Behavioral rules from the user |
| `knowledge` | Domain facts, API docs |
| `decision` | Architecture & technology choices |
| `note` | Long-form references |
| `project` | Project description |
| `continuity` | "Where we left off" last session |

Injection budget: a single ranked **top-8** across all types, capped at
**2.5KB per body and 8KB total** (Claude Code diverts oversized hook output
to a file the model never reads inline, so the budget is what keeps memory
actually visible). A fact injects **once per session** — later prompts only
add facts that newly enter the top-8. Per-type quotas are not implemented;
type balance emerges from relevance.

## Frontmatter schema

Every memory file is a markdown document with YAML frontmatter. Native-required fields (`name`, `description`, `type`) plus hypomnema-specific fields you write:

```yaml
---
type: mistake | strategy | feedback | knowledge | decision | project | continuity | note
name: "short-slug"           # sidecar key; kebab-case
description: "one-liner"     # shown in MEMORY.md index and injection headers
created: YYYY-MM-DD          # you set — recency signal for ranking
status: active | pinned      # stale is sidecar-managed, not hand-set
keywords: [tag1, tag2, tag3] # primary ranking signal
domains: [domain1, domain2]  # domain filter — use kebab-case for multi-word
---
```

`ref_count` and `effectiveness` are sidecar-managed — `memoryctl` reads and updates them. Do not set them manually.

**Tolerated syntax variants** (parser normalises them):
- Tab after a key (`status:\tactive`) works the same as a space
- Quoted scalars (`status: "active"`) are unquoted on read
- Block-style arrays collapse into a single list the same way as inline `[api, auth]`
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
success_count: <integer>
```

**feedback** (additional):
```yaml
evidence:                  # phrases that signal the rule applies (case-insensitive)
  - "phrase one"
  - "phrase two"
precision_class: ambient   # optional — excludes file from precision denominator
```

`evidence:` is type-agnostic — the close hook reads it from any injected memory file (mistake, strategy, knowledge, …). If body mining would give ambiguous tokens for a given rule, add explicit `evidence:` regardless of type.

**Sizing — keep it short.** ≤5 carefully-chosen phrases score best. Long evidence lists pile on specific wordings that rarely appear verbatim in assistant text, so the classifier ends up marking the file silent. Pick phrases the assistant would actually write when applying the rule, not exhaustive paraphrases.

`precision_class: ambient` marks rules that shape behaviour silently (e.g. language preference, security baseline, meta-philosophy). These files continue to be injected and ranked normally; they are excluded from the self-profile precision denominator so they don't drag the ratio down.

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
name: "api-auth-skipped"
description: "Built API client without reading auth docs first"
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

You discovered an approach that worked for a class of problems. Keywords should reflect the situation that should trigger retrieval of this strategy.

```markdown
---
type: strategy
name: "flaky-test-ci-local"
description: "Diagnose tests that pass locally but fail on CI"
project: global
created: 2026-04-15
status: active
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
name: "real-db-in-tests"
description: "Integration tests must hit a real database, not a mock"
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

### Feedback-as-mistake: blurry boundary

A behaviour rule given by the user that corrects a **repeated** agent mistake is a valid projection of both types. Write it as `feedback` (shorter, evidence-based); do NOT also write a separate `mistake` file for the same rule — that duplicates signal and confuses Bayesian effectiveness scoring.

### When to write `knowledge`

Domain facts, API specifics, infrastructure details that future sessions need to remember and that are not obvious from the code.

### When to write `decision`

An architecture or technology choice you want future sessions to defer to rather than re-litigate. Capture the alternatives considered and the specific trade-offs that made you pick this one. Prefer ADR-style (`What` / `Why` / `Trade-off accepted` / `When to revisit`).

### When to write `continuity`

At session end, if you stopped mid-task. Three lines max: what you were doing, current status, next step. File path: `continuity/{project-slug}.md` inside native memory.

## When NOT to write

- Code patterns and conventions — read the code instead.
- Git history — `git log` and `git blame` are authoritative.
- Debugging solutions specific to one bug — fix lives in the code; explanation in the commit message.
- Conversation context — that is what the current session is for.
- Anything already documented in CLAUDE.md files (project or global).

## Lifecycle

`memoryctl close` runs after every turn (Claude Code fires Stop per turn):
- Classifies the session's injected set as `trigger-useful` / `trigger-silent` (evidence phrases or slug/name citation in assistant text) and writes the events to the WAL.
- Recomputes effectiveness from the WAL: `(pos+1)/(pos+neg+2)` over legacy `outcome-*` events **plus** one trigger observation per (slug, session) — useful wins over silent within a session.
- Marks facts `stale` in the sidecar when unused past their type threshold — age counts from **last injection** (fallback `created`), so facts in rotation stay alive. No native content mutation.
- Archiving (physical move out of the store) is planned but not shipped; stale facts simply stop injecting.
- `status: pinned` files and `continuity`/`project` facts never decay.

## How injection ranks files

A single relevance ranker (v2 merged the two v1 pipelines into one). The
score is an additive blend — no single zero signal annihilates a candidate:

```
score = 3.0 × overlap(session_keywords, file keywords+name+description+body)
      + 1.0 × log10(1 + ref_count)     # diminishing-returns on heavily-injected records
      + 2.0 × recency                  # 1/(1 + days/30) from last injection (fallback created)
      + 2.0 × effectiveness            # Bayesian (pos+1)/(pos+neg+2); neutral 0.5 until signal lands
      + 1.0 if project-local           # project facts outrank global ones on ties
```

**Zero-safe:** a new fact with `ref_count=0` and no outcomes gets the neutral prior and its frontmatter `created` as recency — it is injectable from day one.

Status filter: `active` and `pinned` only (sidecar-managed `stale` is excluded). Scope filter: only the current project's facts plus the global store — other projects' rows never inject. Result cap: top-8, 8KB total, once per session per fact.

`session_keywords` come from prompt tokens, CWD basename, and git context (branch name, changed filenames, recent commit subjects) on both SessionStart and UserPromptSubmit (reactive re-rank).

**What changed from v1:** the old substring-trigger mechanism and negation-window logic are gone. The ranker treats all tokens as relevance signal. The two v1 pipelines (composite-score SessionStart + deterministic UserPromptSubmit) are now one. TF-IDF body scoring, FTS5 shadow retrieval, and cold-start gates are retired.

You do NOT need to set ranks manually. Write good `keywords` and `domains`, let `memoryctl inject` handle the rest.

## Secrets detection

`memoryctl guard` is the PreToolUse gate (matcher `Write|Edit`) on memory paths — the legacy `~/.claude/memory` tree, every native `~/.claude/projects/<slug>/memory/` store, and `~/.claude/memory-global/`. It blocks writes whose body contains a recognised credential pattern (`api_key` / `secret` / `password` / `token` / AWS key variants) outside fenced code blocks with value length ≥ 8. Blocked writes exit 2 with a stderr message naming the file and line.

Escape hatches:
- Whitelist a path permanently: add its glob to `~/.claude/memory-global/.secretsignore`.
- Override for a single invocation: `HYPOMNEMA_ALLOW_SECRETS=1`.

Do not route around the gate by stripping the value; either whitelist the path or rewrite the text to use a clearly placeholder value.

## Reading what was injected

Look at the start of your context for a `# Memory Context` block with one `## <name>` section per injected fact (large hook payloads may arrive as a persisted-output file reference — the 8KB budget exists precisely to avoid that).

## Subagent context

There is no auto-generated subagent context file (`_agent_context.md` was retired with v1). When you spawn a subagent, pass the relevant facts inline in its prompt.

## Source of truth

- v2 architecture spec — `docs/specs/2026-05-28-v2-native-memory-design.md`
- A/B ranker evidence — `docs/measurements/2026-05-29-v2-ranker-ab.md`
- Full schema, hook reference, ADRs — `README.md` and `docs/`
- `memoryctl` implementation — `cmd/memoryctl/` + `internal/`
- Templates for each type — `templates/`
- Seed examples (in native format) — `seeds/`
- Onboarding — `docs/QUICKSTART.md`
- Common issues — `docs/TROUBLESHOOTING.md`
- v1.x → v2.0 migration — `docs/MIGRATION.md`
