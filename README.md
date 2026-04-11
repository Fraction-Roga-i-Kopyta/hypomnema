# Hypomnema

> ὑπόμνημα — ancient Greek personal notebooks for self-reflection.
> Marcus Aurelius, Seneca, and Epictetus kept them.
> Not memory itself, but the practice that sustains it.

Persistent memory system for [Claude Code](https://docs.anthropic.com/en/docs/claude-code). Zero dependencies beyond bash and jq. No databases, no embeddings, no cloud — just markdown files, YAML frontmatter, and shell hooks.

Claude Code starts every session from zero. Hypomnema fixes that.

81 smoke tests, 15 benchmarks, hook time ~0.3 sec.

## The problem

Every Claude Code session is stateless. Your AI assistant doesn't remember that it made the same CSS debugging mistake five times, doesn't know the Groovy ternary it broke last week, doesn't recall the strategy that worked for NestJS routing. You repeat yourself. It repeats its mistakes.

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
./install.sh
```

The installer:
- Creates `~/.claude/memory/` with subdirectories for each memory type
- Copies SessionStart and Stop hooks to `~/.claude/hooks/`
- Installs the consolidation utility to `~/.claude/bin/`
- Patches `~/.claude/settings.json` with hook entries (backs up first)
- Creates empty `projects.json` for project mapping

### Requirements

- Claude Code
- bash 3.2+ (macOS default works)
- jq
- awk (BSD or GNU)
- uv (optional — enables fuzzy dedup via rapidfuzz)

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

1. Runs lifecycle rotation with type-specific decay rates (mistakes 60/180d, feedback 45/120d, knowledge 90/365d)
2. Auto-generates continuity from git state (branch, uncommitted, last commit)
3. Rebuilds TF-IDF index if memory files changed (background, non-blocking)
4. Shows structured continuity prompt with git context if continuity is stale
5. Reminds Claude to record findings if nothing was saved

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
| notes/journal | 30 days | 90 days |
| projects | never | never |

```
active ──stale_days without referenced──→ stale ──archive_days──→ archive/
  ↑                                                                  │
  └──────────── manually restore ────────────────────────────────────┘

pinned ──────────────────────────────────→ (never rotated)
```

## Frontmatter schema

```yaml
---
type: mistake         # mistake | strategy | feedback | knowledge | decision | project | continuity
project: global       # global | your-project-id
created: 2025-01-15
injected: 2025-01-15  # auto-updated by hook
referenced: 2025-01-15 # manually updated
status: active        # active | pinned | stale | archived

# Mistakes only
severity: major       # minor | major | critical
recurrence: 3         # how many times this happened
root-cause: "..."
prevention: "..."
keywords: [css, layout]  # for content-aware matching
domains: [css, frontend] # for domain filtering

# Strategies only
trigger: "when to apply this"
success_count: 3
keywords: [css, debugging]
domains: [css, frontend]
---
```

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
└── _agent_context.md      # auto-generated compact context for subagents
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
  "scripts": ["jira", "groovy"]
}
```

Domains are also auto-detected from git diff (`.css` → css, `.sql` → db, `.groovy` → groovy) and CWD path heuristics. Records tagged with non-matching domains are filtered out — jira mistakes won't appear in CSS sessions.

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
# 81 smoke tests (injection, domain filtering, WAL, TF-IDF, outcome, error detection, dedup, decisions, precompact, cold start)
bash hooks/test-memory-hooks.sh

# 15 benchmark scenarios (precision, domain, priority, edge cases)
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

**Why not embeddings?** At 40 files and ~30KB total, keyword matching + TF-IDF covers the use cases. Embeddings add ML dependencies, network calls, and latency for marginal gain at this scale.

**Why spaced repetition, not raw frequency?** A record injected 50 times in one benchmarking session is not 50× more important. Spread (unique days) rewards consistent real usage. Decay prevents stale records from hogging slots. Negative ratio deprioritizes warnings that don't actually help.

**Why three hooks?** SessionStart handles injection (the read path). Stop handles lifecycle and continuity (the maintenance path). PostToolUse handles outcome tracking (the feedback path). Each has a clear single responsibility and different timing constraints.

## License

MIT
