# Hypomnema

[![CI](https://github.com/Fraction-Roga-i-Kopyta/hypomnema/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/Fraction-Roga-i-Kopyta/hypomnema/actions/workflows/test.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/Fraction-Roga-i-Kopyta/hypomnema?include_prereleases&sort=semver)](https://github.com/Fraction-Roga-i-Kopyta/hypomnema/releases)

**Working memory for your AI agent, not a second brain for you.** Hypomnema decides what Claude Code knows when a session or a subagent starts: the project's current state, the rules you gave it, the mistakes it already made. It is a governance and ranking layer over Claude Code's native file memory — a Go engine (`memoryctl`) + 9 thin shims. No cloud, no embeddings, no vault.

> **Status:** v3.2.2; native-primary. Requires Claude Code ≥ v2.1.59 (native file memory). v1.x stays on its tag for older Claude Code installs — see [MIGRATION.md](docs/MIGRATION.md) for the v1.x → v2.x upgrade path.
>
> **Platforms:** macOS (primary, daily-driver) and Linux (`ubuntu-latest`), both covered by CI. The core is the Go binary; the shims are ~5-line `sh` marshallers. Windows: WSL only, native unsupported.
>
> _Not affiliated with or endorsed by Anthropic. Hypomnema is an independent project that integrates with Claude Code's public hook API._

## Who reads this memory

Not you. Hypomnema is not another markdown "second brain" — no Obsidian vault, no backlinks graph, no daily notes, nothing to browse or curate. Every fact is written for one reader: the agent, at the moment it starts work.

- **The agent writes it.** Claude records a bug it should not repeat, an approach that worked, a rule you gave it. You can open, edit or delete any file, but you are not expected to maintain a knowledge base.
- **The agent reads it.** A fresh session starts with the project's current state, its pinned card and your rules for it; a subagent gets only the facts its task names; everything else is listed in an index the agent reads when a description fits. That last part describes the recommended pull-only mode (`HYPOMNEMA_PROMPT_INJECT=off` plus an `@~/.claude/memory-global/MEMORY.md` import in your user CLAUDE.md — see [Setup](#recommended-pull-only-mode)); by default UserPromptSubmit still pushes a gated top-8 on every prompt.
- **Success is measured on the agent.** A fact counts as useful only when it changed what Claude said, did or decided — Claude marks that with a hidden citation. How many notes are stored, or how they look to a human, is not a goal.
- **Priorities, in order:** what reaches the agent's context is accurate → it is fast → it makes the agent more capable → anything a human sees. Human-facing features are dropped when they cost injection quality.

The human side is a control panel, not a reading app: `/memboard` shows what the agent got and lets you hide a fact, show it less, or ask Claude to rewrite it.

## What it looks like

When you open a new Claude Code session in a project, hypomnema injects a `# Memory Context` block before the conversation starts — the session's fixed core: where the work stopped, the project's pinned card, and the rules you gave the agent in this project:

```markdown
# Memory Context
When a fact below changes what you say, do, or decide, wrap that sentence in <cc-memory filenames="FILE">…</cc-memory> (the tag is hidden from the user).

## continuity — continuity.md (continuity, 2026-11-24)
Doing: migrate the billing webhooks to the v2 API. Status: 3 of 5 handlers ported, tests green.
Next: port refund + dispute handlers; staging run before merge.

## billing-service — billing-service.md (project, 2026-09-02)
Repo layout, how to run the stack locally, deploy is via PR to main only, never push to prod directly.

## real-db-in-tests — real-db-in-tests.md (feedback, 2026-02-14)
Integration tests must hit a real database, not a mock. Why: a mocked test passed while the migration failed.
```

Claude reads it as part of its context — no RAG query, no vector database, just a markdown block prepended by the hook. Everything else stays out of the way: in pull-only mode (recommended, [Setup](#recommended-pull-only-mode)) the agent sees the store's `MEMORY.md` index and reads a fact when its description fits the task — by default each prompt still gets a gated ranked push; a subagent gets only the facts its own task names. The file name in each header is what a citation names: when a fact actually changes what Claude says or does, it wraps that sentence in `<cc-memory filenames="real-db-in-tests.md">…</cc-memory>` — hidden from the user, read by `close` as the honest usefulness signal. When Claude hits a new bug worth remembering, it writes a native memory file; the next time a task names that bug, the agent finds it. The core comes back after `/clear` and compaction — the core only: the compaction summary already carries the task — so a compacted session doesn't wake up having forgotten where it was.

## What hypomnema adds on top of native

Claude Code v2.1.59+ ships native file memory: `~/.claude/projects/<slug>/memory/` holds markdown files, and the harness injects only an auto-generated `MEMORY.md` index. That covers capture and raw listing — but not the parts that make memory actually useful over time.

Hypomnema adds:

| Gap in native | What hypomnema provides |
|---|---|
| Native injects only an index, not ranked content | `memoryctl inject` ranks native facts by relevance (keyword overlap + recency + project boost) and injects the top-K via `additionalContext` — not just a table of contents |
| Subagents start with a blank context | `SubagentStart` injects only the facts the subagent's own launch task names (top-5; nothing without a task), under a key that keeps its citations separate from the parent session |
| No per-session effectiveness signal | WAL captures `<cc-memory>` citations (`cite-useful`/`cite-silent`) per session; effectiveness is tracked as a diagnostic |
| No decay / lifecycle | `memoryctl close` down-ranks stale facts in the sidecar; nothing is deleted from disk |
| No secrets gate | `memoryctl guard` (PreToolUse:Write\|Edit) blocks credential patterns before they land in a memory file |
| No global store | Native memory is per-project only; hypomnema owns `~/.claude/memory-global/` for facts that travel across every project (language rules, universal debugging patterns) |
| No migration path | `memoryctl migrate` converts a v1.x hypomnema store to native format + sidecar in one shot |

An A/B replay on the maintainer's corpus suggested ranked injection recovers
several times more useful memories per budget slot than a random baseline.
Treat that number as indicative, not proven: the ground-truth labels
(`cite-useful`) can only exist for facts the ranker itself injected, so the
comparison measures agreement with past injection policy more than retrieval
quality against an unbiased oracle, and the lift scales with the candidate-pool
size. See [`docs/measurements/2026-05-29-v2-ranker-ab.md`](docs/measurements/2026-05-29-v2-ranker-ab.md) for the method and caveats.

## Architecture at a glance

```
┌─ Claude Code harness ───────────────────────────────────────┐
│  native memory: ~/.claude/projects/<slug>/memory/*.md       │
│  • content (minimal frontmatter: name/description/type)     │
│  • MEMORY.md index — injected by harness                    │
└───────────────▲───────────────────────────▲─────────────────┘
       read-only │ (content)       write-intercept │ (guard)
┌────────────────┴───────────────────────────┴────────────────┐
│  hypomnema v2 — memoryctl (Go) + 9 thin sh shims            │
│                                                             │
│  inject ──► additionalContext (ranked top-K)                │
│  inject (SubagentStart) ──► own top-5 for its task          │
│  close  ──► classify → WAL → effectiveness/decay → index    │
│  close --subagent ──► classify under its own key            │
│  guard  ──► secrets gate (PreToolUse:Write|Edit)            │
│  skill-inject/skill-active ──► per-skill learnings          │
│  migrate──► one-shot v1 conversion + pruning                │
│                                                             │
│  WAL (append-only text) = source of truth for events        │
│  SQLite sidecar = derived projection (rebuildable)          │
│  ~/.claude/memory-global/ = global store (native format)    │
└─────────────────────────────────────────────────────────────┘
```

**Nine hooks, nine shims** — each a few lines of marshalling, with the logic in `memoryctl`:

| Event (matcher) | Shim | What memoryctl does |
|---|---|---|
| `SessionStart` | `session-start.sh` | Detect project → inject the fixed core (freshest continuity, pinned facts, project feedback) via `additionalContext` — after `/clear` and compaction too |
| `UserPromptSubmit` | `user-prompt-submit.sh` | Re-rank with prompt tokens added → inject anything not yet in session |
| `PreToolUse:Write\|Edit` | `pre-tool-write.sh` | `guard`: secrets scan on memory-path writes → exit 2 on a credential pattern |
| `PreToolUse:Bash` | `pre-tool-bash.sh` | `guard-bash`: block the model's own `memoryctl pin` / `unpin` / `mark` (only `mark <slug> done` passes) — those are `/memboard` panel actions |
| `PreToolUse:Skill` | `skill-active.sh` | Record which skill just activated (marker for the capture path) |
| `PostToolUse:Skill` | `skill-learnings-inject.sh` | `skill-inject`: surface accumulated learnings for the activated skill |
| `Stop` | `session-stop.sh` | `close`: classify the injected set by `<cc-memory>` citation → WAL → recompute effectiveness/decay → regenerate `MEMORY.md` |
| `SubagentStart` | `subagent-start.sh` | Facts the subagent's own launch task names (head gate) → inject its own top-5; no task → nothing |
| `SubagentStop` | `subagent-stop.sh` | `close --subagent`: classify the subagent's own citations (including its hand-back report) under its own key |

`guard-bash` stops accidental or injected *CLI* calls — say, a prompt
injection telling the model to `hide` a user rule or `pin` a poisoned fact.
It is **not a security boundary**: a model can still append to the WAL or
edit a memory file directly. Its shim blocks only on the binary's
`BLOCKED (hypomnema)` marker, so an older `memoryctl` without the verb is a
no-op, never a blocked Bash call.

The project's native `MEMORY.md` index is regenerated from native files on
close; ranked facts arrive separately through `additionalContext` — we don't
fight the harness for the index channel.

## How injection ranks files

**SessionStart injects a fixed core, not a ranking:** the project's freshest `continuity`, then `status: pinned` facts (project-local, then global), then project-local `type: feedback` — freshest first within each group. Facts that are stale, hidden, or marked *less* in `/memboard` are left out. Core facts that do not fit the 8 KB budget are listed as one-line pointers (name, absolute path, description) that the model can Read; the Memory-queue line's room is reserved first. There is no ranked fill: memory beyond the core arrives by pull (the `MEMORY.md` index, `memoryctl recall`). `/clear` and compaction inject the core only, too: the compaction summary carries the task state, and gating the summary on fact heads was measured to pass a median of 187 facts on real summaries — no selection at all.

Everywhere else, a single relevance ranker (merged from the two v1 pipelines) scores every candidate:

```
score = 3.0 × overlap(keywords, file keywords+name+description+body)
      + 2.0 × recency                  # 1/(1 + days/30) from last USEFUL citation (cite-useful; fallback created)
      + 1.0 × project boost            # project-local facts beat global ones on ties
```

`ref_count` and `effectiveness` are not ranking inputs: injection volume feeds the first and `cite-silent` (a consequence of being injected) feeds the second, so using them as rank signals was circular. They stay computed and visible as diagnostics in `/memboard`, `memoryctl stats`, `doctor` and `sidecar show`. `memoryctl ab` replay therefore measures the recency of usefulness only.

Recency tracks the model's use, not the ranker's own output: it decays from the latest `cite-useful` date (sidecar `last_useful`), never from `last_injected`, so a fact can't refresh its own recency merely by being injected.

The blend is additive and zero-safe: a brand-new fact with `ref_count=0` and no outcome history is still injectable — it scores on overlap and its frontmatter `created` recency, not zero. Status filter: `active`, `pinned` and `candidate`; `stale` is skipped (down-ranked in sidecar, still on disk; `recall` still serves it). Scope filter: only the current project's facts plus the global store inject — other projects' rows never leak in. The result is a flat top-8, capped at 2.5KB per body and 8KB total (Claude Code diverts larger hook payloads to a file the model never sees inline), and each fact injects at most once per context (again after a compaction/`/clear` wipes it).

Keyword overlap is the primary signal. Keywords come from git context (branch name, changed filenames, recent commit messages, CWD basename — skipped for the home directory) plus prompt tokens. On UserPromptSubmit a fact injects only if a prompt term is in its name/description/keywords/domains, or it shares at least two terms with the prompt. `HYPOMNEMA_PROMPT_INJECT=off` turns per-prompt injection off entirely (pull-only mode: the `MEMORY.md` index plus a global index imported from CLAUDE.md, and the model's own reads). The old substring-trigger mechanism is gone; the ranker treats all tokens as relevance signal, not imperative triggers.

You do NOT need to set ranks manually. Write good frontmatter (`keywords`, `domains`), let `memoryctl inject` handle the rest.

Injection is push: the ranker sees the user's prompt, not what the agent
hits mid-task. `memoryctl recall` is the pull counterpart — an explicit query
against the same ranker. A recalled fact counts as delivered: it joins the
session's injected set (the close hook classifies it useful/silent), bumps
its ref_count and staleness clock, and — if it had gone stale — comes back to life.

## What gets remembered

| `type:` | What |
|------|------|
| **mistake** | Bugs, wrong approaches, repeated errors |
| **strategy** | Proven approaches that worked |
| **feedback** | Behavioral rules (why + how to apply) |
| **knowledge** | Domain facts, API docs, infrastructure |
| **decision** | Architecture & technology choices |
| **note** | Long-form knowledge, references |
| **project** | Ongoing work, goals, constraints (Claude Code's own type) |
| **continuity** | "Where we left off" last session |
| **skill-learning** | Usage-learned knowledge bound to a skill (`skill:` field), injected when that skill activates |

The kind is the singular `type:` frontmatter value, not a subdirectory — the
stores are flat. All types compete in one ranked top-8 — there are no per-type
quotas; type balance emerges from relevance. Only a project's freshest
`continuity` fact (by `created`/`modified`) is pushed and exempt from staleness
rotation; older continuity files are history — still recallable, aging like
notes.

### Example: a mistake

```markdown
---
type: mistake
name: "api-auth-skipped"
description: "Built API client without reading auth docs first"
created: 2026-04-15
status: pinned
severity: major
recurrence: 1
keywords: [api, auth, planning, rewrite]
domains: [backend, integration]
root-cause: "Built the API client without checking auth flow first."
prevention: "Read auth section of API docs before writing the first request."
---

Read the auth/security section before the first request: required headers,
signing scheme, token lifecycle. The injection renders the **body**, so the
prose lives here — frontmatter is metadata and ranking signal.
```

This gets injected when relevant. After the fifth time, Claude stops doing it.

## How hypomnema compares

| | What it does | Hypomnema's position |
|---|---|---|
| **Claude Code native memory** (v2.1.59+) | Files in `~/.claude/projects/<slug>/memory/`, `MEMORY.md` index injected by harness | Hypomnema ranks those same files and injects the top-K via `additionalContext`; adds global store, effectiveness tracking, decay, secrets gate. Layered on top, not competing. |
| **`CLAUDE.md` files** (Anthropic native) | Static context injected verbatim, in-repo or `~/.claude/CLAUDE.md` | Hypomnema is dynamic: ranking, decay, per-session signal, WAL feedback loop. The two are complementary. |
| **`mem0`** (Python, hosted or self-host) | Vector-embedding memory with a REST API | No embeddings, no service to run — native files are the store. Trade-off: semantic recall beyond keyword overlap is weaker; no multi-language client. |
| **ChatGPT / Cursor memory** | Vendor-managed, opaque, cloud-stored | Local files you can `cat`/`grep`/`git diff`/`rm`. Trade-off: Claude Code specific. |
| **Obsidian / PKM "second brain" vaults** (incl. MCP bridges to them) | Notes a human writes, links and browses; an agent can be pointed at the vault | Different reader. Hypomnema stores what the agent needs to act — state, rules, mistakes — and decides what enters its context; it has no graph, backlinks or reading UI, and does not try to be your notebook. |

Sweet spot: daily-driver Claude Code users (v2.1.59+) who want their agent to start each session knowing what it should, with memory that stays visible, editable and diffable. Not a fit for personal note-taking, shared/team memory, or users working across many AI tools.

## Install

**Requires Claude Code ≥ v2.1.59 (native file memory).**

```bash
git clone https://github.com/Fraction-Roga-i-Kopyta/hypomnema.git
cd hypomnema
make build       # compiles ./bin/memoryctl — the binary is not checked in
./install.sh     # refuses to run if bin/memoryctl is missing
~/.claude/bin/memoryctl doctor   # verify: no FAIL (an empty corpus is a WARN on day one)
```

`memoryctl` lives in `~/.claude/bin/`. To call it as plain `memoryctl`, add
`export PATH="$HOME/.claude/bin:$PATH"` to your shell profile (`install.sh`
prints this when the directory is not on your `PATH`).

`./install.sh`:
- Verifies Claude Code ≥ v2.1.59 and that `settings.json` is valid JSON — before touching anything.
- Symlinks the pre-built `memoryctl` into `~/.claude/bin/`.
- Copies the 9 hook shims into `~/.claude/hooks/v2/` and registers them in `~/.claude/settings.json` (timestamped backup first — the stable `settings.json.backup-hypomnema` name always points at this run's pre-change snapshot).
- Creates `~/.claude/memory-global/` for global facts.
- Refuses to proceed if `bin/memoryctl` predates subagent support (run `make build` first).

**Upgrading from v1.x:** run `memoryctl migrate --dry-run` first — it shows what will be kept, pruned, and routed to global vs. project store. Then `memoryctl migrate --execute` (backs up the old store, does NOT delete it). See [docs/MIGRATION.md](docs/MIGRATION.md).

**Upgrading v2.x:** `cd /path/to/hypomnema && git pull && make build && ./install.sh`

### Requirements

- Claude Code ≥ v2.1.59 (native file memory)
- Go 1.25+ and `make` (to build `memoryctl` — mandatory, not optional)
- `jq` (install.sh edits settings.json)

### Uninstalling

```bash
./uninstall.sh             # remove shims, keep memory
./uninstall.sh --dry-run   # preview
```

Surgically strips hypomnema entries from `~/.claude/settings.json`. Memory files (native + global store) are kept by default.

## Setup

### Where facts live

Routing is by store location, not by frontmatter: files in
`~/.claude/projects/<slug>/memory/` inject only in that project; files in
`~/.claude/memory-global/` inject everywhere. hypomnema resolves the project
slug the same way Claude Code resolves `autoMemoryDirectory`: sanitize the
**canonical git root** (every non-alphanumeric character → `-`), with a
linked worktree mapping to its main checkout rather than getting its own
store. See [docs/CONFIGURATION.md](docs/CONFIGURATION.md) § Store resolution
for the full anchor order and every override.
(`projects.json` is only consulted by `memoryctl migrate` when converting a
v1 store.)

### Add memory instructions to CLAUDE.md

```markdown
## Memory
- Bug found → write to native memory file (type: mistake)
- Successful strategy → write to native memory file (type: strategy)
- Before ending session → update the project's continuity file in its native store
```

### Recommended: pull-only mode

Turn off the per-prompt push and let the agent read the indexes — Claude
Code's own design, and the mode the rest of this README describes. In
`~/.claude/settings.json`:

```json
{ "env": { "HYPOMNEMA_PROMPT_INJECT": "off" } }
```

and in your user `~/.claude/CLAUDE.md`:

```markdown
@~/.claude/memory-global/MEMORY.md
```

The harness already loads each project's `MEMORY.md`; the import adds the
global store's index (memoryctl regenerates it on every Stop). SessionStart
(the core) and SubagentStart are unchanged. Without the variable,
UserPromptSubmit pushes a gated, ranked top-8 on every prompt — the default.

### Restart Claude Code

The shims activate on next session start.

## Health check

Run `memoryctl doctor` for a health snapshot (`--json` for scripts). Checks: `claude_dir_exists`, `memory_dir_exists`, `settings_hooks_registered`, `shim_files_present`, `no_broken_symlinks_*`, `memoryctl_available`, `corpus_counts`, `memory_queue` (open `/memboard` tasks older than 14 days), `wal_errors_last_7d`, `open_quanta_last_30d`, `citation_signal`, `sidecar`, `corpus_frontmatter_quality`, `oversized_facts` (facts larger than the 2.5 KB injection cap inject as a header plus a marker; split or retire them), `candidate_corroboration` and `store_resolution`. The full verb list is in `memoryctl --help`.

## Pull retrieval

```
memoryctl recall <query words...> [--k N]
```

Pull-side retrieval: rank current project + global memory against an ad-hoc query; print the best fact's body (2.5KB cap) plus an index of runner-ups with file paths (default 6 results total). Writes a `recall` WAL event for the delivered fact and unions it into the session's injected list. Includes stale facts (marked `[stale]`) — recalling one revives it.

A truncated injection ends with `…(truncated — N B total; full text: <path>)` — read that file for the rest; `memoryctl recall` serves a ranked, equally capped excerpt.

## Memory board (`/memboard`)

A Claude Code mod: a pane beside the chat that shows what memory did and lets you act on it. Install:

```
/plugin install memboard --marketplace Fraction-Roga-i-Kopyta/hypomnema
```

It needs a `memoryctl` with `stats` and `mark` (run `make build && ./install.sh` from the repo). The mod runs `$HYPOMNEMA_MEMORYCTL` when set, else `~/.claude/bin/memoryctl`, else the first `memoryctl` on `PATH`. `/memboard` toggles the pane, also mid-turn; the toggle itself injects no memory. The status line shows `mem N↓ ✓N · N%` (facts injected, cited useful, share of useful).

| Tab | Shows |
|---|---|
| `1` Session | This session and its subagents: injected, useful/silent/undelivered citations, recalls, the 5 most-injected facts |
| `2` Project | The project store: counts by type, the last 7 days, health, triage lists (noisy, candidates, stale) and the task queue |
| `3` All | Every store: the funnel (stored, injected 7d, useful 7d, not injected 30d), projects, types. The funnel counts only facts that currently have a file; injected = delivered by `inject` or `recall`; useful is a subset of injected |

Open a fact for its card. Actions (hotkeys on the card):

| Action | Effect |
|---|---|
| `a` Archive | `memoryctl retire` (asks first; `u` undoes it with `revive`) |
| `p` Pin / Unpin | `status: pinned` (never decays) or back to `active` |
| `h` Hide in *project* | `mark hide`: stops push injection of the fact in this session's project only (the button names it); the board still lists it |
| `l` Show less | `mark less`: halves the fact's rank score |
| `c` Clear marks | Drops the fact's marks and open tasks |
| `r` `s` `m` `v` | Queue a task for Claude: rewrite, split, merge, review |
| `d` Done | Closes the fact's tasks |

`recall` and `skill-inject` ignore marks. Pin, unpin and marks other than `done` are panel actions: `guard-bash` (the `PreToolUse:Bash` shim) blocks the model from running `memoryctl pin`, `unpin` or `mark` itself. That is a barrier against accidental or injected CLI calls, not a security boundary.

**Task queue.** Open tasks reach Claude as one line at SessionStart. Claude only offers them; it acts after you agree, then runs `memoryctl mark <slug> done`. A task also closes when the fact's body changes (the `status:` line is ignored, so pin/unpin keeps it open; reverting the file reopens it), and `retire` drops the fact's earlier marks. Known limitation: marks on facts in a project-level override store (`autoMemoryDirectory`) are verified only from that project's sessions.

The same data is available as JSON: `memoryctl stats --json --scope session|project|all|fact`. Directives from the shell: `memoryctl mark <slug> <hide|less|clear|rewrite|split|merge|review|done> [--project TAG]`, `memoryctl pin|unpin <slug> [--project TAG]`.

## Lifecycle

`memoryctl close` down-ranks stale facts in the sidecar (no mutation of native content). Staleness counts from the **last injection** (fallback `created`), so facts in active rotation never age out. Explicit archiving ships as `memoryctl retire <slug> [--reason ...] [--superseded-by <ref>] [--project TAG]` (`--project` addresses a fact in any store, e.g. `global`): the file moves into the store's `.archive/` with tombstone keys `retired`, `retire-reason`, `superseded-by`, `archived-from`, and `recall` renders a tombstone redirect (`[retired <date> → successor]`) instead of silently forgetting; `memoryctl revive` undoes it. Agent-written facts start as `status: candidate` and graduate on their first useful citation; `doctor` flags candidates still uncited after 5 silent sessions.

| Type | Stale after |
|------|-------------|
| mistakes | 60 days |
| strategies | 90 days |
| knowledge | 90 days |
| decisions | 90 days |
| feedback | never for project-local (SessionStart core); 45 days for global |
| notes | 30 days |
| project | 90 days |
| continuity | never for a project's freshest one; 30 days for older ones |

`project` is Claude Code's own type for ongoing work and goals, so it ages; a
project card that must never age should be `status: pinned`. `pinned` facts
never decay automatically. Retirement is never age-triggered — it is an
explicit decision: `doctor` hints at stale facts worth retiring.

To shrink a WAL grown with event types no reader uses any more, run
`memoryctl wal compact` (`--dry-run` first) while no session is mid-turn; it
keeps the original as `.wal.pre-compact-<date>` and re-projects the sidecar.

## Frontmatter schema

Native files carry minimal frontmatter (name/description/type) — that's what the harness requires. Hypomnema-specific metadata (ref_count, effectiveness, status, keywords, domains) lives in the SQLite sidecar, keyed by file slug. The sidecar is a derived projection: delete it and `memoryctl` rebuilds from WAL + native frontmatter scan.

Fields you write in the native file: Claude Code's own memory instructions nest `type` (and sometimes `created`/`status`) under a `metadata:` key, and the harness stamps `metadata.modified` on every write — hypomnema reads that shape as equivalent to the flat one below.

```yaml
---
type: mistake            # mistake | strategy | feedback | knowledge | decision | project | continuity | note
name: "short-slug"       # title of the injected block (the sidecar key and the citation target are the filename)
description: "one-liner" # shown in the MEMORY.md index
created: YYYY-MM-DD      # recency claim for ranking (wins over WAL history; absent → date of Claude Code's `modified` stamp)
status: active           # active | pinned  (stale is sidecar-managed, not hand-set)
keywords: [tag1, tag2]   # explicit relevance signal — feeds keyword overlap alongside name/description/body
domains: [domain1]       # relevance signal today; per-session domain filtering is planned

# Mistakes only (informational — the body is what injects)
severity: minor | major | critical
recurrence: 0
scope: universal | domain | narrow
root-cause: "..."
prevention: "..."

# Strategies only (informational)
success_count: 0

# Any type
precision_class: ambient  # going uncited carries no silence penalty (use for language rules, meta-policy)
---
```

`ref_count` and `effectiveness` are sidecar-managed — do not set them by hand. `memoryctl close` updates them from WAL; they are diagnostics (`/memboard`, `stats`, `doctor`), and the ranker does not read them.

## Subagents

Subagents get memory at `SubagentStart` only when their own launch task names it: `memoryctl` reads the agent's `Agent`/`Task` call out of the parent transcript and injects a fact only if its head (name, description, keywords, domains) shares at least two distinct terms with the call's `description` — the task statement, not the long prompt — one of them in its name or keywords; paths, project and home path words, agent-type words, digits and corpus-common terms do not count. Gated facts are ranked to its own top-5 (5 KB cap) before the first turn. A subagent with no call of its own gets nothing. Lookup-only agents (`fork`, `Explore`, `claude-code-guide`, `statusline-setup` by default; override with `HYPOMNEMA_SUBAGENT_SKIP`) are skipped — memory would be noise there; set `HYPOMNEMA_SUBAGENT_SKIP=*` (in `settings.json`'s `env` block, so both subagent hooks inherit it) to opt every subagent out. A subagent's citations are classified at `SubagentStop` under its own key (`<session_id>:<agent_id>`), separate from the parent session, so its usefulness signal never mixes with the parent's. There is still no auto-generated context file (`_agent_context.md` was a v1 artefact) — pass inline what the subagent needs; it receives only facts its brief names.

## Design decisions

**Why native files as the store, not a custom directory?** Claude Code's harness already injects `MEMORY.md` for the model — the file format is stable, the harness reads it for free. Owning a separate store meant a schema fork and two systems of record. Native is the substrate; sidecar is the metadata layer.

**Why a Go binary, not bash?** The hot path (inject) needs to parse hundreds of files, run ranking, and return before the hook timeout. Go eliminates the bash 3.2/awk/sed portability tax, is testable with table-driven unit tests, and is benchmarkable. The nine shims that remain in bash are a few lines each — marshalling only.

**Why a sidecar, not frontmatter?** Native frontmatter is the content contract (harness reads it). Embedding hypomnema metadata there would pollute the harness format and require careful merge logic on every `git pull`. SQLite sidecar is a derivative — the WAL is the truth, the sidecar is a fast query projection that can be rebuilt anytime.

**Why keyword overlap, not embeddings?** At hundreds of files and tens of KB, keyword matching covers the primary injection path. A replay against a random baseline suggests the ranker earns its keep without semantic similarity (with the measurement caveats noted above). No network calls, no hidden costs, no ML dependencies.

**Why a global store?** Native memory is per-project only. Rules like "always respond in Russian" or "list 2-3 root causes before the first fix" apply everywhere. `~/.claude/memory-global/` is hypomnema-owned, injected alongside project-local facts, with the same ranking pipeline.

**Why are ref_count and effectiveness not in the score?** Raw frequency is noisy (a record injected 50 times during a benchmark run isn't 50× more useful) and both signals are circular: being injected raises `ref_count` and produces `cite-silent`. Effectiveness (Bayesian `(pos+1)/(pos+0.25·neg+2)`; cite-silent counts ¼) is kept as a diagnostic; only recency of the last *useful* citation feeds the rank.

**Why not multiple pipelines?** v1 had two: a composite-score SessionStart pipeline and a substring-trigger UserPromptSubmit pipeline. They solved similar problems with different mechanics, creating maintenance overhead and edge-case conflicts. v2 merges them: one ranker, all tokens (git context + prompt) as relevance signal. The old negation-window mechanism disappears.

## FAQ

**Does it work with Cursor / Aider / other tools?**
No. Hypomnema depends on Claude Code's `additionalContext` injection channel and hook events. Other tools don't expose the same API.

**What happens to my v1.x memory?**
`memoryctl migrate` converts it: v1 subdirectories → native files, WAL seeds ref_count/effectiveness, global facts route to `~/.claude/memory-global/`, low-signal files pruned. The old store is backed up, not deleted. Run `--dry-run` first to see the plan.

**Can I run on CC older than v2.1.59?**
No — v2.x requires native memory as the substrate. v1.x stays on its tag and works as before on older CC; there are no backports.

**Can I version-control my memory?**
Yes — native memory files are plain markdown in `~/.claude/projects/<slug>/memory/` and `~/.claude/memory-global/`. `git init` there and commit `*.md`. Runtime state (the WAL and the `.sidecar.db` projection) lives outside the stores, in `~/.claude/memory/`, so there is nothing to gitignore — and the sidecar is rebuildable anyway.

**How much overhead does the hook add?**
`inject` runs well under 1s warm on a ~200-file corpus. Shims are ~10 lines of bash — they're not the bottleneck. `close` is the heavier operation (classification + sidecar reproject + index/profile regen) and runs on every Stop, off the prompt hot path.

**What happens if the sidecar is missing or corrupted?**
`memoryctl` rebuilds it from WAL + native frontmatter scan. During rebuild, the ranker runs in degraded mode (overlap over native files only; recency neutral). Injection still happens — degraded, not absent.

## Contributing

Issues are open — bug reports, reproduction recipes, and design questions welcome. Include `memoryctl doctor` output and relevant WAL lines when filing.

Pull requests are **gated by invitation** while the format contract hardens. See [CONTRIBUTING.md](CONTRIBUTING.md) for what can land without prior discussion (docs, seed additions) vs. what needs a paired issue (anything touching `internal/`, hook layer, or format contract).

Report security vulnerabilities privately via GitHub Security Advisories — see [SECURITY.md](SECURITY.md).

Release history: [CHANGELOG.md](CHANGELOG.md).

## License

MIT. See [LICENSE](LICENSE).

---

> ὑπόμνημα — ancient Greek personal notebooks for self-reflection.
> Marcus Aurelius, Seneca, and Epictetus kept them.
> Not memory itself, but the practice that sustains it.
