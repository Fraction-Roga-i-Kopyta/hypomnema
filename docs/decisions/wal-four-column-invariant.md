---
type: decision
project: global
created: 2026-04-22
status: active
description: WAL lines are exactly four pipe-delimited columns — date|event|target|session — immutable within format v1.
keywords: [wal, format, invariant, awk]
domains: [architecture, storage]
review-triggers:
  - after: "2027-04-22"
---

## What

Every WAL entry is exactly four pipe-delimited fields:
`YYYY-MM-DD|event-type|target|session-id`. The field count is fixed.
Events that need to carry more than one datum in the third position
encode it with a sub-delimiter (`:`, `>`, `,`) inside the target field
— see `inject-agg` (`slug,count`), `dedup-blocked`
(`new-slug>existing-slug`), `session-metrics`
(`error_count:N,tool_calls:M,duration:Ss`). A fifth column requires a
format major-version bump.

## Why

Readers are numerous and written in four languages: bash awk
(`awk -F'|' '{print $4}'`), perl, Go (`strings.SplitN(line, "|", 4)`),
and sqlite3 (import pipeline). A column-count change silently
corrupts every one of them that doesn't check field length, and
making them all check field length every read is a tax paid on the
hot path forever. Pinning field count at the grammar level pushes
the evolution question to the version-bump door, where it belongs.

## Trade-off accepted

- `session-metrics` at `hooks/session-stop.sh:450` already uses
  column 4 for metrics pairs instead of session_id, technically
  violating the invariant. Documented in
  `docs/notes/measurement-bias.md`; fixing it is the first thing a
  format v1 → v2 bump would do.
- Compound targets (`a>b`, `a:b`, `a,b`) are opaque to a casual
  reader and need per-event-type knowledge to parse. The tax is
  paid on introduction of each new compound form, not on read.
- Structured payloads (JSON-in-WAL, protobuf) are precluded. Hooks
  that want richer records write a side-car file and reference its
  slug in column 3.

## When to revisit

A signal that the target column has gained more than two sub-
delimiter forms (`a>b:c,d`) or that readers are regularly
misparsing because of escape-free `|` leakage in user-authored
fields. Either is evidence that a v1 → v2 bump is overdue.
