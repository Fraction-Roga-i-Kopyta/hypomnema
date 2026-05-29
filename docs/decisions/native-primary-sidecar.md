---
type: decision
project: global
created: 2026-05-29
status: active
description: Claude Code native memory is the canonical content store; hypomnema is a governance + ranking layer; metadata lives in a rebuildable SQLite sidecar; a hypomnema-owned global store fills native's per-project-only gap.
keywords: [native, sidecar, sqlite, storage, architecture, v2]
domains: [architecture]
related: [file-based-memory, bash-go-gradual-port, go-primary-bash-shims]
---

## What

Claude Code v2.1.59+ ships default-on native file memory
(`~/.claude/projects/<slug>/memory/*.md`) with a minimal frontmatter
schema (name / description / type) and a `MEMORY.md` index that the
harness injects on session start.

v2.0 adopts native as the **sole content store** for hypomnema. No
parallel markdown tree under `~/.claude/memory/` is maintained. The
full hypomnema frontmatter schema (8 types, ref_count, effectiveness,
decay, domains, …) lives in a **SQLite sidecar** keyed on native
file slugs. The sidecar is a derived projection — delete it and it
rebuilds from the WAL + a native frontmatter scan. WAL (append-only
text) remains the source of truth for events.

Native memory is **per-project only** — there is no global store.
Hypomnema fills that gap with a hypomnema-owned directory
(`~/.claude/memory-global/` by default, configurable) that stores
global facts in native format. The `inject` command merges
project-native + global-store candidates at runtime; the sidecar
tracks both. No platform support is required for this.

Architecture diagram (from the v2 design spec):

```
┌─ Claude Code harness ───────────────────────────────────────┐
│  native memory: ~/.claude/projects/<slug>/memory/*.md        │
│  • content (minimal frontmatter: name/description/type)      │
│  • MEMORY.md index — injected by the harness itself          │
└───────────────▲───────────────────────────▲─────────────────┘
       read-only │ (content)        Write-intercept │ (guard)
┌────────────────┴───────────────────────────┴────────────────┐
│  hypomnema v2 — memoryctl (Go) + thin bash shims             │
│                                                              │
│  inject ──► additionalContext (ranked top-K)                 │
│  close  ──► outcomes → WAL → effectiveness/decay → profile   │
│  guard  ──► secrets-gate + dedup (PreToolUse:Write)          │
│  migrate──► one-shot conversion + pruning                    │
│                                                              │
│  WAL (append-only text) = source of truth for events         │
│  SQLite sidecar = derived projection (rebuildable)           │
│  ~/.claude/memory-global/ = global store (native format)     │
└──────────────────────────────────────────────────────────────┘
```

The sidecar schema (one row per native file):

```sql
CREATE TABLE memory (
  slug         TEXT PRIMARY KEY,   -- path relative to memory-dir
  content_sha  TEXT,               -- detects rename/edit → re-link
  type TEXT, name TEXT, description TEXT,
  project TEXT, domains TEXT,
  created TEXT, last_injected TEXT,
  ref_count INTEGER DEFAULT 0,
  status TEXT DEFAULT 'active',    -- active|pinned|stale
  effectiveness REAL               -- (pos+1)/(pos+neg+2), Bayesian
);
CREATE TABLE keyword (slug TEXT, term TEXT, weight REAL);
CREATE TABLE outcome (slug TEXT, ts TEXT, kind TEXT);
CREATE TABLE meta    (k TEXT PRIMARY KEY, v TEXT);
```

hypomnema never writes native *content* directly (no schema conflict,
no format ownership dispute) except for one managed exception:
`continuity/` auto-generated files, which are 100% derived from git +
session state and are explicitly marked as such.

## Why

Four converging pressures:

1. **Native comoditises capture + index.** Claude Code handles write
   ergonomics and the `MEMORY.md` index injection. Building a parallel
   store would duplicate effort and create a two-source sync problem.

2. **Governance is where hypomnema adds value.** Ranked injection,
   outcome measurement, decay, dedup, and the global store are all
   absent from native. These are not commoditised — they are the
   differentiators (see `docs/measurements/2026-05-29-v2-ranker-ab.md`:
   ranking beats random 8–20× on static signals alone).

3. **Sidecar is derivable, not authoritative.** All event history is
   in the WAL. Metadata in the sidecar is a projection — it can be
   deleted and rebuilt. This satisfies the transparency property
   from `file-based-memory.md` (users can `cat`, `grep`, `rm` any
   memory file without tooling) while adding a structured index.

4. **Global store fills a structural gap.** Native has no per-user
   global memory. Rules like `language-always-russian` or
   `wrong-root-cause-diagnosis` must be available in every project
   session. `~/.claude/memory-global/` is the minimal viable answer:
   the same native format, injected by hypomnema's `inject` command,
   tracked by the same sidecar.

This decision activates the revisit clause in `file-based-memory.md`:
*"if a native Claude Code memory API ships that covers the same
transparency + portability surface, hypomnema is a compatibility shim,
not the primary store."* The clause fired — but the verdict is
**governance layer + global store**, not shim. The v2 design spec
(`docs/specs/2026-05-28-v2-native-memory-design.md`) has the full
reasoning.

## Trade-off accepted

- **Coupled to Claude Code.** Native memory is Claude Code-specific.
  If the user switches harnesses, hypomnema v2 needs a new `native`
  adapter. The `internal/native` package is the only place that knows
  native format — changing adapters is a targeted swap, not a
  whole-system rewrite. Cross-agent portability is an explicit
  non-goal for v2 (spec §9).
- **Sidecar is a second artifact to manage.** Doctor flags sidecar
  drift; `inject` auto-rebuilds on content_sha mismatch. Acceptable
  overhead given the alternatives (awk/TSV parsing on every hook call,
  which was the v1 cost).
- **content_sha ambiguity on rename.** If a file is renamed and edited
  simultaneously, the sha-based re-link may create a new row rather
  than update the old one. Outcome history for the old slug is
  preserved (not deleted); doctor flags the orphan.

## When to revisit

If Claude Code's native memory gains any of:
- A ranking/scoring layer that approaches hypomnema's recall@K numbers.
- A global (cross-project) store.
- A decay / lifecycle API.

At that point, re-evaluate which governance components still earn
their keep versus components that can be delegated to the platform.
The `internal/native` adapter boundary is the right seam for that
negotiation.
