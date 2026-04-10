# Hypomnema

> ὑπόμνημα — ancient Greek personal notebooks for self-reflection.
> Marcus Aurelius, Seneca, and Epictetus kept them.
> Not memory itself, but the practice that sustains it.

Persistent memory system for [Claude Code](https://docs.anthropic.com/en/docs/claude-code). Zero dependencies beyond bash and jq. No databases, no embeddings, no cloud — just markdown files, YAML frontmatter, and two shell hooks.

Claude Code starts every session from zero. Hypomnema fixes that.

## The problem

Every Claude Code session is stateless. Your AI assistant doesn't remember that it made the same CSS debugging mistake five times, doesn't know the Groovy ternary it broke last week, doesn't recall the strategy that worked for NestJS routing. You repeat yourself. It repeats its mistakes.

## The solution

Hypomnema gives Claude Code a file-based long-term memory with automatic context injection. At session start, a hook reads your memory files, filters by current project, prioritizes by recurrence and severity, and injects the most relevant context. At session end, another hook reminds Claude to record what it learned.

```
You ←→ Claude Code ←→ ~/.claude/memory/
                            │
         SessionStart hook ─┤── mistakes (sorted by recurrence)
         reads & injects    ├── strategies (proven approaches)
                            ├── feedback (behavioral rules)
                            ├── knowledge (domain facts)
                            ├── project overview
                            └── continuity (where we left off)

         Stop hook ─────────┤── lifecycle rotation (stale/archive)
         checks & reminds   └── "nothing recorded" reminder
```

## What gets remembered

| Type | What | Injected at start | Limit |
|------|------|:-:|---|
| **mistakes/** | Bugs, wrong approaches, repeated errors | Yes | 3 global + 3 per project |
| **strategies/** | Proven approaches that worked | Yes | 3 |
| **feedback/** | Behavioral rules (why + how to apply) | Yes | 5 |
| **knowledge/** | Domain facts, API docs, infrastructure | Yes | 3 |
| **projects/** | Project descriptions, stack, known issues | Yes | 1 (current) |
| **continuity/** | "Where we left off" last session | Yes | 1 (current) |
| **notes/** | User profile, references, URLs | No | — |
| **journal/** | Session logs | No | — |

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
tags: [debugging, methodology]
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
tags: [css, layout, frontend]
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

1. Reads `projects.json`, matches `cwd` to a project via longest-prefix match
2. Parses all memory files using single-pass BSD-compatible awk (no subprocess spawning per file)
3. Filters by `project` (global + current) and `status` (active or pinned)
4. Applies quotas: 3 global + 3 project mistakes, 5 feedback, 3 knowledge, 3 strategies
5. Sorts mistakes by `recurrence` descending — most frequent errors surface first
6. Injects everything as markdown into Claude's system context
7. Updates `injected:` dates in frontmatter
8. Generates `_agent_context.md` — compact summary for subagents
9. Creates a session marker in `/tmp` for the Stop hook

### Stop hook

1. Runs lifecycle rotation (stale after 30 days, archive after 90)
2. Checks if any memory files were modified during the session
3. If not — reminds Claude to record findings before the session ends

## Lifecycle

Every memory file has two date fields:

- **`injected`** — updated automatically by the hook whenever the file is injected into context
- **`referenced`** — updated manually when someone actually reads or edits the file

Lifecycle decisions use `referenced`, not `injected`. A file that's been injected 100 times but never actually referenced is a candidate for archival.

```
active ──30 days without referenced──→ stale ──90 days──→ archive/
  ↑                                                         │
  └──────────── manually restore ───────────────────────────┘

pinned ──────────────────────────────→ (never rotated)
```

## Frontmatter schema

```yaml
---
type: mistake         # mistake | strategy | feedback | knowledge | project | continuity
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
tags: [css, layout]

# Strategies only
trigger: "when to apply this"
success_count: 3
tags: [css, debugging]
---
```

## Project structure

```
~/.claude/memory/
├── mistakes/           # what went wrong
├── strategies/         # what worked
├── feedback/           # behavioral rules
├── knowledge/          # domain facts
├── projects/           # project overviews
├── continuity/         # where we left off (one per project)
├── notes/              # user profile, references
├── journal/            # session logs
├── archive/            # rotated files (mirrors structure above)
├── projects.json       # cwd → project mapping
└── _agent_context.md   # auto-generated compact context for subagents
```

## Subagents

Claude Code subagents (via the Agent tool) don't receive SessionStart injection. The hook generates `_agent_context.md` — a compact summary with top mistakes and strategies. Include in subagent prompts:

```
Read ~/.claude/memory/_agent_context.md before starting.
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

**Why two date fields?** `injected` tracks when the hook last touched a file — useless for measuring actual relevance. `referenced` tracks when a human or agent actually used the information — this drives lifecycle decisions.

**Why split quotas for mistakes?** Without split quotas (3 global + 3 project), a frequently-recurring global mistake (like "jumps to first hypothesis") would push out all project-specific mistakes. Split quotas guarantee both types get representation.

**Why not embeddings?** At 40 files and ~30KB total, keyword matching by project and tags covers 100% of use cases. Embeddings add ML dependencies, network calls, and latency for zero gain at this scale.

## License

MIT
