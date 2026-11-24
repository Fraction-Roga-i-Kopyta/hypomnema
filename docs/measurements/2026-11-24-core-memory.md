# Core memory — what push injection delivered, and what a gate would pass

Date: 2026-11-24. Source: the WAL and session transcripts of one live
install (the author's), read-only. No replay, no synthetic corpus: every
number below counts events that happened in real sessions.

The decision these numbers feed is `docs/decisions/session-start-core.md`
(SessionStart = fixed core, task-gated SubagentStart, score without
effectiveness / ref_count). This file holds the figures; the reasoning and
trade-offs live in the ADR.

## 1. Parent sessions: how much of the push was used

A fact counts as used when the session cites it (`cite-useful`); a fact
injected into a session that cited something else is `cite-silent`.
Sessions with no resolvable citation at all write `cite-none` and are left
out of the useful/silent columns.

| Window | Release | Parent sessions with a citation | `cite-useful` | `cite-silent` | Useful share |
|---|---|---|---|---|---|
| 2026-10-17 … 11-08 | 2.16 – 2.17 | 13 of 46 | 57 | 469 | 11 % |
| 2026-11-09 … 11-24 | 3.1.0 | 11 of 23 | 30 | 975 | 3 % |

In the 3.1.0 window a parent session received **42 injections** on average;
the largest single session took in **259 distinct facts**. Per parent
session, silent facts rose from about 10 to about 42 while useful ones
stayed at about 1.3: the extra volume added nothing that got used.

Read with care: the useful share depends on citation discipline as much as
on relevance, and the 3.1.0 window is short (23 sessions).

## 2. Subagents: what the channel actually carried

Profile of SubagentStart over 2026-10-12 … 11-08, keyed by
`<session>:<agent>`:

| | Count |
|---|---|
| Subagents | 1713 |
| … that received exactly one fact | 1419 |
| … of those, the one fact was the parent's continuity | 1389 |
| `cite-useful` from subagents | 29 |

In practice the subagent channel was a continuity relay: almost every
subagent got the parent's "where we left off" and nothing about its own
task.

## 3. Calibrating a subagent gate

Before gating SubagentStart on the task, three candidate inputs were run
through the same head gate (≥ 2 distinct terms in a fact's head, one of them
in `name` or `keywords`; paths and digit-only tokens stripped) over the
real Agent briefs of 2026-11-17 … 11-24 — **362 briefs** — against the
store as it was.

| Gate input | Facts passing, median | p90 | Subagents with zero |
|---|---|---|---|
| Whole brief (description + 2.5 – 5.5 KB prompt) | 136 | 256 | — |
| Description + first 500 prompt characters | 23 | 43 | — |
| Description only | 1 | 4 | 36 % |

Long free text shares two head terms with a large part of any store, so a
lexical gate over the whole brief selects almost nothing out. Only the
short task label (the Agent call's `description`) is selective — at the
price of 36 % of subagents getting no memory.

## 4. Calibrating a gate after compaction

The same gate over **21 real compaction summaries** (focus text about
4.8 KB each), against a store of about **413** facts: median **187** facts
pass, p90 **258**. A summary is as unselective as a whole brief; ranking a
fill behind the core after `compact` would mostly re-inject noise.

## Caveats

- One install, one user, one working style; the store size (≈ 400 facts)
  and the share of long briefs drive sections 3 and 4.
- `cite-silent` only exists in sessions that cited something, so section 1
  undercounts silence in sessions that cited nothing.
- The calibration counts what *passes* a gate, not what would have been
  used; it bounds noise, not recall.

## Follow-up

The ADR plans a re-measurement around 2026-12-08 on the same four signals:
parent sessions with a citation, useful/silent ratio, injections per
session, and the share of subagents receiving zero vs task-matched facts.
