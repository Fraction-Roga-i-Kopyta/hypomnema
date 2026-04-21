# Hypomnema

> ὑπόμνημα — ancient Greek personal notebooks for self-reflection.
> Marcus Aurelius, Seneca, and Epictetus kept them.
> Not memory itself, but the practice that sustains it.

Persistent memory system for [Claude Code](https://docs.anthropic.com/en/docs/claude-code). Zero dependencies beyond bash and jq. No databases, no embeddings, no cloud — just markdown files, YAML frontmatter, and shell hooks.

Claude Code starts every session from zero. Hypomnema fixes that.

200 smoke tests, 25 benchmarks, hook time ~0.3 sec.

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

         PreToolUse hook ───┤── fuzzy dedup: blocks duplicate mistakes (rapidfuzz)
                            └── exit 2 = write blocked, hint to update existing

         PostToolUse hook ──┤── outcome tracking (negative/new detection)
                            ├── error pattern detection in Bash output
                            └── feeds back into WAL scoring

         PreCompact hook ───┤── checkpoint reminder before context compression
                            └── stronger warning if nothing saved in session

         UserPromptSubmit ──┤── v0.5 context triggers: match `triggers:` frontmatter
                            └── against prompt text, inject matched records (cap 4)
```

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
- uv (optional — enables fuzzy dedup via rapidfuzz in `bin/memory-dedup.py`)

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
6. Scores records: `keyword_hits × 3 + tfidf_bonus × 2 + wal_spaced_score (0-10)`
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
3. Compares against existing mistakes using rapidfuzz trigram similarity (via `uv run`)
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
5. **Contradicts** — the newer record (by `referenced` date) gets `[ПРИОРИТЕТ]` header annotation; a `⚠ Конфликт` warning explains which is outdated
6. **Cascade signals** — when a file with `instance_of` children is updated, the children get `[REVIEW: parent updated YYYY-MM-DD]` markers for 14 days via the outcome hook writing `cascade-review` WAL events

## Self-awareness (v0.7)

The system aggregates WAL signals into a derived view of itself. No manual bookkeeping.

### `memory/self-profile.md` — auto-generated

Regenerated on every Stop hook from WAL + mistakes/ + strategies/. Four sections:

- **Meta-signals** — counts: total sessions logged, clean sessions (0 errors), `outcome-positive` (mistake injected but not repeated), `outcome-negative` (mistake repeated anyway), `strategy-used`, `strategy-gap` (clean session but no strategy injected), `trigger-useful` vs `trigger-silent`.
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
status: active         # active | pinned | stale | archived | superseded

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
├── test-memory-hooks.sh       # 200 smoke tests
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

Records are ranked by a composite score:

```
score = keyword_hits × 3 + tfidf_bonus × 2 + wal_spaced_score (0-10)
```

- **Keywords** — `keywords:` in frontmatter matched against git context (branch name, diff filenames, commit messages, CWD basename). Primary mechanism. When git signal is weak (< 3 keywords), project domains are used as fallback pseudo-keywords.
- **TF-IDF** — Offline index of body text for records without keywords. Weak signal (~3 IDF units at 30-50 docs), but catches unlabeled records.
- **WAL spaced repetition** — `spread × decay × (1 - negative_ratio)`. Rewards records used consistently across days, penalizes burst usage (benchmarks) and records with negative outcomes.

## Outcome tracking

A PostToolUse hook monitors writes to `mistakes/*.md`:

- If Claude writes/updates a mistake that was **already injected** this session → `outcome-negative` (the warning didn't prevent the repeat)
- If it's a new mistake → `outcome-new`

Negative outcomes reduce the record's WAL score over time. Records that repeatedly fail to prevent mistakes get deprioritized.

## Testing

```bash
# 144 smoke tests (injection, domain filtering, WAL, TF-IDF, outcome, error detection, dedup,
#                 decisions, precompact, cold start, cluster activation, cascade signals, prompt triggers)
bash hooks/test-memory-hooks.sh

# 25 benchmark scenarios (precision, domain, priority, edge cases, v0.3 ref_count)
bash hooks/bench-memory.sh
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
| 200 | ~1 sec | Consider index.tsv cache |
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

## License

MIT
