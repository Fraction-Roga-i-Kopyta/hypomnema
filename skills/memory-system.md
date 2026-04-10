---
name: memory-system
description: Persistent memory system for Claude Code — mistakes, strategies, feedback, knowledge, lifecycle
---

# Hypomnema — Memory System

You have a persistent file-based memory system at `~/.claude/memory/`. SessionStart hook automatically injects relevant context. Your job is to write to it when something worth remembering happens.

## Memory Types

### mistakes/ — What went wrong
Record when a bug, wrong approach, or repeated error is found.

Frontmatter: `type: mistake`, `severity: minor|major|critical`, `recurrence: N`, `root-cause: "..."`, `prevention: "..."`, `domains: [general]`, `keywords: [...]`

Write immediately when a mistake is found. Increment `recurrence` if it happens again.

### strategies/ — What worked
Record when a successful approach is found that's worth repeating.

Frontmatter: `type: strategy`, `trigger: "when to use"`, `success_count: N`, `domains: [general]`, `keywords: [...]`

Body: numbered steps of the approach.

### feedback/ — How to behave
User preferences about communication, code style, workflow. Structured as:

```
Rule statement.

**Why:** Context behind the rule.
**How to apply:** When and where this kicks in.
```

### knowledge/ — Domain facts
Facts about infrastructure, APIs, tools that don't change often.

### projects/ — Project overviews
Brief description of each project: stack, key components, known issues.

### continuity/ — Where we left off
One file per project (`continuity/{project}.md`). Overwritten each session. Three lines:
- Task: what was being worked on
- Status: done / in-progress / blocked
- Next: what to do next

### notes/ — Long-form knowledge and references
Notes are injected by keyword/domain matching (cap: 2).

## Lifecycle

### Frontmatter fields
- `injected: YYYY-MM-DD` — set on file creation, never updated (write-once)
- `referenced: YYYY-MM-DD` — updated automatically by hook when file is injected into context
- `status: active | pinned | stale | archived`

### Automatic rotation (Stop hook)
- `active` + no `referenced` update for N days → `stale` (N depends on type: mistakes=60, strategies=90, knowledge=90, feedback=45, notes/journal=30)
- `stale` + no `referenced` update for 90 days → moved to `archive/`
- `pinned` — never rotated, never archived

### What to pin
Critical mistakes with high recurrence, user profile, important references.

## Injection Pipeline

SessionStart hook injects (in order):
1. **Continuity** — last session context (if project detected)
2. **Project overview** — current project description
3. **Mistakes** — top 3 global + top 3 project-specific (scored by keywords, recurrence)
4. **Feedback** — up to 6 behavioral rules (keyword + WAL scored)
5. **Knowledge** — up to 4 domain facts (keyword + domain filtered)
6. **Notes** — up to 2 long-form notes (keyword + domain filtered)
7. **Strategies** — up to 4 proven approaches (keyword + WAL scored)

Also generates `_agent_context.md` — compact summary for subagents.

## Project Detection

`projects.json` maps filesystem paths to project IDs:
```json
{"/Users/me/dev/myapp": "myapp"}
```
Longest prefix match from `cwd`. Determines which project-specific files to inject.

## When to Write

| Signal | Action |
|---|---|
| Bug found, wrong approach taken | Write to `mistakes/` |
| Successful approach worth repeating | Write to `strategies/` |
| User corrects behavior or confirms approach | Write to `feedback/` |
| Learned API/infrastructure fact | Write to `knowledge/` |
| End of session with project work | Update `continuity/{project}.md` |

## When NOT to Write

- Code, diffs, debug output → derivable from git
- Conversation context → ephemeral
- Anything already in CLAUDE.md → avoid duplication
- File paths, project structure → derivable from filesystem

## Subagents

Subagents don't receive SessionStart injection. Include in their prompt:
```
Read ~/.claude/memory/_agent_context.md before starting.
```

## Consolidation

Run `~/.claude/bin/memory-consolidate.sh` periodically to find:
- Duplicate mistakes with same prevention
- Stale mistakes (recurrence: 0, old)
