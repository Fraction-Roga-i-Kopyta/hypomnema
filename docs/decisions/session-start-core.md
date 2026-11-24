---
type: decision
name: session-start-core
created: 2026-11-24
status: active
description: SessionStart delivers a fixed core (freshest continuity, pinned, project feedback) instead of a ranking, after compaction too; SubagentStart gates on the Agent call's description; effectiveness and ref_count leave the score; project feedback never decays.
keywords: [session-start, core, subagent, gate, ranking, effectiveness, ref-count, compact, decay]
domains: [ranking, injection]
related: [native-primary-sidecar, scoring-weights, quota-pool, precision-class-ambient]
---

## What

Five coupled choices, shipped together (spec
`docs/superpowers/specs/2026-11-24-core-memory-design.md`):

1. **SessionStart = a fixed core, not a ranking.** Every source (`startup`,
   `resume`, `clear`, `compact`) injects, within the 8 KB budget: the
   project's freshest `continuity`; `status: pinned` facts, project-local
   then global; project-local `type: feedback`. No ranked fill. Stale,
   retired, hidden and *less*-marked facts are not core. Bodies that do
   not fit become one-line pointers (not deliveries); the Memory-queue
   line's bytes are reserved first. Invariant I6.
2. **Compact = core only.** After compaction nothing is ranked in behind
   the core; the summary carries the task state, the rest is pulled.
3. **SubagentStart gates on the Agent call's `description`.** A fact
   injects only when its head (`name`, `description`, `keywords`,
   `domains`) holds ≥ 2 distinct description terms, one of them in `name`
   or `keywords`. Paths, `$HOME`/project-root path words, agent-type
   words, digit-only tokens and corpus-common head terms (in
   ≥ `max(5, ⌈10 % × N⌉)` heads) never count. Gate first, then rank to
   top-5. No Agent call of its own → nothing (no fallback to the parent's
   prompts). Continuity is not special-cased.
4. **Score = `3.0 × overlap + 2.0 × recency + 1.0 if project-local`.**
   `effectiveness` and `ref_count` leave the ranker; *less* still halves
   the score, *hide* still drops the fact. They stay computed as
   diagnostics (`/memboard`, `stats`, `doctor`, `sidecar show`).
5. **Project-local feedback never decays**, like pinned facts and each
   project's freshest continuity; one close restores one staled earlier.

## Why

- **Ranked push missed.** WAL 2026-11-09…24 (v3.1.0): parent sessions
  recorded 30 `cite-useful` against 975 `cite-silent` (3 %) at 42
  injections per session. The model's own account of a session: the
  continuity, the pinned card and how-to-check facts helped; facts pulled
  in by cwd/git overlap got in the way.
- **Subagents got the parent's state.** 1389 of 1419 subagents in the
  2026-11 profile received exactly one fact — the parent's continuity;
  subagent `cite-useful` was 29 of 1713.
- **Whole-text lexical gating selects nothing.** On 362 real briefs
  (2026-11-17…24) the full Agent call (2.5–5.5 KB prompt) passed a median
  of 136 facts (p90 256) even with paths stripped and a name/keyword hit
  required; description + first 500 prompt characters, median 23. The
  description alone: median 1, p90 4, 36 % of subagents get nothing. On
  21 real compaction summaries (~4.8 KB focus text) the same gate passed
  a median of 187 facts (p90 258) of ~413.
- **Effectiveness and ref_count are circular rank inputs.** Injection
  volume feeds `ref_count`; `cite-silent`, a consequence of being
  injected, feeds `effectiveness` — which then measures citation
  discipline, not usefulness. A pinned card shapes every turn and is
  almost never quoted.
- **Core facts are refreshed only by being core.** Decay counts from last
  injection; a project feedback fact that decayed out of the core would
  never be injected again to refresh it.

## Trade-off accepted

- Facts outside the core are not pushed at SessionStart or after
  compaction; the agent reads them from the `MEMORY.md` index or `recall`.
- 36 % of subagents get no memory, and a fact relevant to the prompt body
  but not to the 3–5 word label is missed — the parent passes it inline.
- Genuinely obsolete project feedback never ages out; the user retires it
  or hides it in `/memboard`.
- `memoryctl ab` replay now measures recency of usefulness only.

## When to revisit

The re-measurement planned for ~2026-12-08 (parent sessions with a
citation, useful/silent ratio, injections per session, share of subagents
with zero vs task-matched facts) shows the citation share dropping against
the 2026-11-24 baseline, or users reporting that a compacted session lost
facts it needed.
