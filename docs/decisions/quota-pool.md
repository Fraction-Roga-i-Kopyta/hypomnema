---
type: decision
project: global
created: 2026-04-24
status: active
description: Adaptive quota pool of 12 slots shared across feedback/knowledge/strategies/decisions/notes, shrinking to 10 or 8 when keyword signal is strong. Per-type mistake cap stays at 3+3. This ADR records the thresholds and the reason they sit where they sit.
keywords: [quotas, pool, keywords, signal]
domains: [ranking]
related: [scoring-weights, cold-start-scoring]
review-triggers:
  - metric: ambient_fraction
    operator: ">"
    threshold: 0.75
    source: self-profile
  - after: "2027-04-24"
---

## What

SessionStart applies two layers of cap on how many candidate records
it injects:

1. **Mistake slot:** hard cap of 3 global-scope mistakes + 3
   project-scope mistakes. Not part of the adaptive pool.
2. **Adaptive pool:** 12 / 10 / 8 slots shared across feedback,
   knowledge, strategies, decisions, and notes. The pool **shrinks**
   when keyword signal is strong:

```
keywords_derived  (from git branch + diff + commit + cwd + project domains)
   < 5        →  pool = 12
   ≥ 5, < 10  →  pool = 10
   ≥ 10       →  pool =  8
```

The rationale: when the git/keyword signal is strong the ranker's
top N files are the correct N. When it's weak, breadth of relevant
context matters more than depth, and the ranker should err toward
surfacing more candidate records.

## Why

1. **Mistakes get their own slot because they carry the strongest
   negative signal.** Not injecting an on-topic mistake that already
   matches by domain is the only kind of omission with a clear
   downstream cost (the agent repeats the mistake). 3+3 is enough
   for the overwhelming majority of sessions; in practice most
   sessions inject 0–2 mistakes after filtering.

2. **The other five types don't have individual caps** because
   their relative usefulness depends on the session. A web-frontend
   session might benefit from three knowledge files and one
   feedback rule; a backend-refactor session might flip that
   ratio. Hard per-type caps would starve whichever type happens
   to be most relevant today.

3. **The pool shrinks with keyword signal so strong-signal sessions
   don't drown in low-confidence noise.** On a session where ≥10
   keywords match, the top-10 by score are usually the real hits;
   adding two marginal candidates just pushes the most relevant
   ones further down the agent's attention and risks prompt
   truncation. When keyword signal is weak (<5 keywords resolved
   from git context), the ranker is guessing more, so showing the
   agent a wider candidate list is cheaper than being confidently
   wrong about the top 8.

4. **Thresholds (5 / 10) are round numbers from measurement.**
   Maintainer corpus over a 14-day window: sessions with ≥10
   keywords include precise bug-fix work (diff touches ~10 files
   with named subsystems). Sessions with <5 keywords are
   exploratory — "fix the login page" with a freshly-cut branch.
   The middle bucket was added when `doctor` started surfacing
   sessions that were neither clearly narrow nor clearly broad.

## Trade-off accepted

- **The shrink is stepwise, not continuous.** A session with
  exactly 9 keywords gets pool=10; a session with 10 gets pool=8.
  At the boundary the two extra slots disappear for a marginal
  increase in signal. Accepted because: (a) continuous scaling
  invites debate over the function shape, and (b) the bench
  penalty of computing a continuous function per-session is a
  real cost on 500+ file corpora.

- **`notes/` competes with `feedback/` for the same slots.** A
  long-form note and a short actionable feedback pay the same
  slot cost. On the author's corpus notes routinely lose to
  feedback because feedback files usually have explicit
  `triggers:` that the pipeline rewards. This is intended — if
  the note were more load-bearing than the feedback, its
  `triggers:` or body vocabulary would pull it up.

- **Adaptive quota is invisible to the user.** Someone asking
  "why didn't hypomnema surface my note?" sees the Injected
  section of the context block, not the quota math behind it.
  `memoryctl doctor` could report the active pool size per-session
  — deferred as a P1 polish item for v0.16 if the next audit asks
  for it.

## When to revisit

- `ambient_fraction > 0.75` on `self-profile` — if three quarters
  of injections end up being ambient rules (no session-specific
  signal), the adaptive pool has effectively degenerated to
  "inject the same ambient rules every session", and the shrink
  logic isn't doing any work. Revisit the thresholds or widen
  the pool.
- Calendar: `2027-04-24`. One year. Re-read and decide whether to
  close or extend.

Manual revisit if a cold corpus user reports "I add keywords but
hypomnema still surfaces the same breadth of rules" — that would
mean the shrink thresholds never fire in their git workflow.

## Implementation note

Pool sizes and keyword thresholds are in
`hooks/session-start.sh` around the adaptive-quota block. Any
change here should:

- Update `CLAUDE.md § Memory layout` which names the 12-slot pool.
- Run the fixture harness (`make test-fixtures`) since
  `synthetic-cold-start` has <5 keywords by design and
  `synthetic-mature` is above the 10-keyword threshold for its
  trigger prompt — both still need to land in their claimed
  pool sizes post-change.
- Add a `scoring-pool-change` entry to
  `docs/fixtures/real-corpus-notes.md` if the change would shift
  which files real corpora inject.
