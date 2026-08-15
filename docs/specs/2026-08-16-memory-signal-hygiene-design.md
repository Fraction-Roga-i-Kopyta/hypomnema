# Memory signal hygiene: metadata.* frontmatter, recency from last-useful, size hygiene, WAL write-amplification

**Status:** approved design → run A of two (run B = P3b applied-marker + IDF + P3c, own spec)
**Target:** v2.12.0
**Date:** 2026-08-16

## Problem

A live audit of the install on 2026-08-16 (sidecar 325 rows, WAL 106 742 lines,
30-day window) found four defects that together explain why injected memory
rarely helps the model. Numbers below are from that audit.

1. **Schema drift — 168 of 325 facts (52%) are typeless for hypomnema.**
   Claude Code's native memory instructions now tell the model to write
   `metadata:\n  type: …` (nested). `native.splitFrontmatter`
   (`internal/native/native.go:157`) deliberately skips every indented line
   ("top-level keys are always at column 0", review G2), so `type`, `status`,
   `created` nested under `metadata:` are dropped. In this project's scope
   7/105 files are affected (doctor `untyped:7`); in `headoffice-portal` 17/18,
   `work-jira` 81/97, `vulpio-jira-admin` 20/28. Consequences: `candidate`
   projects as `active` (skips corroboration), `continuity`/`project` lose their
   decay exemption, `created` falls back to WAL-earliest or empty (recency 0
   for a brand-new fact), self-profile misses mistakes/strategies, decay uses
   the unknown-type threshold.

2. **Recency is a self-reinforcing loop.** `rank.recency`
   (`internal/rank/rank.go:132`) decays from `LastInjected` ("reflects actual
   use" — but injection is the ranker's own output, not the model's). A fact
   the ranker picks gets a fresh recency and is picked again. Live evidence:
   `parallel-worktree-subagent-migration` ref 619 / eff 0.04, `design-system-
   migration-claude-ai-handoff` ref 460 / eff 0.01, `atlassian-keycloak-saml-
   sso` ref 455 / eff 0.02 — all `active`, all still injecting. 35 active
   facts have ref ≥ 50 and eff < 0.3; 157 of 289 active facts have eff < 0.3.
   Honest per-(slug, session) usefulness over 30 days: 22 of 1033 pairs (2.1%).

3. **Oversized facts inject as headers.** 64 of 107 files in scope exceed the
   2 500 B body cap; `hypomnema.md` (project fact) is 37 KB — a release
   journal — and `avatar-unified-analysis.md` (global) is 106 KB of
   project-specific content. Truncation shows `…(truncated)` with no hint how
   to get the rest, and doctor is silent about size.

4. **WAL write-amplification.** `close` runs on every Stop (per turn) and
   re-emits `trigger-useful/silent` (and `holdout-hit/miss`) for the whole
   injected set each time — one session wrote 16 341 `trigger-silent` rows.
   Every reader already dedups per (slug, session) (`reproject.classify`,
   `profile`, `ablate report`, `promote`), so the extra rows carry zero
   information and only inflate the WAL and every full replay.

## Invariants preserved

- I3 atomic writes: no new file writers outside `pathutil.WriteFileAtomic`/WAL append.
- I5 hook fail-safe: `close`/`inject` still exit 0 on any internal error.
- WAL is the source of truth; the sidecar stays a rebuildable projection
  (`memoryctl sidecar rebuild` reproduces every new column from WAL + frontmatter).
- No hook mutates native content. Content-ops in this run are explicit
  `memoryctl retire`/manual edits by the operator, all reversible (`revive`,
  `.archive/`).
- Existing WAL grammar (four columns) unchanged; no new event kinds.
- Zero-safe ranking: a new fact (no history) still gets neutral eff and
  frontmatter `created` as recency.

## Feature 1 — `metadata.*` frontmatter promotion

**Parser.** In `splitFrontmatter`, an indented `key: value` line directly
under a top-level `metadata:` key with an empty value is promoted to a
top-level key. Rules:

- Only `metadata` is a promotion parent (the harness nests only there). Any
  other empty-valued key followed by indented `k: v` lines keeps today's
  behaviour (lines skipped).
- Top-level wins on collision: `type:` at column 0 beats `metadata.type`.
- Block scalars are unaffected: their parent value is `|`/`>` (non-empty), so
  the G2 protection still holds.
- Block-style lists under a promoted key (`metadata:\n  keywords:\n    - a`)
  are collapsed the same way as top-level block lists.
- Promotion applies to every consumer of `native.Parse` — sidecar reproject,
  inject, close, doctor, memindex, guard, migrate.

**Types.** Harness type names (`user`, `reference`) are kept verbatim (types
are open strings already; doctor's `corpus_counts` lists whatever it finds).
`sidecar.staleDays` gains `user: 180`, `reference: 90`.

**Doctor.** No new check — `corpus_counts` untyped drops as the visible
effect. `doctor` detail for `corpus_frontmatter_quality` gains
`nested_metadata_count` in `extra` for observability.

**Docs.** CLAUDE.md "Tolerated syntax variants" + README frontmatter section:
`metadata:`-nested `type/created/status/keywords/domains` are read as if
top-level; top-level preferred. Global `~/.claude/CLAUDE.md` memory section:
one line that both shapes work.

## Feature 2 — recency from last useful citation

**Sidecar v6.** `memory` gains `last_useful TEXT` (YYYY-MM-DD, may be "").
`schemaVersion = "6"` → existing sidecar recreated on open (existing
mechanism). `Record.LastUseful` added; `upsertIn` writes it.

**Reproject.** `agg.lastUseful` = max date over `trigger-useful` and `recall`
events for the fact (qualified + legacy bare key, same merge as `lastInject`).
Rationale for `recall`: a pull by the agent is a deliberate use, and it is
already what revives a stale fact. `candidate-confirmed` is implied by the
`trigger-useful` that triggers it. `holdout-hit/miss` never count (unchanged
principle: no signal from sessions where the model did not see the fact).

**Ranker.** `rank.Candidate.LastUseful` added; `recency()` uses
`LastUseful`, fallback `Created`; `LastInjected` is no longer read by
`score`. Doc comment rewritten: injection is the ranker's output and cannot
be its recency input. Weights unchanged (`wRecency` 2.0, 30-day scale).

**Callers.** `inject` (`internal/inject/inject.go:233`), `rank` verb
(`cmd/memoryctl/rank.go:90`), `ab` replay (`internal/ab/replay.go:47` +
`SignalsBefore` gains `LastUseful` from `trigger-useful`/`recall` events
before the session date) all populate `LastUseful`. `rank` verb prints
`useful=<date|->` per row; its usage text is corrected to
`memoryctl rank --query "<words>" [--project P] [--k N]`.

**MarkStale is unchanged** (age from `last_injected`, fallback `created`).
Reason: the chain "ranker demotes → injections stop → last_injected ages →
stale" already retires the popularity loop without staling anything by fiat.
Simulation on the live sidecar: switching MarkStale to last-useful would
stale 46/270 immediately; the current rule stales 0 — the difference is
exactly the set the ranker should demote first, not archive.

**Evidence.** `memoryctl ab` before and after (same WAL, `HYPOMNEMA_TODAY`
pinned) → `docs/measurements/2026-08-16-v2.12-recency-basis.md`. Live
`memoryctl rank --query …` breakdown for three prompts (this session's
Russian question, a git-flavoured prompt, an empty SessionStart) before/after
as qualitative evidence in the same doc.

## Feature 3 — size hygiene

**Doctor `oversized_facts`.** New check: files in scope (current project +
global) whose body exceeds `inject.MaxBodyBytes`. WARN with count and the
five largest (`size slug`), OK when none. Suggests `recall` for full text
and "split or retire" as the fix.

**Truncation marker.** `inject.capBody` emits
`…(truncated: <shown>/<total> B — full text: memoryctl recall <name>)`
instead of the bare `…(truncated)`; the marker's own bytes stay inside the
per-body cap so the 8 KB total budget is unaffected. Same helper serves
`recall`'s top-body cap.

**Content-ops (operator, after `make install`; approved 2026-08-16):**

| File | Store | Op |
|---|---|---|
| `hypomnema.md` (37 KB) | hypomnema | rewrite as ≤2.5 KB project fact (repo, version, open items, conventions); old body → `hypomnema-journal-2026.md` (`type: note`) then `memoryctl retire hypomnema-journal-2026 --reason "release journal; history lives in CHANGELOG/git" --superseded-by hypomnema` |
| `hypomnema-external-review-2026-05-08.md` (29 KB) | hypomnema | `retire --reason "historical v1.0 review; superseded by v2.x"` |
| `hypomnema-external-review-response-2026-05-08.md` (12 KB) | hypomnema | same |
| `hypomnema-future-plan-2026-05-08.md` (7.6 KB) | hypomnema | `retire --reason "v1.1 plan; superseded by v2.x roadmap"` |
| `avatar-unified-analysis.md` (106 KB) | global → akasha | `mv` into `~/.claude/projects/-Users-akamash-Development-akasha/memory/` (project content misfiled in global); doctor in akasha will flag size |
| `assignee-backup-ticket-series-map` | global | `retire --reason never-corroborated` (promote recommendation) |
| `hidden-tab-canvas-never-paints` | global | same |

Then `memoryctl sidecar rebuild` + `memoryctl doctor` (expect `untyped:0`
in scope, `oversized_facts` WARN with a shorter list, `candidate_corroboration` OK).

## Feature 4 — WAL write-amplification

`closer.Run` passes a dedup key to `wal.Append` for the per-fact
classification rows: `|trigger-useful|<target>|<sid>`,
`|trigger-silent|<target>|<sid>`, `|holdout-hit|<target>|<sid>`,
`|holdout-miss|<target>|<sid>` (target = sanitised qualified slug). Effect:
one row per (event, fact, session). A fact silent on turn 1 and useful on
turn 5 yields one silent row and one useful row — `reproject.classify`
already resolves that as useful-wins, so no reader changes.
`session-metrics` and `session-close` stay per turn (doctor `open_quanta`
reads them). `wal.Append`'s dedup scan is tail-bounded (256 KiB); with the
amplification gone a session's rows fit comfortably in that window. Existing
WAL rows are not compacted in this run.

`docs/EVENTS.md`: the four rows note "written once per (fact, session)".

## Milestones

1. **M1 — metadata.* promotion** — parser + tests (nested type/status/created/
   keywords; top-level wins; block scalar untouched; block list under
   metadata), staleDays entries, doctor extra, docs. Verify on the live corpus:
   `doctor` untyped 7 → 0.
2. **M2 — recency from last_useful** — sidecar v6 column, reproject agg,
   Record/Candidate fields, rank/inject/ab callers, `rank` verb output + usage
   fix, docs formula lines (README, CLAUDE.md, ARCHITECTURE), ab measurement doc.
3. **M3 — size hygiene** — doctor `oversized_facts`, truncation marker
   (inject + recall), docs; content-ops executed after install (tracked in the
   plan as an operator checklist, not code).
4. **M4 — WAL dedup** — closer dedup keys + tests asserting one row per
   (event, fact, session) across two `Run` calls; EVENTS.md.

Release: `feature/memory-signal-hygiene` → PR → CI (macOS + Linux, lint,
shellcheck) → merge → `v2.12.0` → `make install` → content-ops → rebuild →
doctor → update memory (`hypomnema.md` project fact reflects v2.12.0).

## Out of scope (→ run B, own spec)

- Overlap length bias: a 106 KB body matches nearly any prompt (`OverlapScores`
  counts distinct query terms anywhere in name+description+keywords+body). Fix
  is IDF or field weighting — a ranker re-tune that needs the P3c A/B.
- P3b variant C: explicit `[[applied:<slug>]]` marker as the strong positive,
  evidence/name as weak, silent as neutral; negative = "injected many times,
  never applied". Protocol change (global CLAUDE.md) + classifier + effectiveness.
- P3c: re-A/B with real baselines (top-K by ref_count / recency / native index).
- WAL compaction of the 106 K historical rows.
- MarkStale basis change (explicitly kept on last_injected, see Feature 2).
