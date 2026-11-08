# Hypomnema storage format — contract v2

**Status:** STABLE. Any change to this document is a breaking change to the
memory engine and requires a major version bump.

This document describes what's on disk in **hypomnema v2**, which sits on top of
Claude Code's native file memory. It is the shared surface between `memoryctl`
(`cmd/memoryctl/` + `internal/`) and any alternate implementation. Anything
outside this document — ranking-formula constants, specific hook scripts,
sidecar schema — is implementation detail and may change between minor
versions.

If you read or write hypomnema memory, conform to this document. If you need
behaviour it does not describe, propose an extension; do not invent silently.

---

## 1. Scope

v2 stores content in Claude Code's **native** per-project memory plus a global
store, and keeps its own runtime state (WAL, sidecar) alongside. There is no
`format_version:` frontmatter field — that v1 mechanism (and the
`format-unsupported` skip event) is retired. Implementations MUST:

- Ignore unknown frontmatter fields (forward-compatibility).
- Tolerate unknown WAL event types (log, don't crash — §5).
- Treat the WAL grammar (§5) as the one hard on-disk invariant `memoryctl wal
  validate` enforces.

---

## 2. On-disk layout

Content lives in **flat** native stores (no fixed subdirectories — type is a
frontmatter field, not a folder):

```
~/.claude/projects/<slug>/memory/*.md   # per-project native store
~/.claude/memory-global/*.md            # global store (facts applicable everywhere)
~/.claude/memory-global/.secretsignore  # optional glob whitelist for the secrets gate
```

Hypomnema's own runtime state is the only thing under `~/.claude/memory/`:

```
~/.claude/memory/
├── .wal              # append-only write-ahead log — source of truth (§5)
├── .wal.pre-compact-<date>  # original WAL kept by `memoryctl wal compact` (optional)
├── .sidecar.db       # SQLite projection: ref_count, effectiveness, status, ... (§6)
└── .runtime/         # session-scoped markers
    ├── injected-<session_id>.list   # slugs injected this session
    └── active-skill-<sid>           # skill activated this session
```

There are **no** `mistakes/` `strategies/` `feedback/` `knowledge/` `decisions/`
`notes/` `projects/` `continuity/` subdirectories, and **no** `archive/`,
`journal/`, `seeds/` directories, `.config.sh`, `.stopwords`, `projects.json`,
`.tfidf-index`, or `index.db`. Those are all retired v1 layout. `.sidecar.db` is
the single derivative index; `.wal` + the native markdown are the source of
truth, and the sidecar is rebuildable from them (`memoryctl sidecar rebuild`).

---

## 3. Universal frontmatter

Every memory file is Markdown opening with a YAML frontmatter block delimited by
`---` lines. Shared fields:

| Field | Required | Type | Notes |
|---|:-:|---|---|
| `type` | yes | enum | `mistake \| strategy \| feedback \| knowledge \| decision \| note \| project \| continuity \| skill-learning`. |
| `name` | yes | string | Sidecar key; kebab-case slug. |
| `description` | yes | string | One-line summary; shown in the `MEMORY.md` index and injection headers. |
| `created` | no | `YYYY-MM-DD` | You set it — novelty signal: recency uses it only until the fact earns a useful citation (`last_useful` then takes precedence); when absent, the date of `modified` is substituted, else the WAL-earliest date. |
| `modified` | no | ISO date or timestamp | Stamped by Claude Code on every Write/Edit (under `metadata:`); it never writes `created`. Only the `YYYY-MM-DD` prefix is read — as the `created` fallback and to pick a project's freshest `continuity` fact. Not hand-set. |
| `status` | no | enum | `active \| pinned`. `pinned` protects from decay. `stale` is **sidecar-managed** — never hand-set it. |
| `keywords` | no | list | Primary ranking signal. Inline `[a, b]` or block `- a` form. |
| `domains` | no | list | Domain filter; kebab-case for multi-word. Same forms as `keywords`. |

**`project` is derived, not a field.** A fact's project comes from *which store
holds it* — files in `~/.claude/projects/<slug>/memory/` belong to `<slug>`;
files in `~/.claude/memory-global/` are global. Do not write a `project:` key.

**`ref_count` and `effectiveness` are sidecar-managed.** `memoryctl` reads and
updates them from the WAL. Never set them in frontmatter — they are not
frontmatter fields in v2.

**Field ordering** is not significant. Implementations SHOULD preserve order on
round-trip but MUST accept any order.

### 3.1 `status: pinned`

`pinned` guarantees the fact is **never decayed** to `stale` by the close hook
regardless of age, and that `pinned` outranks `active` on ranking ties. It does
NOT reserve an injection slot — the top-8 flex pool is allocated by relevance
score, so a pinned fact with weak keyword/effectiveness signal can still lose
its slot. A project's freshest `continuity` fact also never decays, pinned or
not (§4); every other type ages, `project` included.

### 3.2 Tolerated syntax (parser MUST accept)

Reflecting the actual parser (`internal/native.go` `splitFrontmatter`):

- **UTF-8 BOM** at file start is stripped.
- **CRLF** line endings are tolerated (trailing `\r` trimmed).
- **Tab after a key**: `status:\tactive` parses the same as `status: active`.
- **Quoted scalars**: `status: "active"` / `status: 'active'` parse as `active`
  (surrounding single/double quotes stripped).
- **Block-style lists** (`keywords:` on its own line, then `  - a` / `  - b`)
  collapse into the same comma-joined value as inline `[a, b]`.
- **`metadata:` block** (`metadata:` on its own line, then indented `type:` /
  `created:` / `modified:` / `status:` / `keywords:` / `domains:` …): direct children are
  promoted to top-level keys; a column-0 key of the same name wins. This is
  the shape Claude Code's native memory instructions produce.

### 3.3 Not supported

- **Multi-line block scalars** (`root-cause: |` / `description: >` with indented
  continuation lines). The parser treats the block marker as the scalar value
  and skips the indented continuation lines entirely — the prose is lost. Use a
  single-line scalar and put long text in the document body.
- **Unclosed frontmatter** (no second `---`). No fields are parsed; the whole
  document is treated as body, so the file carries no ranking metadata.
- Top-level keys must sit at **column 0**; indented `key: value` lines are read
  as block-scalar continuation and ignored, not as frontmatter keys. The single exception is the direct children of a top-level `metadata:` key (§3.2).

---

## 4. Per-type extensions

### `type: mistake`

| Field | Required | Notes |
|---|:-:|---|
| `severity` | yes | `critical \| major \| minor`. |
| `recurrence` | yes | Integer — observed repetitions. Informational: no code reads it. |
| `scope` | no | `universal \| domain \| narrow` — informational: how broadly the lesson applies. |
| `root-cause` | yes | Single-line: what went wrong. |
| `prevention` | yes | Single-line: how to avoid it. |

### `type: strategy`

| Field | Required | Notes |
|---|:-:|---|
| `success_count` | no | Integer, starts at 0. Informational: no code reads it. |

`trigger:` / `triggers:` are **retired** — v2 has no substring-cue matching. A
strategy surfaces through `keywords` / `domains` relevance like any other type.

### `type: feedback`

| Field | Required | Notes |
|---|:-:|---|
| `precision_class` | no | `ambient` marks a rule that shapes behaviour silently (language preference, security baseline): going uncited does not lower its effectiveness — the silence penalty is dropped. Still injected/ranked normally. Read from any type. |

An `evidence:` list in older files is ignored — usefulness comes only from an
explicit `<cc-memory filenames="…">` citation.

### `type: skill-learning`

| Field | Required | Notes |
|---|:-:|---|
| `skill` | yes | Skill name this learning binds to. `skill-inject` (PostToolUse `Skill`) retrieves learnings by this field. |

### `type: knowledge` / `decision` / `note`

| Field | Required | Notes |
|---|:-:|---|
| `related` | no | List of sibling slugs this file clusters with. |

### `type: project`

Claude Code's own type for ongoing work, goals, and constraints. No additional
required fields; body is human-readable prose. Ages like `knowledge` (stale
after 90 days unused); a project card that must never age should be
`status: pinned`.

### `type: continuity`

"Where we left off." **Agent-written** at session end (three lines max: task,
status, next step) as a regular file in the project's native store — there is no
`continuity/` subdirectory and the Stop hook does not generate it. Only the
project's **freshest** continuity fact — the later of `created`/`modified`;
on a tie, the lexically greater filename — is pushed by injection and exempt
from decay. Older continuity files are history: still recallable, no longer
injected, stale after 30 days unused like a `note`. Keep one file and rewrite
it in place.

---

## 5. WAL grammar

The WAL (`~/.claude/memory/.wal`) is an append-only plaintext log (the one
rewrite is the manual `memoryctl wal compact`, §5.2). Each row has
exactly **four** pipe-delimited fields (enforced by `memoryctl wal validate`,
`internal/wal/validate.go`):

```
date|event|target|session
```

| Field | Shape |
|---|---|
| `date` | `^\d{4}-\d{2}-\d{2}$` (`YYYY-MM-DD`), local timezone of the writer. |
| `event` | `^[a-z][a-z0-9-]*$` — lowercase kebab-case. |
| `target` | Slug or identifier. `\|`, newlines, and `\r` MUST be sanitised to `_` (else the row splits or fails validation). |
| `session` | Non-empty session id (sanitised to `_`); writers use `unknown` / `cli` rather than an empty column. May be a subagent's compound observation key, `<session_id>:<agent_id>`, verbatim (not sanitised further) — `SubagentStart`/`SubagentStop` write it as-is; only runtime file NAMES map `:` to `_` (`pathutil.SafeFileName`). |

The **four-column invariant is immutable**. Richer data is encoded inside
`target` with a sub-delimiter (`,` or `>`), never a fifth column. Empty rows are
skipped. An optional header `# hypomnema-wal v2` is tolerated on the **first**
line only and not counted as a row.

`memoryctl wal validate` exits `0` (clean), `1` (≥1 failing row), or `2` (WAL
missing / command misuse). It checks grammar, not event vocabulary — unknown
event names pass validation.

### 5.1 v2 event vocabulary

Written by the v2 hooks / verbs:

| Event | Writer | `target` |
|---|---|---|
| `inject` | `inject` (SessionStart / UserPromptSubmit / SubagentStart) | slug |
| `recall` | `recall`, `skill-inject` | slug |
| `cite-useful` | `close` | slug (citation resolved AND delivered this session — injected/recalled/skill-injected, or read directly with the Read tool) |
| `cite-silent` | `close` | slug (injected fact went uncited; written only in a session with ≥1 delivered citation) |
| `cite-undelivered` | `close` | slug (citation resolved to an in-scope fact but never delivered this session; diagnostic only — feeds no reader) |
| `cite-none` | `close` | session id (both `$3` and `$4`); one per session with injected facts and zero delivered citations |
| `session-close` | `close` (also `close --subagent`) | session id (canonical `$4`) — a subagent's row uses its compound key and is deduped (`wal.AppendSuffixUnique`); the parent's own per-turn row is not |

`trigger-useful` / `trigger-silent` are **legacy since v2.14** (superseded by
`cite-useful`/`cite-silent`) — `close` no longer writes them; see
`docs/EVENTS.md` for the full registry and status legend.

Additionally **consumed** by the sidecar projection for effectiveness/ranking
(read for back-compat even if not actively written by v2 hooks): `inject-agg`
(WAL compaction fold, `target = slug,count`), `outcome-positive`,
`outcome-negative`, and `trigger-silent-retro` (along with `trigger-useful`/
`trigger-silent` themselves — tolerated as history, no longer emitted or
scored; `wal compact` drops them, §5.2).

Effectiveness is the Bayesian `(pos+1)/(pos+0.25·neg+2)` (cite-silent counts ¼ — the model under-cites, so silence is weak evidence) over one citation
observation per `(slug, session)` — `cite-useful` wins over `cite-silent`
within a session; legacy `outcome-*`/`trigger-*` rows are tolerated on read
but no longer scored (see `docs/EVENTS.md`).

### 5.2 Legacy events and `wal compact`

Older WALs carry event types nothing writes any more — `trigger-*`,
`session-metrics`, `ablate-start`/`ablate-stop`, `holdout-*`, `dedup-*` (see
`docs/EVENTS.md`); none of them feeds the sidecar projection, so compacting
them away changes no score, recency or decay age.
`memoryctl wal compact [--dry-run]`
rewrites the WAL under its lock without the four types no reader consumes —
`trigger-useful`, `trigger-silent`, `trigger-silent-retro`, `session-metrics` —
keeping every other row (unknown or malformed ones included) byte-for-byte and
in order. With nothing to drop it changes nothing. The original stays as
`.wal.pre-compact-<date>` (never replaced — a second backup that day gets a
`-2` suffix), and the sidecar is re-projected so its recorded WAL offset matches the new file. Run it when no
session is mid-turn: an append that times out on the lock writes without it
and could land in the backup instead.

### 5.3 Extension rules

- Adding a new event type is non-breaking; readers MUST ignore (not crash on)
  unknown events.
- The four-column invariant is immutable — a fifth column requires a major bump.
- New events SHOULD follow `<domain>-<action>` naming.

---

## 6. Derivative index

`.sidecar.db` (SQLite) is the **only** derivative index in v2. It projects
`ref_count`, `effectiveness`, `status`, project, and keywords from the WAL +
native frontmatter. Any implementation MAY delete it and rebuild from source
(`memoryctl sidecar rebuild`); conforming readers MUST tolerate its absence.
The v1 `.tfidf-index`, `index.db` (FTS5), and `.analytics-report` are retired.

`MEMORY.md` (per-project index) is regenerated by the Stop hook / `memoryctl
reindex` and is likewise rebuildable, not a source of truth.

---

## 7. Backward compatibility

An implementation MUST:

1. **Accept** every frontmatter field and WAL event type in this document.
2. **Preserve** unknown frontmatter fields on round-trip.
3. **Tolerate** unknown WAL event types (log, don't crash).
4. **Never** hand-mutate sidecar-managed fields (`ref_count`, `effectiveness`,
   `stale`) as frontmatter — they live in the sidecar.

An implementation MAY add optional frontmatter fields, emit new WAL event types,
and regenerate `.sidecar.db` / `MEMORY.md` at any time. It MUST NOT rename or
change the meaning of an existing field/event, or break the WAL four-field
invariant.

---

## 8. Minimum working example

```markdown
---
type: mistake
name: bsd-sed-range
description: Assumed GNU sed range semantics on macOS
created: 2026-04-22
status: active
severity: major
recurrence: 2
keywords: [bsd, sed, portability]
domains: [tooling, shell]
root-cause: "Used 1,/pattern/ sed range assuming BSD semantics"
prevention: "Use /pattern/,/pattern/ or awk — the range address differs on GNU vs BSD sed"
---

On macOS `sed '1,/^---$/d'` deletes only line 1; on Ubuntu it extends to the
next match and wipes everything between. See CI run f31b8a6 for the trace.
```

A minimal valid WAL:

```
# hypomnema-wal v2
2026-04-22|inject|bsd-sed-range|session-abc
2026-04-22|cite-useful|bsd-sed-range|session-abc
2026-04-22|session-close|session-abc|session-abc
```
