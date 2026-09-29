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
- **mistake** — `severity` (minor|major|critical), `recurrence` (int), `scope` (universal|domain|narrow, informational), `root-cause`, `prevention`
- **strategy** — `success_count` (int)
- **feedback** — `evidence:` (≤5 phrases Claude would write when applying the rule; used only for the `ablate` holdout observation on a fact withheld this session), optional `precision_class: ambient`
- **knowledge / decision / note** — optional `related: [slug, …]`

`evidence:` is type-agnostic — any injected file can carry it. Usefulness itself comes only from an explicit `<cc-memory filenames="…">` citation of a fact actually delivered this session (see "Lifecycle" below) — keep `evidence:` short regardless, since long lists rarely match verbatim for the holdout comparison.

## When to write

| Signal | Type |
|---|---|
| Bug found, wrong approach taken, error repeated | `mistake` |
| Approach that worked for a class of problems | `strategy` |
| User corrects your approach or validates a non-obvious choice | `feedback` |
| Domain fact / API detail not obvious from the code | `knowledge` |
| Architecture / technology choice to defer to later | `decision` |
| Stopped mid-task at session end (3 lines max) | `continuity` |

A user rule that corrects a *repeated* mistake is written as `feedback` only — do not also write a `mistake` for the same rule (it duplicates signal).

## When NOT to write

Code patterns and conventions (read the code), git history (`git log`/`blame`), one-off bug fixes (they live in the code + commit message), conversation context, or anything already in a `CLAUDE.md`.

## Injection and ranking

`memoryctl inject` (SessionStart + UserPromptSubmit hooks) ranks all `active`/`pinned` files in the current project's store plus the global store, and injects the top-8 (2.5KB per body, 8KB total) once per context — again after a compaction/clear wipes the model's context. You do not set ranks — write good `keywords` / `domains` / `description` and let the ranker score by overlap + recency + effectiveness. `session_keywords` come from prompt tokens, CWD basename, and git context. `SubagentStart` runs the same ranker for each eligible subagent against its own task, injecting a smaller top-5 (5KB total) under that subagent's own key.

## Citing memory

Every delivery opens with one instruction line: when a fact changes what you say or do, wrap that sentence in `<cc-memory filenames="FILE">…</cc-memory>` (hidden from the user). This citation — not a keyword or evidence-phrase match — is what `close` reads to credit a fact as useful; cite only the facts that actually changed your answer.

## Lifecycle

`memoryctl close` runs after each turn (Stop hook): classifies the injected set from explicit `<cc-memory filenames="…">` citations of facts actually delivered this session — `cite-useful` for a cited, delivered fact; `cite-silent` for an injected-but-uncited one, but only in a session that had at least one delivered citation. `evidence:` phrases no longer produce usefulness; they feed only the `ablate` holdout observation. `close` then recomputes effectiveness and marks unused facts `stale` in the sidecar (age counts from last injection). `pinned` files and `continuity`/`project` facts never decay. No native content is ever mutated by hooks.

## Pull retrieval

Push injection ranks against the user's prompt and can't see what you hit mid-task. For an unfamiliar error/domain or a previously-solved-smelling decision:

```
memoryctl recall "<3-6 query words>"
```

The top match arrives with its body; runner-ups come as an index of paths to Read. Stale facts are included and revived on recall.

## Secrets gate

`memoryctl guard` (PreToolUse:Write|Edit on memory paths) blocks credential patterns before they land in a file. Whitelist a path via `~/.claude/memory-global/.secretsignore`, or override once with `HYPOMNEMA_ALLOW_SECRETS=1`. Don't route around it by stripping values — whitelist the path or use an obvious placeholder.

## Subagents

Subagents receive ranked memory automatically at `SubagentStart` — query = the agent's own launch task, else the parent's recent prompts — except a short skip list (`fork`, `Explore`, `claude-code-guide`, `statusline-setup`, overridable via `HYPOMNEMA_SUBAGENT_SKIP`). Its citations are measured under its own key, separate from the parent session. There is still no auto-generated context file (`_agent_context.md` was retired with v1) — pass inline only what is specific to the task beyond what the ranker already covers.
