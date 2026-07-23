# Harness lifecycle: retire/tombstones, candidate corroboration, per-fact ablation, promotion ladder

Status: approved design, pre-implementation
Date: 2026-07-23
Origin: ideas adapted from lopopolo/harness-engineering (CC BY 4.0) — "turn
feedback into infrastructure", "test a curated knowledge base in a fresh
session", corroborate agent self-reports, tombstone retired context.

## Problem

v2 measures whether injected facts get used (trigger-useful/silent →
Bayesian effectiveness) and down-ranks stale facts, but the lifecycle has
four gaps:

1. **No archiving.** Stale facts stop injecting but stay in the store
   forever; CLAUDE.md says "archiving is planned but not shipped". There is
   no way to retire a fact with a recorded reason and successor.
2. **Uncorroborated self-reports.** An agent-written mistake/strategy file
   becomes ranked context immediately and permanently. Nothing distinguishes
   a lesson that later proved real from one that never triggered again.
3. **No ablation.** Effectiveness observes usage, not causation. There is no
   way to test "does this fact still change behavior?" by withholding it —
   a fact whose rule the model now applies unprompted is pure injection-budget
   waste, and nothing detects that.
4. **No promotion path.** A mistake that recurs despite being injected is
   evidence that prose memory is the wrong owner (should be a hook/lint/test);
   a fact useful in every session belongs in CLAUDE.md, not in a ranked
   rotation. Nothing surfaces these.

Together these form one lifecycle:

```
write (candidate) → corroborate (active) → serve → ablate-check →
    promote to durable owner | retire with tombstone
```

## Invariants preserved

- WAL stays 4-column append-only source of truth; sidecar stays a
  rebuildable projection (all new state derives from WAL events).
- Hooks never mutate native content files. Frontmatter `status:` remains
  author-written; lifecycle transitions live in the sidecar (same pattern as
  `stale`). File moves happen only in explicit CLI verbs (precedent:
  `dedup --merge`).
- Ranker weights, injection budget (top-8 / 8KB / once-per-session), guard,
  and WAL format v1 are untouched.
- Zero-safe injection: a brand-new fact is injectable from day one.
- New WAL events are registered in `docs/EVENTS.md` in the same commit
  (authoring rules there apply: kebab-case, target sub-delimiters from the
  existing glossary, reader wired in the same change).

## New WAL events

| event | target shape | producer | consumer |
|---|---|---|---|
| `retire` | `<slug>[><successor-slug>]` | `memoryctl retire` | sidecar reproject (status=retired), recall, doctor |
| `revive` | `<slug>` | `memoryctl revive` | sidecar reproject (status=active) |
| `candidate-confirmed` | `<slug>` | `memoryctl close` | sidecar reproject (candidate→active) |
| `holdout-skip` | `<slug>` | `memoryctl inject` | ablate report (fact would have entered top-8; skipped) |
| `holdout-hit` | `<slug>` | `memoryctl close` | ablate report (evidence present WITHOUT injection) |
| `holdout-miss` | `<slug>` | `memoryctl close` | ablate report (evidence absent without injection) |
| `ablate-start` | `<slug>:<sessions>` | `memoryctl ablate` | sidecar reproject (holdout on) |
| `ablate-stop` | `<slug>:<reason>` (`expired`\|`manual`) | inject (expiry) / `memoryctl ablate stop` | sidecar reproject (holdout off) |

The retire reason is free text: sanitized via `wal.SanitizeField` and
appended after the directed pair with the `:` sub-delimiter
(`<slug>><successor>:reason-text`), or `<slug>:reason-text` with no
successor. Readers must tolerate a missing reason and missing successor.

## Feature 1 — `memoryctl retire` / `revive` (tombstones + archive)

**CLI.**
```
memoryctl retire <slug> [--reason "..."] [--superseded-by <slug-or-freetext>]
memoryctl revive <slug>
```

**Mechanics.**
- Resolve slug in the current project store or global store (same resolution
  order as recall). Ambiguity across stores → error listing candidates.
- Move the file to `<store>/.archive/<original-name>.md`. Dot-directory
  inside the store; `reindex`, injection scanning, and dedup ignore it.
  Collision in `.archive/` → suffix `-2`, `-3`.
- Emit `retire` WAL event; sidecar status → `retired` (keeps row, keeps
  history). Reproject derives `retired` from the WAL event, not from file
  absence — a rebuilt sidecar on a machine where the file was hand-deleted
  still knows the fact was deliberately retired, not lost.
- Regenerate MEMORY.md (drop the index line).
- `revive` moves the file back, emits `revive`, status → `active`.

**Tombstone surface.** `recall` continues to scan `.archive/` and shows
retired matches as:
```
[retired 2026-07-23 → <successor>] <name> — <description> (reason: ...)
```
with the body available on explicit Read. A retired fact never re-enters
push injection unless revived. Recalling a retired fact does NOT revive it
(unlike stale) — revival is an explicit verb because retirement was an
explicit decision.

**Doctor.** Existing stale-fact reporting gains a hint: "stale N days —
consider `memoryctl retire <slug>`".

## Feature 2 — candidate corroboration (soft path)

**Frontmatter.** `status: candidate` becomes a valid author-set value
(alongside `active`/`pinned`). Agent protocol change (CLAUDE.md of this repo
+ skills/memory-system.md + templates): agent-originated facts (mistake,
strategy, knowledge written from the agent's own observation) start as
`candidate`; user-mandated rules (feedback, decisions the user dictated)
start `active`/`pinned` as today. No enforcement at write time — the guard
does not check this; it is protocol, not gate.

**Ranking/injection.** Candidates rank and inject exactly like active facts
(zero-safe preserved). Status filter becomes `active|pinned|candidate`.

**Graduation.** In `memoryctl close`, when a candidate is classified
`trigger-useful` for the first time: emit `candidate-confirmed`; sidecar
status → `active`. Frontmatter is not rewritten (sidecar-managed transition,
like `stale`). Sidecar status takes precedence over frontmatter on
reproject: a file whose frontmatter still says `candidate` but whose WAL
carries `candidate-confirmed` projects as `active`.

**Failure to corroborate.** A candidate with ≥5 `trigger-silent` events and
zero `trigger-useful` is flagged by `doctor`:
"candidate never corroborated after N injections — retire or rewrite
keywords/evidence". Doctor-only; no auto-retire.

## Feature 3 — per-fact ablation (`memoryctl ablate`)

**CLI.**
```
memoryctl ablate <slug> [--sessions N]     # default N=5
memoryctl ablate stop <slug>
memoryctl ablate report [<slug>]
```

**Holdout mechanics.**
- `ablate-start` event; sidecar records holdout with remaining-session
  budget.
- `inject`: after ranking, if a held-out fact lands in the top-K result set,
  drop it from delivery, emit `holdout-skip`, and pull in the next-ranked
  fact so the budget is still filled. Decrement happens per session (first
  skip in a session counts the session), not per prompt.
- Holdout expires after N sessions **in which the fact would have been
  injected** (holdout-skip sessions), not wall-clock sessions — a fact that
  never ranks provides no ablation signal. `inject` emits
  `ablate-stop|<slug>:expired` on expiry.
- `close`: for each held-out fact with a `holdout-skip` this session, run
  the same evidence classification used for injected facts against the
  transcript: evidence present → `holdout-hit`, absent → `holdout-miss`.
  These do NOT feed Bayesian effectiveness (the fact was not injected;
  penalizing it for silence would corrupt the posterior).

**Report.** `ablate report <slug>`:
```
holdout: 5 sessions requested, 4 observed (would-have-injected)
behavior without injection: applied 3/4 (holdout-hit)
verdict hint: rule applied without memory in 75% of sessions —
  likely internalized; consider retire (or promote to CLAUDE.md if it
  must never regress)
```
Verdict is a hint with thresholds: hit-rate ≥ 0.75 → "likely internalized";
≤ 0.25 → "memory is doing work — keep"; between → "inconclusive, extend".
Report only; no state change.

## Feature 4 — promotion ladder (`memoryctl promote`, report-only)

Scans sidecar + WAL + frontmatter and prints promotion candidates:

| signal | suggestion |
|---|---|
| `type: mistake` with `recurrence ≥ 2` | prose failed twice — promote to mechanical owner: hook / lint / test |
| fact with `trigger-useful` in ≥ M consecutive sessions where injected (default M=5) | needed every time — promote to CLAUDE.md / always-on context |
| completed ablation with hit-rate ≥ 0.75 | already internalized — retire with tombstone |
| candidate flagged by doctor (≥5 silent, 0 useful) | never corroborated — retire or rewrite |

Each line names the fact, the evidence (counts, sessions), the suggested
owner class, and the closing command
(`memoryctl retire <slug> --superseded-by "<new owner>"`). `promote` never
mutates anything — the human builds the hook/lint/CLAUDE.md entry, then
closes the loop with `retire`. Exit 0 always (informational, cron-safe).

Overlap rule: a fact matching several signals prints once with the
highest-consequence suggestion (mechanical owner > CLAUDE.md > retire).

## Milestones

1. **M1 — retire/revive + tombstones.** New verbs, `.archive/` handling,
   `retire`/`revive` events + EVENTS.md rows, reproject `retired` status,
   recall tombstone rendering, reindex exclusion, doctor hint.
2. **M2 — candidate corroboration.** Status value, filter changes,
   `candidate-confirmed` in close, reproject precedence, doctor flag,
   CLAUDE.md/templates/skills protocol text.
3. **M3 — ablation.** `ablate` verb + sidecar holdout state, inject skip +
   `holdout-skip`, close classification (`holdout-hit`/`miss`), expiry,
   report with verdict hints, EVENTS.md rows.
4. **M4 — promote.** Report-only detector over signals from M1–M3 +
   existing recurrence/trigger data; doctor cross-reference.

Each milestone ships with table-driven tests beside the code (existing
convention), EVENTS.md updated in the same commit, and CHANGELOG entries.

## Out of scope

- Hard staging of candidates (excluded-from-injection until approval) —
  rejected in design review: breaks zero-safe, too much friction solo.
- Auto-rotation ablation (system-chosen holdouts) — rejected: silent
  deactivation of behavioral rules is riskier than the insight is worth.
- Auto-retire / auto-promote — everything consequential stays report-only
  with an explicit human verb.
- Ranker changes, per-type quotas, WAL format changes.
