---
type: decision
project: global
created: 2026-04-20
status: active
description: FTS5 + BM25 runs alongside substring triggers as a fire-and-forget recall signal, not as an injection source.
keywords: [fts5, recall, shadow, sqlite, observability]
domains: [architecture, retrieval]
related: [substring-triggers-with-negation]
review-triggers:
  - metric: shadow_miss_ratio
    operator: ">"
    threshold: 0.30
    source: wal
  - after: "2027-04-20"
---

## What

`hooks/user-prompt-submit.sh` spawns `memory-fts-shadow.sh` (or
`memoryctl fts shadow`) as a detached subshell via `& disown`. The
shadow process runs an FTS5 + BM25 query against
`~/.claude/memory/index.db` over memory bodies. Matches that the
substring-trigger pipeline did not surface are logged as
`shadow-miss|<slug>|<session_id>` WAL events. **No file the shadow
finds enters the current session's context** — the primary trigger
pipeline keeps full authority over injection.

## Why

Trigger-phrase-based reactive injection is deliberately brittle: it
fires only on literal substring matches (with a ±40-char negation
window). On freshly installed memory — seeded corpora with empty
`triggers:` fields — `make replay` measured 2.1% trigger-match
against 89.6% shadow-miss. The substring channel is
starving; something has to name the starvation before we can
reason about fixing it.

Making shadow an observation instead of an injection keeps two
contracts intact: (1) determinism of reactive injection (same
prompt → same 4 files → same priority-key order) required by replay
measurement; (2) the substring-trigger field as the author-
controlled authority on what surfaces for which prompt. Shadow is
instrumentation on top of those contracts, not a replacement.

## Trade-off accepted

- **Recall doesn't improve until the signal is consumed.** `shadow-
  miss` accumulates in WAL. Before v0.10.3 nothing read it; since
  v0.10.3 `memory-analytics.sh` surfaces top-10 per 30-day window
  in `.analytics-report § Recall gaps`. The loop closes through the
  operator, not through the hook.
- **Index maintenance overhead.** `index.db` is a derivative that
  must stay fresh; `bin/memory-fts-sync.sh` / `memoryctl fts sync`
  run on query, with a cheap `SUM(mtime) + COUNT(*)` drift check on
  the hot path.
- **Shadow pass is fail-safe, so a broken FTS layer is silent.**
  `memoryctl doctor` lists the index age and the operator is
  expected to check.

## When to revisit

A documented case where a user genuinely needs an FTS-surfaced
record in the session and can't express it as a trigger phrase. At
that point the injection authority moves, and the question becomes
"how does FTS injection coexist with priority-key ordering"
(composite score vs lexicographic key in the same pipeline), which
is a v0.12 evidence-learn-adjacent problem.

## Calibration history

**2026-05-07 — false-positive cleanup.** `decisions review` fired
pressure at `shadow_miss_ratio = 0.65` (threshold 0.30). Evidence
probe against the 14-day session JSONL transcripts classified 8/8
sampled events as FTS5 false-positives: cross-project files (e.g. an
iOS Swift strategy `project: fly-mobile`) surfaced in unrelated
sessions (a Groovy/Confluence JIRA session) on incidental body-
keyword overlap. The substring pipeline already filters by project
via the priority-key first bucket; the shadow pass was missing the
same filter and so emitted shadow-miss for files that — by primary-
pipeline rules — could not have injected anyway.

Fix: added `CurrentProject` to `ShadowConfig` (Go) and a fourth
positional arg to `bin/memory-fts-shadow.sh` (bash). Files whose
frontmatter `project:` is neither empty/global nor equal to the
current project are now skipped before WAL emission. The 0.30
threshold is preserved — post-patch the ratio is expected to drop
sharply; if pressure still fires after a 14-day post-patch window,
the remaining miss events are the genuine recall gap and the
trigger phrases of the offending slugs should be widened.
