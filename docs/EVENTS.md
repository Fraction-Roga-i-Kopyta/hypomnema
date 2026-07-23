# WAL events — normative registry

Source of truth for hypomnema WAL event types. Adding, deprecating, or changing
the `target` shape of an event is a contract change and MUST be recorded here in
the same commit that ships the change. This file is the durable index that other
docs (CHANGELOG, code comments, test fixtures) reference by event name.

The WAL (`~/.claude/memory/.wal`) is append-only and the source of truth; the
SQLite sidecar is a rebuildable projection of it (see `docs/ARCHITECTURE.md`).

## Schema reminder

Every WAL line carries exactly four pipe-delimited columns:

```
date|event|target|session
```

This registry catalogs `event` values and per-event `target` semantics. Pipes
and newlines in source data MUST be replaced with `_` before write — see
[`FORMAT.md § 5`](FORMAT.md#5-wal-grammar) and the `wal.SanitizeField`
sanitiser.

## Status legend

- **Active** — emitted by current v2 code; readers MUST handle.
- **Legacy** — emitted only by v1 (≤ `v1.1.2`); v2 does NOT emit it, but v2
  readers still tolerate it (aggregating or skipping) because historical WALs
  and migrated stores carry it. Do NOT emit from new code.
- **Retired** — a v1 event that no v2 reader treats specially; skipped as an
  unknown line. Listed so the row reads as deliberate, not an oversight.

---

## Registry — Active (emitted in v2)

Exactly these events are written by v2 code. Producers verified by grep of
`wal.Append(` across `internal/` + `cmd/` (non-test).

| event | target shape | producer (verb) | consumer | notes |
|---|---|---|---|---|
| `inject` | `<slug>` | `memoryctl inject` — `SessionStart` + `UserPromptSubmit` shims (`cmd/memoryctl/inject.go` → `persistInjected`) | `internal/sidecar` reproject (ref_count, last_injected); `internal/closer` via the session injected-set | one line per injected fact per delivery |
| `recall` | `<slug>` | `memoryctl recall` (pull CLI) and `skill-inject` (`cmd/memoryctl/recall.go`) | `internal/sidecar` reproject; `internal/closer` (joined into injected-set) | same-day repeat of one fact in one session dedups to a single ref bump |
| `trigger-useful` | `<slug>` | `memoryctl close` — `internal/closer` (`Classify`) | `internal/sidecar` (effectiveness `pos`); `internal/profile` | evidence phrase or name/slug cited in assistant transcript text |
| `trigger-silent` | `<slug>` | `memoryctl close` — `internal/closer` (`Classify`) | `internal/sidecar` (effectiveness `neg`); `internal/profile` | injected fact went uncited; skipped if the transcript was unreadable |
| `session-metrics` | `domains:_global_,error_count:N,tool_calls:M,duration:Ss` (`$3`); session id (`$4`) | `memoryctl close` — `internal/closer` | `internal/profile` (rollup); tolerated by `ab`, `doctor` | one per closed session |
| `session-close` | `<session_id>` (in both `$3` and `$4`) | `memoryctl close` — `internal/closer` | session-boundary marker; `internal/profile` | emitted for any error count |
| `dedup-blocked` | `<new-slug>><existing-slug>` (`>` sub-delim) | `memoryctl dedup check` (pre-tool) — `internal/dedup` | informational (`doctor` tolerates) | see note below |
| `dedup-merged` | `<new-slug>><existing-slug>` | `memoryctl dedup check` (post-tool) — `internal/dedup` | informational | emitted only after the on-disk recurrence bump succeeds |
| `dedup-candidate` | `<new-slug>~<existing-slug>` (`~` sub-delim) | `memoryctl dedup check` — `internal/dedup` | informational | soft-similarity, below the merge threshold |
| `retire` | `<qslug>[><successor>][:<reason>]` | `memoryctl retire` (`cmd/memoryctl/retire.go`) | `internal/sidecar` reproject (status=retired) | `<qslug>` = project-qualified slug; successor/reason use the `>`/`:` sub-delims, whitespace collapsed to `_` |
| `revive` | `<qslug>` | `memoryctl revive` (`cmd/memoryctl/retire.go`) | `internal/sidecar` reproject (cancels retirement) | |
| `candidate-confirmed` | `<qslug>` | `memoryctl close` — `internal/closer` | `internal/sidecar` reproject (candidate→active); `internal/doctor` | emitted once per fact (WAL dedup); graduation of a soft-corroboration candidate |

> **Dedup is not a default hook.** `memoryctl dedup check` is a real verb that
> emits the three `dedup-*` events, but the six installed shims do not wire it —
> it fires only when invoked explicitly. See `docs/ARCHITECTURE.md`.

---

## Registry — Legacy (v1-emitted, v2-tolerated)

v2 does not emit these, but v2 readers still consume them from historical or
migrated WALs. Do not emit them from new code.

| event | target shape | consumed by (v2 reader) | was produced by (v1) |
|---|---|---|---|
| `inject-agg` | `<slug>` (`$3`), aggregated count (`$4`) | `internal/sidecar` reproject (adds to ref_count); `internal/ab` | `hooks/wal-compact.sh` |
| `outcome-positive` | `<slug>` | `internal/sidecar` (effectiveness `pos`); `internal/profile`; `internal/ab` | `hooks/memory-outcome.sh` |
| `outcome-negative` | `<slug>` | `internal/sidecar` (effectiveness `neg`); `internal/profile`; `internal/ab` | `hooks/memory-outcome.sh` |
| `outcome-new` | `<slug>` | `internal/profile` | `hooks/memory-outcome.sh` (new mistake written) |
| `trigger-silent-retro` | `<slug>` | `internal/sidecar` (treated as `trigger-silent`); `internal/profile` | `hooks/wal-retro-silent.sh` |
| `clean-session` | `<domain>` or `_global_` | `internal/profile` | `hooks/session-stop.sh` (zero errors) |
| `strategy-used` | `<slug>` | `internal/profile` | `hooks/memory-outcome.sh` |
| `strategy-gap` | `<domain>` | `internal/profile` | `hooks/session-stop.sh` |
| `evidence-empty` | `<slug>` | `internal/doctor` (tolerated) | `hooks/lib/feedback-loop.sh` |
| `evidence-missing` | `<phrase-hash>` | `internal/doctor` (tolerated) | `hooks/lib/feedback-loop.sh` |
| `schema-error` | `<slug>` | `internal/doctor` (tolerated) | `hooks/lib/parse-memory.sh` |
| `format-unsupported` | `<slug>:N` | `internal/doctor` (tolerated) | `hooks/lib/parse-memory.sh` |

---

## Registry — Retired (no special v2 reader)

Emitted by v1 only and skipped as unknown lines by v2. Retired with the v1
mechanisms they belonged to (FTS5 shadow retrieval, substring triggers,
rotation-to-archive, cluster/cascade). Kept here so the rows read as deliberate.

`trigger-match`, `shadow-miss`, `cluster-load`, `cascade-review`,
`cascade-predictive` (never activated), `rotation-stale`, `rotation-archive`,
`rotation-summary`, `strategy-rescored`, `error-detect`, `index-stale`.

See git tag `v1.1.2` for the code that produced them and the pre-v2 revision of
this file for their original producer/consumer wiring.

---

## Glossary of `target` sub-delimiters

The four-column invariant is immutable within WAL format v1; richer data is
encoded into the `target` column with sub-delimiters. Readers ignore unknown
structure, so adding a sub-delimiter is non-breaking — but introduce them
sparingly to keep the WAL greppable.

| sub-delim | meaning | active example events |
|---|---|---|
| `,` | rows of equal weight (CSV-like) | `session-metrics` (inner) |
| `:` | `key:value` (single pair or namespace prefix) | `session-metrics` (inner) |
| `>` | directed pair (`from > to`, asymmetric) | `dedup-blocked`, `dedup-merged` |
| `~` | soft / similarity pair (symmetric) | `dedup-candidate` |

---

## Authoring rules

When adding a new event in v2:

1. **Add the row here first** with status `Active`, the exact `target` shape,
   the producing verb (`memoryctl <verb>` and the `internal/` package), and the
   consumer. This file is the authoritative spec; CHANGELOG narrates the change.
2. **Name it `<domain>-<action>`** (`dedup-merged`, not `merge_dedup`).
   Lowercase kebab-case only. Reserve suffix families for genuine siblings.
3. **Encode rich data into `target`** with an existing sub-delimiter, or add a
   new one to the glossary above with a worked example. Keep the four-column
   invariant — a fifth column is a format-major change (see
   [`FORMAT.md § 5.3`](FORMAT.md#53-extension-rules)).
4. **Wire the reader.** `internal/sidecar` reproject if it affects
   ranking metadata (ref_count/effectiveness/recency); `internal/profile` if it
   is a measurement the self-profile should report.
5. **Update `CHANGELOG.md`**, citing the row added here.

When retiring an event: flip its status to `Legacy` (still tolerated by readers)
or `Retired` (skipped), stop emitting in code, and keep the row — historical
WALs carry it for years.
