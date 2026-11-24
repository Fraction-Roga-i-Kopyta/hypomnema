---
name: memory-system
description: Persistent memory system for Claude Code (hypomnema v2) — ranked injection over native file memory: mistakes, strategies, feedback, knowledge, decisions, lifecycle
---

# Hypomnema — Memory System (v2)

Claude Code ships native file memory: markdown files under `~/.claude/projects/<slug>/memory/` (per-project) plus `~/.claude/memory-global/` (cross-project). Hypomnema adds ranked auto-injection, effectiveness measurement, decay, and a secrets gate on top. The authoritative protocol is the repo `CLAUDE.md` — this skill is a summary; when in doubt, read `CLAUDE.md`.

## Stores are flat

Type is encoded in the `type:` frontmatter field, **not** in a subdirectory. There are no `mistakes/` / `strategies/` / `continuity/` folders — every memory file sits directly in the store. Logical types: `mistake`, `strategy`, `feedback`, `knowledge`, `decision`, `note`, `project`, `continuity`.

## Frontmatter schema

Required on every file: `name`, `description`, `type`. Optional: `created` (YYYY-MM-DD), `status` (`active` | `pinned`), `keywords`, `domains`.

```yaml
---
type: mistake
name: "short-slug"
description: "one-liner shown in the index and injection header"
created: 2026-05-01
status: candidate   # agent-originated facts start as candidate; user-dictated rules use active|pinned
keywords: [tag1, tag2]
domains: [backend]
---
```

**Candidate lifecycle:** a `candidate` fact injects normally and graduates to
`active` (sidecar-managed) on its first useful citation; `doctor` flags
candidates that stay silent ≥5 sessions. Retire dead facts with
`memoryctl retire <slug> --reason "..." [--superseded-by <ref>]` — recall
then shows a tombstone redirect instead of silently forgetting.

**Never hand-set** `ref_count`, `effectiveness`, `injected`, or `referenced` — the sidecar manages them. **Do not use** `triggers`, `seed`, `decay_rate`, or a `project:` field: triggers are retired, seed/decay_rate are dead, and ownership is derived from which store the file lives in.

Type-specific fields:
- **mistake** — `severity` (minor|major|critical), `recurrence` (int), `scope` (universal|domain|narrow) — all three informational, nothing reads them — plus `root-cause`, `prevention`
- **strategy** — `success_count` (int, informational)
- **feedback** — optional `precision_class: ambient` (a rule that shapes behaviour silently: going uncited does not lower its effectiveness)
- **knowledge / decision / note** — optional `related: [slug, …]`

Usefulness comes only from an explicit `<cc-memory filenames="…">` citation of a fact actually delivered this session (see "Lifecycle" below). Do not write `evidence:` — it is ignored.

## When to write

| Signal | Type |
|---|---|
| Bug found, wrong approach taken, error repeated | `mistake` |
| Approach that worked for a class of problems | `strategy` |
| User corrects your approach or validates a non-obvious choice | `feedback` |
| Domain fact / API detail not obvious from the code | `knowledge` |
| Architecture / technology choice to defer to later | `decision` |
| Stopped mid-task at session end (3 lines max; rewrite the project's one `continuity.md` in place) | `continuity` |

A user rule that corrects a *repeated* mistake is written as `feedback` only — do not also write a `mistake` for the same rule (it duplicates signal).

## When NOT to write

Code patterns and conventions (read the code), git history (`git log`/`blame`), one-off bug fixes (they live in the code + commit message), conversation context, or anything already in a `CLAUDE.md`.

## Injection and ranking

**SessionStart does not rank: it injects a fixed core** — the project's freshest `continuity`, then `status: pinned` facts (project-local, then global), then project-local `feedback` — after `/clear` and compaction too (core only; the compaction summary carries the task). Core facts that do not fit the 8KB budget arrive as one-line pointers (`- name — path — description`): Read them if relevant. Everything else you read from the `MEMORY.md` index when a description fits the task, or pull with `recall`.

`memoryctl inject` on UserPromptSubmit (unless pull-only mode, `HYPOMNEMA_PROMPT_INJECT=off`, is on) ranks the `active`/`pinned`/`candidate` files of the current project's store plus the global store and injects those the prompt names, top-8 (2.5KB per body, 8KB total), once per context. You do not set ranks — write good `keywords` / `domains` / `description`; the score is `3.0 × overlap + 2.0 × recency (from the last useful citation) + 1.0 if project-local`. Effectiveness and ref_count are diagnostics, not ranking inputs. `session_keywords` come from prompt tokens, CWD basename, and git context.

## Citing memory

Every parent delivery opens with one instruction line: when a fact changes what you say, do, or decide, wrap that sentence in `<cc-memory filenames="FILE">…</cc-memory>` (hidden from the user). This citation — not a keyword or evidence-phrase match — is what `close` reads to credit a fact as useful; cite only the facts that actually changed your answer. A subagent's delivery asks it to cite in its final report instead; do not copy `<cc-memory>` tags from a subagent's report.

## Lifecycle

`memoryctl close` runs after each turn (Stop hook): classifies the injected set from explicit `<cc-memory filenames="…">` citations of facts actually delivered this session — `cite-useful` for a cited, delivered fact; `cite-silent` for an injected-but-uncited one, but only in a session that had at least one delivered citation. `close` then recomputes effectiveness (a diagnostic) and marks unused facts `stale` in the sidecar (age counts from last injection, else `created`, else the date of Claude Code's `modified` stamp). `pinned` files, each project's freshest `continuity` fact and project-local `feedback` never decay (they are the SessionStart core); only that freshest continuity is injected — older ones are recallable history that ages like a note. `project` ages like knowledge (90 days) — pin a project card that must never age. No native content is ever mutated by hooks.

## Pull retrieval

Push injection ranks against the user's prompt and can't see what you hit mid-task. For an unfamiliar error/domain or a previously-solved-smelling decision:

```
memoryctl recall "<3-6 query words>"
```

The top match arrives with its body; runner-ups come as an index of paths to Read. Stale facts are included and revived on recall.

## Secrets gate

`memoryctl guard` (PreToolUse:Write|Edit on memory paths) blocks credential patterns before they land in a file. Whitelist a path via `~/.claude/memory-global/.secretsignore`, or override once with `HYPOMNEMA_ALLOW_SECRETS=1`. Don't route around it by stripping values — whitelist the path or use an obvious placeholder.

## Subagents

A subagent receives memory at `SubagentStart` only when its task names it: a fact injects when its name/description/keywords/domains hold at least two distinct terms of the Agent call's `description` (the short task label — not the prompt), one of them in the name or keywords; passing facts are ranked to a top-5 (5KB total) under the subagent's own key. Most subagents get nothing. No Agent call of its own (nested agents included) → nothing — there is no fallback to the parent's prompts. Skipped entirely: `fork`, `Explore`, `claude-code-guide`, `statusline-setup` (overridable via `HYPOMNEMA_SUBAGENT_SKIP`). Its citations are measured under its own key, separate from the parent session. There is no auto-generated context file (`_agent_context.md` was retired with v1) — pass inline what the subagent needs, and write a description that names the task.
