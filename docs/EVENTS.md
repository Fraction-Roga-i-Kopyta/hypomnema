# WAL events — normative registry

Source of truth for hypomnema WAL event types. Adding, deprecating, or changing
the `target` shape of an event is a contract change and MUST be recorded here in
the same commit that ships the change. This file is the durable index that other
docs (CHANGELOG, code comments, test fixtures) reference by event name.

The WAL (`~/.claude/memory/.wal`) is append-only and the source of truth; the
SQLite sidecar is a rebuildable projection of it (see `docs/ARCHITECTURE.md`).
The one rewrite is the manual `memoryctl wal compact`, which drops only the
legacy rows no reader consumes (see [Compaction](#compaction)).

## Schema reminder

Every WAL line carries exactly four pipe-delimited columns:

```
date|event|target|session
```

This registry catalogs `event` values and per-event `target` semantics. Pipes
and newlines in source data MUST be replaced with `_` before write — see
[`FORMAT.md § 5`](FORMAT.md#5-wal-grammar) and the `wal.SanitizeField`
sanitiser.

The `session` column (`$4`) may itself be `<session_id>:<agent_id>` — a
subagent's own observation key, written by the `SubagentStart`/`SubagentStop`
shims (`memoryctl inject --event=SubagentStart`, `memoryctl close
--subagent`). Sidecar effectiveness (one observation per (slug, parent
session)) and `doctor` candidate checks fold a key back to its parent
(`wal.ParentSession` — the part before the first `:`) so a session and its
subagents count as one observation; `ab` and doctor's
`citation_signal`/`open_quanta_last_30d` instead treat each subagent key as
its own session (reported as a separate subagent tally in doctor's
`citation_signal`). Per-session dedup on `cite-*`/`session-close`
rows is end-anchored (`wal.AppendSuffixUnique`, matching `strings.HasSuffix`)
rather than plain substring, precisely so a subagent row ending in
`s1:a1` never suppresses the parent's own row ending in plain `s1`.

## Status legend

- **Active** — emitted by current v2 code; readers MUST handle.
- **Legacy** — emitted only by v1 (≤ `v1.1.2`), or by v2 before a later
  cutover superseded it (e.g. `trigger-useful`/`trigger-silent`, v2.0–v2.13,
  superseded by `cite-useful`/`cite-silent` in v2.14) or its feature was
  removed (`session-metrics`, `ablate-*`/`holdout-*`, through v2.17); the current v2 code
  does NOT emit it, but v2 readers still tolerate it (aggregating or
  skipping) because historical WALs and migrated stores carry it. Do NOT
  emit from new code.
- **Retired** — an event that no current reader treats specially; skipped as
  an unknown line. Listed so the row reads as deliberate, not an oversight.

---

## Registry — Active (emitted in v2)

Exactly these events are written by v2 code. Producers verified by grep of
`wal.Append(` across `internal/` + `cmd/` (non-test).

| event | target shape | producer (verb) | consumer | notes |
|---|---|---|---|---|
| `inject` | `<slug>` | `memoryctl inject` — `SessionStart` + `UserPromptSubmit` + `SubagentStart` shims (`cmd/memoryctl/inject.go` → `persistInjected`; `cmd/memoryctl/subagent.go` → `runSubagentStart`) | `internal/sidecar` reproject (ref_count, last_injected); `internal/closer` via the session injected-set | one line per injected fact per delivery; a `SubagentStart` delivery writes under the subagent's own `<session_id>:<agent_id>` key |
| `recall` | `<slug>` | `memoryctl recall` (pull CLI) and `skill-inject` (`cmd/memoryctl/recall.go`) | `internal/sidecar` reproject; `internal/closer` (joined into injected-set) | same-day repeat of one fact in one session dedups to a single ref bump; does NOT feed last_useful (delivery, not use) |
| `cite-useful` | `<slug>` | `memoryctl close` — `internal/closer` (`Citations` + `resolveCitations`) | `internal/sidecar` (effectiveness `pos`); sidecar last_useful (ranking recency); `internal/ab`; `internal/doctor` candidates | slug/file name appears in an assistant-authored `<cc-memory filenames="…">` citation in the transcript; written once per (fact, session) — a later changed verdict adds its own row. Credit requires **delivery**, not just the citation text: the fact must have been injected/recalled/skill-injected this session (in `injected-<sid>.list`) OR read directly with the Read tool from its resolved project-store or global-store path — a resolved citation for an undelivered fact writes `cite-undelivered` instead. Delivery is session-scoped, not ordered: a citation earlier in the session still earns credit if the fact is delivered later in the same session. Read-path delivery matches the cleaned path string, so a store reached through a symlinked alias is not recognised (fails closed to `cite-undelivered`); on a project/global basename tie, the project copy gets the credit |
| `cite-silent` | `<slug>` | `memoryctl close` — `internal/closer` | `internal/sidecar` (effectiveness `neg`, weight 0.25 — `internal/usefulness`; one observation per (slug, parent session) — a session and its subagents count once); `internal/doctor` candidates | injected fact went uncited; written only in a session with ≥1 resolvable **delivered** citation (see `cite-none`) — never fabricated for a session where the citation channel itself may be broken, and an undelivered citation alone does not arm this guard either; written once per (fact, session) — a later changed verdict adds its own row |
| `cite-undelivered` | `<slug>` | `memoryctl close` — `internal/closer` | none — diagnostic only | a citation resolved to an in-scope fact that was neither injected/recalled/skill-injected this session nor read directly with the Read tool — counts for nothing else: not useful, not candidate-confirmed, and does not arm the `cite-silent` guard; written once per (fact, session) |
| `cite-none` | `<session_id>` (both `$3` and `$4`) | `memoryctl close` — `internal/closer` | `internal/doctor` `open_quanta_last_30d` (via `closingEvents`); `internal/doctor` `citation_signal` counts it (any `cite-*` prefix) as proof the v2.14+ citation channel has run at all, and as the mark that a session was classified — N counts injected sessions carrying any `cite-*` row, M the subset that also carries `cite-useful` | one per session, only when the transcript was readable, had ≥1 injected fact, and resolved zero **delivered** citations (a session with only `cite-undelivered` citations still writes this); marks the session's classification pass as closed so a citation-less-but-readable session does not read as an open/lost quantum |
| `session-close` | `<session_id>` (in both `$3` and `$4`) | `memoryctl close` — `internal/closer` (also `memoryctl close --subagent` — `SubagentStop` — under the subagent's `<session_id>:<agent_id>` key) | session-boundary marker; `internal/doctor` sidecar-freshness check (a parent `session-close` past the sidecar's WAL offset) | the parent's row is written on every `Stop` (per turn); a subagent's row is deduped end-anchored (`wal.AppendSuffixUnique`) so a repeated `SubagentStop` for the same key adds nothing |
| `retire` | `<qslug>[><successor>][:<reason>]` | `memoryctl retire` (`cmd/memoryctl/retire.go`) | `internal/sidecar` reproject (status=retired) | `<qslug>` = project-qualified slug; successor/reason use the `>`/`:` sub-delims, whitespace collapsed to `_` |
| `revive` | `<qslug>` | `memoryctl revive` (`cmd/memoryctl/retire.go`) | `internal/sidecar` reproject (cancels retirement) | |
| `candidate-confirmed` | `<qslug>` | `memoryctl close` — `internal/closer` | `internal/sidecar` reproject (candidate→active); `internal/doctor` | emitted once per fact (WAL dedup); graduation of a soft-corroboration candidate |

---

## Registry — Legacy (no longer emitted, v2-tolerated)

Current code does not emit these, but historical or migrated WALs carry them
and v2 readers still tolerate them (aggregating or skipping). Do not emit them
from new code.

| event | target shape | consumed by (v2 reader) | was produced by |
|---|---|---|---|
| `inject-agg` | `<slug>` (`$3`), aggregated count (`$4`) | `internal/sidecar` reproject (adds to ref_count); `internal/ab` | `hooks/wal-compact.sh` |
| `outcome-positive` | `<slug>` | `internal/sidecar` (raw `outcome` table only — ignored for `effectiveness` since v2.14) | `hooks/memory-outcome.sh` |
| `outcome-negative` | `<slug>` | `internal/sidecar` (raw `outcome` table only — ignored for `effectiveness` since v2.14) | `hooks/memory-outcome.sh` |
| `trigger-useful` | `<slug>` | none — ignored by every usefulness reader (sidecar effectiveness, `ab`, `doctor` candidates); `internal/doctor` `open_quanta_last_30d` still tolerates it as a closing event so pre-cutover sessions don't read as open. Dropped by `wal compact` | `memoryctl close` — `internal/closer` (`Classify`), v2.0–v2.13; ignored since v2.14 |
| `trigger-silent` | `<slug>` | same as `trigger-useful` — ignored by usefulness readers, tolerated only by `open_quanta_last_30d`. Dropped by `wal compact` | `memoryctl close` — `internal/closer` (`Classify`), v2.0–v2.13; ignored since v2.14 |
| `trigger-silent-retro` | `<slug>` | `internal/doctor` `open_quanta_last_30d` (tolerated as a closing event); ignored by usefulness readers, same as `trigger-silent`. Dropped by `wal compact` | `hooks/wal-retro-silent.sh` |
| `session-metrics` | v2: `domains:_global_,error_count:N,tool_calls:M,duration:Ss` (`$3`), session id (`$4`); v1: `<domains_csv>` (`$3`), `error_count:N,...` (`$4`, no session id) | none. Dropped by `wal compact` | `memoryctl close` — `internal/closer`, one per parent `Stop`, through v2.17 (read only by the removed self-profile); v1 `hooks/session-stop.sh` |
| `ablate-start` | `<qslug>:<sessions>` | none — `internal/sidecar` reproject skips it explicitly | `memoryctl ablate`, v2.11–v2.17 (per-fact holdout, removed) |
| `ablate-stop` | `<qslug>:<manual\|expired>` | none — skipped like `ablate-start` | `memoryctl ablate stop` / `memoryctl inject` on budget exhaustion, v2.11–v2.17 |
| `holdout-skip` | `<qslug>` | none — skipped like `ablate-start` | `memoryctl inject`, v2.11–v2.17: a held-out fact ranked top-K but was withheld |
| `holdout-hit` / `holdout-miss` | `<qslug>` | none — skipped like `ablate-start` | `memoryctl close`, v2.11–v2.17: `evidence:` phrase present / absent in a session that withheld the fact |
| `clean-session` | `<domain>` or `_global_` | `internal/doctor` `open_quanta_last_30d` (tolerated as a closing event) | `hooks/session-stop.sh` (zero errors) |
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

Also skipped as unknown since their v2 readers were removed: `outcome-new`,
`strategy-used`, `strategy-gap` (v1 hooks; read only by the removed
self-profile) and `dedup-blocked` / `dedup-merged` / `dedup-candidate`
(target `<new>><existing>` / `<new>~<existing>`; written through v2.17 by the
removed `memoryctl dedup check` — see git tag `v2.17.0`).

---

## Glossary of `target` sub-delimiters

The four-column invariant is immutable within WAL format v1; richer data is
encoded into the `target` column with sub-delimiters. Readers ignore unknown
structure, so adding a sub-delimiter is non-breaking — but introduce them
sparingly to keep the WAL greppable.

| sub-delim | meaning | active example events |
|---|---|---|
| `,` | rows of equal weight (CSV-like) | `session-metrics` (inner, legacy) |
| `:` | `key:value` (single pair or namespace prefix) | `retire` (`:<reason>`); `session-metrics` (inner, legacy) |
| `>` | directed pair (`from > to`, asymmetric) | `retire` (`><successor>`); `dedup-blocked`, `dedup-merged` (retired) |
| `~` | soft / similarity pair (symmetric) | `dedup-candidate` (retired) |

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
   ranking metadata (ref_count/effectiveness/recency); `internal/doctor` if it
   is a health signal. An event with no reader is not worth writing.
5. **Update `CHANGELOG.md`**, citing the row added here.

When retiring an event: flip its status to `Legacy` (still tolerated by readers)
or `Retired` (skipped), stop emitting in code, and keep the row — historical
WALs carry it for years.

## Compaction

`memoryctl wal compact [--dry-run]` rewrites the WAL under its lock without
the rows of `wal.LegacyEvents` — `trigger-useful`, `trigger-silent`,
`trigger-silent-retro`, `session-metrics` — and keeps every other row,
including unknown and malformed ones, byte-for-byte and in order. The
original stays as `.wal.pre-compact-<date>` (a same-day rerun replaces it);
the sidecar is then re-projected so its recorded WAL offset matches the new
file. `--dry-run` prints the same counts and writes nothing. Run it when no
session is mid-turn: an append that times out on the lock writes without it
and could land in the backup instead of the new WAL. Exit codes: `0` done (or
nothing to drop), `1` failed (WAL unchanged, or — if only the re-projection
failed — run `memoryctl sidecar rebuild`), `2` misuse or no WAL.
