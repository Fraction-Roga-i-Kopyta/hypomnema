# Hypomnema

[![CI](https://github.com/Fraction-Roga-i-Kopyta/hypomnema/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/Fraction-Roga-i-Kopyta/hypomnema/actions/workflows/test.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/Fraction-Roga-i-Kopyta/hypomnema?include_prereleases&sort=semver)](https://github.com/Fraction-Roga-i-Kopyta/hypomnema/releases)

**File-based long-term memory for [Claude Code](https://docs.anthropic.com/en/docs/claude-code) — markdown, YAML, shell hooks. No cloud, no database, no embeddings.** Claude Code starts every session from zero; hypomnema fixes that.

> **Status:** v1.0 (memory format `format_version: 2`). Active daily development by a solo maintainer. Subsequent breaking changes will increment the major version and are tracked in [CHANGELOG.md](CHANGELOG.md); the v1→v2 bump is a one-time migration with backward-compat readers (existing v1 WAL keeps parsing).
>
> **Platforms:** macOS (primary, daily-driver) and Linux (`ubuntu-latest`), both covered by CI. The shell layer follows a `bash 3.2-safe` convention and depends only on coreutils + `jq` + `perl` + `sqlite3`. Windows: WSL only, native unsupported.
>
> _Not affiliated with or endorsed by Anthropic. Hypomnema is an independent project that integrates with Claude Code's public hook API._

Runtime deps: bash, jq, perl, awk, sqlite3 with FTS5. Optional Go 1.25+ for the `memoryctl` binary that replaces the slow paths. ~300 bash hook smoke tests + ~30 synthetic-fixture snapshot tests + Go unit tests across `internal/{fuzzy,profile,wal,fts,dedup,doctor,tfidf,evidence}` at ~70–90% coverage on data-critical packages, plus a CI-gated bench (`scripts/bench-gate.sh` against `docs/measurements/baselines.json`). Canonical counts live in `CHANGELOG.md § [latest]`. SessionStart hook ~0.3 s on a 100-file corpus.

## What it looks like

When you open a new Claude Code session in a project, hypomnema injects a `# Memory Context` block before the conversation starts:

```markdown
# Memory Context

**Active domains:** backend python fastapi

## Active Mistakes

### sqlalchemy-metadata-reserved-name [major, x1]
Root cause: `metadata_` mapped to column "metadata" collides with `Base.metadata`
Prevention: Check SQLAlchemy reserved attribute names before mapping

## Feedback

### code-approach
Parameterized queries only. No hardcoded secrets. No debug logging in committed code.

## Strategies

### verification-before-done
Don't claim "done" before running a minimum verification set.
```

That's it. Claude reads it as part of its context — no RAG query, no vector database, just a markdown block prepended by the hook. When Claude hits a new bug worth remembering, it writes a markdown file to `~/.claude/memory/mistakes/` and the next session injects it automatically.

## The problem

Every Claude Code session is stateless. Your AI assistant doesn't remember that it made the same debugging mistake five times, doesn't know the database migration it broke last week, doesn't recall the strategy that worked for async race conditions. You repeat yourself. It repeats its mistakes.

## The solution

Hypomnema gives Claude Code a file-based long-term memory with automatic context injection. At session start, a hook reads your memory files, filters by current project, prioritizes by recurrence and severity, and injects the most relevant context. At session end, another hook reminds Claude to record what it learned.

```
You ←→ Claude Code ←→ ~/.claude/memory/
                            │
         SessionStart hook ─┤── mistakes (sorted by recurrence × severity)
         reads & injects    ├── strategies, feedback, knowledge, decisions, notes
                            ├── project overview + continuity
                            ├── scoring: keywords + TF-IDF + WAL spaced repetition
                            └── domain fallback for cold start (weak git signal)

         Stop hook ─────────┤── lifecycle rotation (decay rates by type)
         checks & reminds   ├── auto-generate continuity from git state
                            ├── rebuild TF-IDF index
                            └── structured continuity prompt

         PreToolUse hook ───┤── fuzzy dedup: blocks duplicate mistakes (via memoryctl)
                            └── exit 2 = write blocked, hint to update existing

         PostToolUse hook ──┤── outcome tracking (negative/new detection)
                            ├── error pattern detection in Bash output
                            └── feeds back into WAL scoring

         PreCompact hook ───┤── checkpoint reminder before context compression
                            └── stronger warning if nothing saved in session

         UserPromptSubmit ──┤── v0.5 context triggers: match `triggers:` frontmatter
                            └── against prompt text, inject matched records (cap 4)
```

## How hypomnema compares

| | What it does | Hypomnema's position |
|---|---|---|
| **`CLAUDE.md` files** (Anthropic native) | Static context injected verbatim, in-repo or `~/.claude/CLAUDE.md` | Dynamic: scoring, decay, prompt-reactive triggers, WAL feedback loop. Layers *on top of* CLAUDE.md rather than replacing it. |
| **Anthropic Skills** | Named skill bundles loaded on demand | Skills are user- or Claude-invoked with known intent; hypomnema injects unprompted based on current project, prior outcomes, and substring/FTS triggers. The two are complementary, not overlapping. |
| **`mem0`** (Python, hosted or self-host) | Vector-embedding memory with a REST API and cross-vendor SDKs | No embeddings, no service to run, no DB to back up — `~/.claude/memory/*.md` is the store. Trade-off: semantic recall beyond substring/FTS is weaker, and there's no multi-language client. If you want a managed service for many AI products, mem0 is the right tool. |
| **ChatGPT / Cursor memory** | Vendor-managed, opaque, cloud-stored | Local files you can `cat`/`grep`/`git diff`/`rm`. Trade-off: zero-config only works inside Claude Code; porting the hook glue to another IDE is real work (the markdown data itself is portable). |
| **Obsidian vault + MCP** | Markdown store read through a Model Context Protocol bridge | Hypomnema is wired into 5 Claude Code lifecycle hooks (dedup on write, outcome detection on stop, compaction reminder, etc.) — not just retrieval. Obsidian wins on GUI and plugin ecosystem; hypomnema wins on integration depth. |

Sweet spot: daily-driver Claude Code users who want visible, editable, diffable memory and are comfortable reading markdown. Not a fit for teams that need a shared central memory, or for users working across many AI tools at once.

## Architecture at a glance

Five Claude Code hook events, one script each:

| Event | Script | Purpose |
|---|---|---|
| `SessionStart` | `hooks/session-start.sh` | Scan, score, inject top-N into context |
| `UserPromptSubmit` | `hooks/user-prompt-submit.sh` | Substring trigger + parallel FTS5 shadow |
| `PreToolUse:Write` | `hooks/memory-dedup.sh` | Fuzzy dedup via `memoryctl` |
| `Stop` | `hooks/session-stop.sh` | Evidence match, outcome detection, rotation |
| `PreCompact` | `hooks/memory-precompact.sh` | Warning before context compression |

Two scoring pipelines: **SessionStart** uses a composite recall-oriented score (keyword × 3 + WAL spaced-repetition + TF-IDF − noise penalty); **UserPromptSubmit** uses a deterministic precision-oriented priority key. They solve different problems and must not be conflated.

Heavy logic lives in 8 pure-function libraries under `hooks/lib/` (≈1k lines total). Hot paths — FTS5 BM25 retrieval, fuzzy dedup, single-pass WAL aggregation — are additionally implemented in Go under `internal/` and exposed via the optional `memoryctl` binary; bash fallbacks exist for everything except dedup, with byte-for-byte parity enforced by `scripts/parity-check.sh` (8 fixtures, including hostile-input cases).

`memoryctl` ships drop-in replacements for hot bash paths (`fts shadow`, `dedup check`, `tfidf rebuild`, `self-profile`, `doctor`, `decisions review`, `evidence learn`, `scoring-probe`), plus standalone inspection subcommands:

- **`memoryctl wal validate`** — scans `.wal` for grammar violations against the four-column invariant; CI-friendly exit codes.
- **`memoryctl audit invariants`** — runs every automated invariant checker from `internal/invariants/` against the repo (currently I3 atomic writes and I5 hook fail-safe; soft invariants remain human-judgment items).

Full architectural map with end-to-end data flow, scoring formulas, library purposes, WAL event taxonomy: **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)**. Per-event normative registry (producer/consumer matrix, first-seen version, soft-close membership): **[docs/EVENTS.md](docs/EVENTS.md)**. Project-level invariants the codebase upholds (4-column WAL grammar, slug sanitisation symmetry, atomic writes, fail-safe hooks, …): **[docs/INVARIANTS.md](docs/INVARIANTS.md)**.

## What gets remembered

| Type | What | Injected at start | Limit |
|------|------|:-:|---|
| **mistakes/** | Bugs, wrong approaches, repeated errors | Yes | 3 global + 3 per project |
| **strategies/** | Proven approaches that worked | Yes | cap 4 (adaptive) |
| **feedback/** | Behavioral rules (why + how to apply) | Yes | cap 6 (adaptive) |
| **knowledge/** | Domain facts, API docs, infrastructure | Yes | cap 4 (adaptive) |
| **projects/** | Project descriptions, stack, known issues | Yes | 1 (current) |
| **continuity/** | "Where we left off" last session | Yes | 1 (current) |
| **decisions/** | Architecture & technology choices | Yes | cap 3 (adaptive) |
| **notes/** | Long-form knowledge, references, URLs | Yes | cap 2 (adaptive) |
| **journal/** | Session logs | No | — |

Feedback, knowledge, strategies, decisions, and notes share an adaptive budget of 12 slots total with per-type caps. If one type has fewer relevant files, the budget flows to others.

### Example: a mistake

```yaml
---
type: mistake
project: global
created: 2025-06-15
injected: 2025-07-01
referenced: 2025-06-28
status: pinned
severity: major
recurrence: 5
scope: universal
root-cause: "Jumps to first hypothesis without listing alternatives"
prevention: "List 2-3 possible root causes before attempting the first fix"
keywords: [debugging, hypothesis, root, cause, diagnosis, fix, cascade]
domains: [general]
---
```

This gets injected into every session. After the fifth time, Claude stops doing it.

### Example: a strategy

```yaml
---
type: strategy
project: global
created: 2025-06-20
injected: 2025-07-01
referenced: 2025-06-30
status: active
trigger: "CSS layout bug"
success_count: 3
keywords: [css, layout, viewport, cascade, parent, container, debugging]
domains: [css, frontend]
---

1. Trace constraint chain from viewport down to target element
2. Find the OUTER constraining container
3. Fix parent constraints, not child styles
4. Check CSS variables via computed styles
```

## Install

```bash
git clone https://github.com/Fraction-Roga-i-Kopyta/hypomnema.git
cd hypomnema
./install.sh                                # base install
./install.sh --discover --patch-claude-md   # interactive setup
```

See `docs/QUICKSTART.md` for the 5-minute walkthrough, `docs/TROUBLESHOOTING.md` if anything goes wrong, `docs/FAQ.md` for cross-machine sync and upgrades, and `CLAUDE.md` (in this repo) for the agent protocol.

### Upgrading

```bash
cd /path/to/hypomnema && git pull && ./install.sh --skip-base
```

Symlinks rebuild from the enumerated `hooks/` and `bin/` listings, so new files added upstream get picked up automatically. `--skip-base` preserves your `~/.claude/memory/` contents and only refreshes the symlinks and `settings.json` entries.

### Uninstalling

```bash
./uninstall.sh             # remove symlinks, keep memory
./uninstall.sh --dry-run   # preview
```

Surgically strips hypomnema entries from `~/.claude/settings.json` — foreign hooks you added yourself are preserved. Memory directory is kept by default; pass `--purge-memory --yes` to wipe it too.

The installer (v0.8+):
- Pre-flight checks: `jq`, `perl`, `awk`, `bash 3.2+`, `~/.claude` (fails fast with clear errors)
- Creates `~/.claude/memory/` with subdirectories for each memory type, seeds, `.config.sh` (runtime overrides)
- Symlinks hooks and lib/ into `~/.claude/hooks/`
- Symlinks utilities into `~/.claude/bin/`
- Patches `~/.claude/settings.json` with hook entries (backs up first)
- `--discover` interactively scans `~/Development`/`~/code`/`~/projects`/`~/src` for git repos and adds them to `projects.json`
- `--patch-claude-md` appends a four-line memory section to `~/.claude/CLAUDE.md` (idempotent)
- `--dry-run` previews without writing

### Requirements

- Claude Code
- bash 3.2+ (macOS default works)
- jq
- perl 5 (preinstalled on macOS/Linux)
- awk (BSD or GNU)
- Go 1.22+ (optional — build `memoryctl` via `make build` to enable fuzzy dedup + the FTS5 shadow pass)

## Setup

### 1. Map your projects

Tell Hypomnema which directories belong to which projects. Edit `~/.claude/memory/projects.json`:

```json
{
  "/Users/you/dev/webapp": "webapp",
  "/Users/you/dev/api": "api",
  "/Users/you/work/scripts": "scripts"
}
```

When you `cd` into a mapped directory, only mistakes and knowledge relevant to that project get injected. Global memories (with `project: global`) are always included.

### 2. Add memory instructions to CLAUDE.md

Add to your `~/.claude/CLAUDE.md` so Claude knows to write memories:

```markdown
## Memory
- Bug found → write to ~/.claude/memory/mistakes/
- Successful strategy → write to ~/.claude/memory/strategies/
- Before ending session → update continuity/{project}.md
- When reading/editing a memory file → update referenced: YYYY-MM-DD
```

### 3. Restart Claude Code

The hooks activate on next session start.

## How injection works

### SessionStart hook (~0.3 sec)

1. Reads `projects.json`, matches `cwd` to a project via longest-prefix match (with boundary check — `/fly` won't match `/fly-other`)
2. Detects active domains via 4-level cascade: `projects-domains.json` → CWD path heuristics → git diff extensions → signature files
3. Extracts keywords from git context: branch name, diff filenames, commit messages, CWD basename. Falls back to project domains as pseudo-keywords when git signal is weak (< 3 keywords)
4. Parses all memory files using single-pass BSD-compatible awk (no subprocess spawning per file)
5. Filters by `project` (global + current), `status` (active/pinned), `domains` (intersection with active), and `recurrence` threshold
6. Scores records with a five-component composite (keyword + project + WAL spaced-repetition + strategy + TF-IDF, with a noise penalty). Cold-start gates keep Bayesian and TF-IDF dormant until the corpus has accumulated signal. Canonical formula: `CLAUDE.md § How injection ranks files`.
7. Applies adaptive quotas: 3+3 mistakes, shared budget of 12 for feedback/knowledge/strategies/decisions/notes
8. Injects everything as markdown into Claude's system context
9. Updates `injected:` and `referenced:` dates in frontmatter (scoped to frontmatter block only)
10. Logs injections to WAL (Write-Ahead Log) for spaced repetition scoring
11. Generates `_agent_context.md` — compact summary with body excerpts for subagents

### Stop hook

1. Runs lifecycle rotation with type-specific decay rates (mistakes 60/180d, feedback 45/120d, knowledge 90/365d). v0.7+ logs `rotation-stale`, `rotation-archive`, `rotation-summary` to WAL — no longer silent.
2. Auto-generates continuity from git state (branch, uncommitted, last commit)
3. Rebuilds TF-IDF index if memory files changed (background, non-blocking)
4. Shows structured continuity prompt with git context if continuity is stale
5. Reminds Claude to record findings if nothing was saved
6. v0.7 — runs `memory-strategy-score.sh` (updates `success_count` in strategies from WAL `strategy-used` events) and `memory-self-profile.sh` (regenerates `self-profile.md`)

### PreToolUse hook (fuzzy dedup)

1. Triggers on Write to `mistakes/*.md` (new files only, skips overwrites)
2. Extracts `root-cause` from content before file is created
3. Compares against existing mistakes using `memoryctl dedup check` (rapidfuzz-compatible token_set_ratio, implemented in Go)
4. Similarity ≥ 80% → **blocks write** (exit 2) with hint: "update existing file instead"
5. Similarity ≥ 50% → warns: "possible duplicate"
6. Logs dedup events to WAL

### PostToolUse hooks

**Outcome tracking** (Write/Edit → `mistakes/*.md`):
1. Checks WAL: was this mistake injected in current session?
2. If yes → logs `outcome-negative` (warning didn't help)
3. If no → logs `outcome-new` (fresh mistake)
4. Feeds back into spaced repetition scoring — repeatedly-failed warnings get deprioritized

**Error pattern detection** (Bash, exit 0 only):
1. Extracts `stdout` + `stderr` from tool response
2. Matches against patterns in `.error-patterns` config (pipe-delimited: `pattern|category|hint`)
3. Session dedup via WAL — one hint per category per session
4. Cross-references with existing mistakes
5. Outputs hint to Claude's context: "Pattern detected: {hint}"
6. Only fires on successful commands (exit 0) — failed commands are already visible in context

### PreCompact hook

1. Fires before context window compression
2. Checks if any memory files were created/modified during this session (uses session marker as timestamp reference)
3. Outputs reminder: "Save unsaved insights now"
4. Stronger warning if nothing was saved this session

### UserPromptSubmit hook (v0.5)

1. Fires on every user prompt
2. Scans memory files for `triggers:` (array) / `trigger:` (single) frontmatter fields
3. Matches phrases against prompt text as case-insensitive substring (Latin + Cyrillic)
4. Sorts candidates: `pinned` > `active`; tiebreak by severity, recurrence, ref_count
5. Dedups against SessionStart-injected slugs via `/tmp/.claude-injected-${SESSION_ID}.list`
6. Caps at `MAX_TRIGGERED=4` to prevent noisy injections on broad prompts
7. Emits matched records under `## Triggered (from prompt)` section
8. For mistakes with empty body — synthesizes from `root-cause` + `prevention` frontmatter
9. Logs `trigger-match|<slug>|<session_id>` to WAL; counts as positive signal in future scoring

## Cluster activation (v0.4)

Memory files declare typed relationships in `related:` frontmatter:

```yaml
related:
  - debugging-approach: reinforces       # two rules point at the same thing
  - css-layout-debugging: instance_of    # concrete mistake → general strategy
  - old-api-rule: supersedes             # new rule replaces old
  - legacy-approach: contradicts         # explicit conflict (priority goes to newer)
```

During SessionStart, after the main scoring pass:

1. **Forward scan** — each injected file's `related:` targets are pulled in if they score above zero
2. **Reverse scan** — non-injected files that reference an injected slug are also candidates
3. **Cluster cap +4** — up to four additional files above `MAX_FILES`
4. **Provenance markers** — cluster-loaded records show `(via source → type)` in output
5. **Contradicts** — the newer record (by `referenced` date) gets `[PRIORITY]` header annotation; a `⚠ Conflict` warning explains which is outdated
6. **Cascade signals** — when a file with `instance_of` children is updated, the children get `[REVIEW: parent updated YYYY-MM-DD]` markers for 14 days via the outcome hook writing `cascade-review` WAL events

## Self-awareness (v0.7)

The system aggregates WAL signals into a derived view of itself. No manual bookkeeping.

### `memory/self-profile.md` — auto-generated

Regenerated on every Stop hook from WAL + mistakes/ + strategies/. Five sections:

- **Meta-signals** — counts: total sessions logged, clean sessions (0 errors), `outcome-positive` (mistake injected but not repeated), `outcome-negative` (mistake repeated anyway), `strategy-used`, `strategy-gap` (clean session but no strategy injected), `trigger-useful` vs `trigger-silent`.
- **Intuition signal** (v1.1) — windowed `silent_applied_30d / trigger_useful_30d` ratio plus a one-line interpretation. The crossing point `> 1.0` sustained over 30 days is the operational definition of the v1.0 «Интуиция» tier (rules applied silently more often than they are cited explicitly). See `docs/decisions/intuition-milestone.md`.
- **Strengths** — top strategies ranked by `success_count` (synced from WAL `strategy-used` by `bin/memory-strategy-score.sh`).
- **Weaknesses** — top mistakes by `recurrence`. Entries with `scope: universal` get a 🔴 marker — these are systemic tendencies, not project-specific quirks.
- **Calibration** — domains sorted by average error rate per session, derived from `session-metrics` WAL events.

Never edit manually — it's a pure function of WAL. Delete it and it regenerates.

### `scope` field for mistakes

Defaults to `domain` if missing. Changes SessionStart behavior:

- `universal` — always injected (systemic patterns like "jumps to first hypothesis")
- `domain` — injected when active domains intersect with file domains (current pre-v0.7 behavior)
- `narrow` — only injected via explicit keyword-match; **not** included by domain alone

Narrow scope solves the signal-to-noise problem: library-specific gotchas (`bcrypt-shell-escape`, `html2canvas-oklch`) are useful *when relevant* but were cluttering SessionStart for every backend/frontend session. Now they wait for a keyword hit.

`pinned` status bypasses scope — always injected.

## FTS5 shadow retrieval (v0.8)

Parallel to the substring-trigger pipeline, a fire-and-forget background pass runs FTS5/BM25 search over memory body content for every user prompt. Files the shadow pass would surface but the primary pipeline did NOT inject become `shadow-miss` WAL events — a recall signal for trigger tuning, closing a gap left open by v0.8 (which measures precision but cannot measure recall without ground truth).

Stack (all bash + SQLite FTS5, no embeddings, no services, no network):

- **`bin/memory-fts-sync.sh`** — lazy reconciler of `~/.claude/memory/index.db`. Drift check via `SUM(mtime) + COUNT(*)` (microseconds when nothing changed), diff reconciliation via temp table otherwise. Safe to call before every query.
- **`bin/memory-fts-query.sh`** — BM25 lookup over FTS5 `tokenize='trigram'`. Handles Cyrillic case-folding, arbitrary punctuation, and FTS5 reserved keywords (`OR`/`AND`/`NOT`/`NEAR`) without crashing the parser.
- **`bin/memory-fts-shadow.sh`** — diffs FTS5 top-K against the primary trigger-match list plus the SessionStart dedup file. Scans only the dirs the trigger pipeline uses (`mistakes|feedback|strategies|knowledge|notes|seeds`); auto-generated files like `self-profile.md` or `_agent_context.md` are excluded. Caps at `MEMORY_FTS_SHADOW_MAX=4` events per prompt.
- **New WAL event** `shadow-miss|<slug>|<session_id>` — deduped per key within the session by `wal_append`.
- **Hook integration** — `( memory-fts-shadow.sh ... ) >/dev/null 2>&1 & disown` appended at the end of `hooks/user-prompt-submit.sh`. Detached subshell — zero impact on hook latency or output.

Read the signal:

```bash
awk -F'|' '$2=="shadow-miss" {print $3}' ~/.claude/memory/.wal \
  | sort | uniq -c | sort -rn | head -10
```

Slugs that shadow repeatedly surfaces but the primary pipeline never triggers are candidates for `triggers:` expansion.

**Trigram tokenization tradeoffs** — substring match is asymmetric: query-token must be ⊆ corpus-token (`деплой` finds docs with `деплоили`, but `деплоили` does not find docs with just `деплой`). For typical recall use (prompts shorter/more generic than memory bodies) this works in practice and avoids stemming dependencies.

Latency: cold full build ~370ms for 90 files; steady-state no-op sync ~18ms; typical query ~25ms. Entire shadow pass runs after the hook returns.

## Lifecycle

Every memory file has two date fields:

- **`injected`** — updated automatically by the hook whenever the file is injected into context
- **`referenced`** — updated automatically on injection AND manually when someone reads or edits

Lifecycle decisions use `referenced`. Both fields are updated within the frontmatter block only — body content is never modified by hooks.

| Type | Stale after | Archive after |
|------|-------------|---------------|
| mistakes | 60 days | 180 days |
| strategies | 90 days | 180 days |
| knowledge | 90 days | 365 days |
| decisions | 90 days | 365 days |
| feedback | 45 days | 120 days |
| notes | 30 days | 90 days |
| journal | 30 days | 90 days |
| projects | never | never |
| continuity | never (auto-generated per session) | never |

```
active ──stale_days without referenced──→ stale ──archive_days──→ archive/
  ↑                                                                  │
  └──────────── manually restore ────────────────────────────────────┘

pinned ──────────────────────────────────→ (never rotated)
```

## Frontmatter schema

```yaml
---
# Core (all types)
type: mistake          # mistake | strategy | feedback | knowledge | decision | project | continuity
project: global        # global | your-project-id
created: 2025-01-15    # used by recency-boost ranking (≤7/30/90 days → rank 3/2/1)
injected: 2025-01-15   # auto-updated by SessionStart on each injection
referenced: 2025-01-15 # auto-updated when file is accessed
status: active         # active | pinned | stale | archived

# Ranking signals (optional, used by UserPromptSubmit trigger-match priority key)
ref_count: 108         # auto-incremented per injection (log10-bucketed in priority key)
decay_rate: slow       # slow | normal | fast — overrides type-default stale/archive thresholds
description: "..."     # optional one-liner; auto-picked by regen-memory-index.sh for MEMORY.md

# Mistakes only
severity: major        # minor | major | critical (critical=pinned priority)
recurrence: 3          # how many times this happened
scope: domain          # v0.7 — universal | domain | narrow
                       #   universal = always injected at SessionStart
                       #   domain    = injected when domain matches (default)
                       #   narrow    = only on explicit keyword-match, never by domain alone
root-cause: "..."      # also used as fallback description in MEMORY.md index
prevention: "..."
keywords: [css, layout]  # for TF-IDF content matching
domains: [css, frontend] # for domain-gated injection

# Strategies only
trigger: "when to apply this"   # legacy single-phrase trigger (substring match)
success_count: 3
keywords: [css, debugging]
domains: [css, frontend]

# v0.4 — typed links between memory files
related:
  - wrong-root-cause-diagnosis: instance_of   # reinforces | contradicts | instance_of | supersedes

# v0.5 — prompt triggers (case-insensitive substring match; quoted/code-block/blockquote stripped)
triggers:
  - "css layout"
  - "flex gap"

# v0.7.1 — evidence of application (used by session-stop feedback detector)
evidence:
  - "parameterized query"
  - "timezone-aware datetime"

# v0.8.1 — precision-class (optional; excludes file from measurable precision denominator)
precision_class: ambient
---
```

**Trigger matching rules (v0.6):**
- fenced ` ``` ` blocks, inline backticks, and `> blockquote` lines are stripped before matching
- negation window: match skipped if ±40 chars around it contain не/без/уже/нет/already/fixed/skip/no/don't
- priority key: `project_rank → status → severity → recency → log₁₀(ref_count) → recurrence → ref_count`
- malformed frontmatter (no closing `---`) produces `schema-error` WAL event, file skipped

**Feedback detector (v0.7.1):**
- session-stop writes `trigger-useful` if memory content was applied in the assistant's responses, else `trigger-silent`
- if `evidence:` is present: match is case-insensitive substring over those phrases, threshold ≥1
- if `evidence:` is absent: fallback to body-mining — extract content tokens from the body (length ≥4, ru+en stop-list filtered, slug filtered, excludes `**Why:**` / `**How to apply:**` / fenced code blocks / blockquotes), threshold ≥2 unique tokens
- body-mining is UTF-8 aware (v0.8: perl `\w{4,}` with `/u` flag) — works for Cyrillic, CJK, Greek, etc., no need for explicit `evidence:` on non-ASCII memories
- new WAL events: `evidence-missing` (injected memory file not found at session-stop), `evidence-empty` (body mining yielded zero tokens — candidate to add `evidence:` or enrich body)

**Precision metric (v0.8.1):** self-profile classifies trigger events into three buckets — `trigger-useful` (rule cited in text), `silent-applied` (silent in same session as `outcome-positive` for the same file — applied without citation), and `silent-noise` (silent with no application signal; the concrete list for trigger tuning). Files with `precision_class: ambient` in frontmatter are reported separately and excluded from the denominator.

## Project structure

```
~/.claude/memory/
├── mistakes/              # what went wrong
├── strategies/            # what worked
├── feedback/              # behavioral rules
├── knowledge/             # domain facts
├── decisions/             # architecture & technology choices
├── projects/              # project overviews
├── continuity/            # where we left off (one per project)
├── notes/                 # long-form knowledge, references
├── journal/               # session logs
├── archive/               # rotated files (mirrors structure above)
├── projects.json          # cwd → project mapping
├── projects-domains.json  # project → default domains mapping
├── .error-patterns        # error detection patterns (pattern|category|hint)
├── .stopwords             # TF-IDF tokenization stop words
├── .tfidf-index           # auto-generated TF-IDF index
├── .wal                   # Write-Ahead Log (injection + outcome + error-detect)
├── .wal.lockd/            # mkdir-based lock for WAL compaction (v0.6+)
├── .runtime/              # per-session dedup lists (v0.6+, replaces /tmp)
├── self-profile.md        # v0.7 — auto-generated strengths/weaknesses/calibration view
├── .config.sh             # v0.8 — runtime overrides for caps/limits (copied from templates/)
├── index.db               # v0.8 — SQLite FTS5 shadow-retrieval index (rebuildable, git-ignored)
└── _agent_context.md      # auto-generated compact context for subagents
```

Hook structure (in repo):

```
hooks/
├── session-start.sh           # SessionStart — orchestration (v0.8: 1289 → 722 lines)
├── session-stop.sh            # Stop — lifecycle rotation, feedback loop, continuity
├── user-prompt-submit.sh      # UserPromptSubmit — trigger-based injection
├── memory-{outcome,dedup,error-detect,index,precompact,analytics}.sh
├── wal-compact.sh
├── regen-memory-index.sh
├── bench-memory.sh            # perf benchmarks
├── test-memory-hooks.sh       # 290+ smoke tests (see CHANGELOG for exact count)
└── lib/                       # v0.8 — shared sourced helpers
    ├── wal-lock.sh            # mkdir-based WAL locking
    ├── stat-helpers.sh        # portable _stat_mtime/_stat_size
    ├── detect-project.sh      # shared project detection (CWD → projects.json)
    ├── parse-memory.sh        # AWK_MISTAKES, AWK_SCORED, parse_related, find_memory_file, domain_matches
    ├── score-records.sh       # compute_wal_scores, compute_tfidf_scores, load_noise_candidates
    ├── build-context.sh       # expand_clusters, apply_priority_markers, assemble_context, apply_cascade_markers
    └── evidence-extract.sh    # feedback detector token extraction (UTF-8 via perl)
```

## Subagents

Claude Code subagents (via the Agent tool) don't receive SessionStart injection. The hook generates `_agent_context.md` — a compact summary with top mistakes and strategies. Include in subagent prompts:

```
Read ~/.claude/memory/_agent_context.md before starting.
```

## Domain filtering

Each project can have default domains in `projects-domains.json`:

```json
{
  "webapp": ["frontend", "css", "backend"],
  "api": ["python", "sql", "backend"]
}
```

Domains are also auto-detected from git diff (`.css` → css, `.sql` → db, `.py` → python) and CWD path heuristics. Records tagged with non-matching domains are filtered out — database mistakes won't appear in frontend sessions.

Memory files use `domains: [css, frontend]` in frontmatter. Records with `domains: [general]` or no domains always match.

## Scoring

Records are ranked by a composite five-component score. The canonical
formula lives in `CLAUDE.md § How injection ranks files` — duplicating
it here invited drift, so this section summarises the components and
links out for the weights.

- **Keyword hits** — primary mechanism. `keywords:` in frontmatter
  matched against git context (branch, diff filenames, commit messages,
  CWD basename). When git signal is weak (< 3 keywords), project
  domains are used as fallback pseudo-keywords.
- **Project boost** — `file.project == current project` adds a fixed
  bonus so project-local records outrank `global` records on ties.
- **WAL spaced repetition** — Bayesian effectiveness × recency decay ×
  spread-of-days. Rewards records used consistently, penalises burst
  usage and records with negative outcomes. Gated behind a minimum
  outcome-sample threshold on fresh corpora (cold-start ADR).
- **Strategy bonus** — `min(success_count × 2, 6)` so battle-tested
  strategies surface ahead of rarely-used ones.
- **TF-IDF body match** — offline index of body text across the full
  corpus. Gated until the index holds ≥ 100 unique terms.
- **Noise penalty** — records that match no active signal at all take
  a fixed `−3`, so clearly-irrelevant files don't occupy quota.

## Outcome tracking

A PostToolUse hook monitors writes to `mistakes/*.md`:

- If Claude writes/updates a mistake that was **already injected** this session → `outcome-negative` (the warning didn't prevent the repeat)
- If it's a new mistake → `outcome-new`

Negative outcomes reduce the record's WAL score over time. Records that repeatedly fail to prevent mistakes get deprioritized.

## Testing

```bash
# 290+ smoke tests (injection, domain filtering, WAL, TF-IDF, outcome, error detection, dedup,
#                  decisions, precompact, cold start, cluster activation, cascade signals, prompt triggers)
bash hooks/test-memory-hooks.sh

# 25 benchmark scenarios (precision, domain, priority, edge cases, v0.3 ref_count)
bash hooks/bench-memory.sh

# Measure retrieval on the synthetic corpus (or your real memory dir)
make replay                                   # seeds-only baseline
scripts/replay-runner.sh --memory ~/.claude/memory   # real dogfood
```

## Consolidation

Over time, similar mistakes accumulate. Run the consolidation utility to find candidates for merging:

```bash
~/.claude/bin/memory-consolidate.sh
```

This finds:
- Mistakes with identical `prevention` fields (duplicates)
- Mistakes with `recurrence: 0` older than 60 days (stale candidates)

Merging is manual — ask Claude to consolidate them.

## Performance

| Files | Hook time | Notes |
|-------|-----------|-------|
| 40 | ~0.3 sec | Current tested scale |
| 100 | ~0.5 sec | Estimated, within 5 sec timeout |
| 200 | ~1 sec | Consider `memoryctl` Go binary for hot paths |
| 500+ | — | Consider compiled CLI replacement |

The format (YAML frontmatter + markdown) is a stable contract. Any future engine (Python, Go, Rust) reads the same files. Migration = replacing the hook script, not the data.

## Design decisions

**Why files, not a database?** Transparency. You can `cat`, `grep`, `git diff` your memories. No migration scripts, no schema versions. The filesystem is the API.

**Why bash, not Python?** Zero cold start. Claude Code hooks have a 5-second timeout. Python adds ~300ms of startup. Bash + awk runs in ~300ms total on 40 files.

**Why two date fields?** `injected` is set once on file creation (write-once) — tracks when the memory was first recorded. `referenced` tracks when a human or agent actually used the information — this drives lifecycle decisions.

**Why split quotas for mistakes?** Without split quotas (3 global + 3 project), a frequently-recurring global mistake (like "jumps to first hypothesis") would push out all project-specific mistakes. Split quotas guarantee both types get representation.

**Why not embeddings?** At 40 files and ~30KB total, keyword matching + TF-IDF covers the primary injection path. v0.8 added SQLite FTS5 with `tokenize='trigram'` as a parallel shadow channel for recall measurement — still no embeddings, no ML dependencies, no network, just the `sqlite3` that ships with macOS. Cyrillic morphology and arbitrary punctuation work out of the box.

**Why spaced repetition, not raw frequency?** A record injected 50 times in one benchmarking session is not 50× more important. Spread (unique days) rewards consistent real usage. Decay prevents stale records from hogging slots. Negative ratio deprioritizes warnings that don't actually help.

**Why separate hooks?** SessionStart handles baseline injection (the cold read path). UserPromptSubmit handles prompt-reactive injection (v0.5 — explicit task context triggers). Stop handles lifecycle and continuity (maintenance). PostToolUse handles outcome tracking and cascade detection (feedback). Each has a clear single responsibility and different timing constraints.

**Why cluster activation?** Related memories don't just sit in separate files — a mistake is often an instance of a general rule, a strategy reinforces a feedback note. At injection time, the cluster pass pulls in the graph neighborhood of each primary record, up to +4 files, with provenance markers showing *why* each was included. This surfaces context that keyword-only scoring would miss.

**Why prompt triggers?** CWD/git scoring catches the general shape of a task (*"frontend files changed, css domain"*). But the user's first sentence often names the exact symptom (*"tailwind hsl"*, *"410 gone"*). Triggers close that gap — a file declares its own activation phrase, and the UserPromptSubmit hook matches it directly against what the user just typed.

## FAQ

**Does it work with Cursor / Aider / Windsurf / any other Claude Code alternative?**
No. Hypomnema hooks into the Claude Code hook events (`SessionStart`, `UserPromptSubmit`, `PreToolUse`, `Stop`, `PreCompact`). Other tools don't expose the same hooks, and the memory layer is designed around the exact envelope shapes Claude Code emits (see `docs/hooks-contract.md`). Port is possible but not trivial.

**How is this different from mem0 / letta / MemGPT?**
They target LLM applications generally and assume a runtime with embeddings, vector stores, and LLM-driven summarisation. Hypomnema is Claude-Code-specific, offline by design, with deterministic retrieval (substring triggers + keyword scoring + FTS5 BM25 shadow). No network calls, no hidden costs, no embeddings. If you want adaptive semantic memory across heterogeneous LLM apps, those tools are correct. If you want a Claude Code session to not forget your project's BSD/GNU sed gotcha for the fifth time, hypomnema.

**Isn't `.config.sh` a prompt-injection vector?**
Yes — that's why `hooks/lib/load-config.sh` is a *safe parser*, not `source`. The file is writable by any process with memory-dir access, including Claude via its Write tool. If hooks `source`d it, a malicious or mistaken Claude could write `rm -rf $HOME` there and have it execute at the next session start. The safe parser only honours integer assignments on a whitelist and rejects everything else. Memory content files are never executed.

**How much overhead does the hook add to each session?**
~0.3 sec on a 40-file memory tree at SessionStart, measured on M-series Apple Silicon. UserPromptSubmit is sub-50ms (substring-match only, FTS5 shadow runs detached). Stop is ~200ms (evidence match + rotation). PreToolUse dedup is a single `memoryctl` subprocess (~20ms). The 5-second hook timeout is never approached. Full benchmark numbers in `docs/measurements/` and `hooks/bench-memory.sh perf`.

**Does it degrade Claude's answers?**
The injected context makes Claude aware of mistakes it has already been told about. Whether that *improves* or *degrades* answers depends on whether your memory is curated — a noisy memory full of irrelevant rules dilutes the context. The `self-profile.md` report tracks precision per rule (`trigger-useful` vs `trigger-silent`) so you can identify and remove noise. On a mature maintainer-dogfood corpus `measurable_precision` runs ~70%; fresh installs sit near 0 until outcome signal accumulates (cold-start ADR, `docs/decisions/cold-start-scoring.md`).

**What happens if Claude writes memory files that aren't valid?**
The hook tolerates malformed frontmatter silently — the file just doesn't participate in scoring or cluster expansion. Pre-write fuzzy dedup (PreToolUse) blocks duplicates at 80%+ similarity to an existing mistake. Post-write dedup merges 80%+ duplicates and bumps the existing `recurrence` counter. Files older than their per-type stale threshold flip to `status: stale`; older still, they move to `archive/`. Pinned files are exempt from rotation.

**Does it work across multiple projects?**
Yes. `~/.claude/memory/projects.json` maps each CWD to a project slug; `SessionStart` reads the current directory and filters memories by project. `project: global` records inject everywhere; project-specific records only inject in their own tree. Domain detection (from `projects-domains.json`) further narrows the domain filter.

**Can I version-control my memory?**
Yes — `~/.claude/memory/` is plain markdown + YAML. `git init` inside it, commit the `*.md` files, ignore the runtime artefacts (the shipped `.gitignore` template lists them). Nothing in hypomnema requires the memory dir to be non-tracked.

**Is this going to get `systemd`-level complicated?**
Design constraints in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) are explicit: no network, no embeddings, no external dependencies beyond the shell baseline, bash/Go parity contract, append-only WAL, agent-writes/hooks-don't-write invariant. If a change violates one of those, it gets pushed back or moved to a fork.

## Contributing

Issues are open — bug reports, reproduction recipes, and design questions are all welcome. Please include `memoryctl doctor` output and relevant `~/.claude/memory/.wal` lines when filing.

Pull requests are **gated by invitation** while the format contract is still hardening through external use. Read [CONTRIBUTING.md](CONTRIBUTING.md) for the current posture and the kinds of change that can land without prior discussion (docs typos, seed additions) vs. those that need a paired issue first (anything touching `internal/`, the hook layer, or the format contract).

Report security vulnerabilities privately via GitHub Security Advisories — see [SECURITY.md](SECURITY.md).

Release history and breaking-change log: [CHANGELOG.md](CHANGELOG.md).

## License

MIT. See [LICENSE](LICENSE).

---

> ὑπόμνημα — ancient Greek personal notebooks for self-reflection.
> Marcus Aurelius, Seneca, and Epictetus kept them.
> Not memory itself, but the practice that sustains it.
