---
type: decision
project: global
created: 2026-05-08
status: proposed
description: When silent-applied / trigger-useful exceeds 1.0 sustained over a 30-day window, hypomnema's rules are internalised faster than they are explicitly cited; this is an observational milestone, not a release.
keywords: [intuition, milestone, silent-applied, trigger-useful, ratio, internalised]
domains: [scoring, observability]
related: [precision-class-ambient, two-scoring-pipelines]
---

## Status history

- 2026-05-08 — proposed. No metric implementation in self-profile yet
  (the ratio is not currently reported); ADR is written first per the
  project's ADR-first convention. Implementation tracked in v1.1
  cycle.

## What

A new ADR-tracked **observational milestone**: when the corpus-wide
ratio

```
silent_applied_30d / trigger_useful_30d
```

sustains above `1.0` for a continuous 30-day window, hypomnema is
declared to have crossed the **intuition tier** — rules are being
applied silently more often than they are being cited explicitly.

This is **not** a release. There is no version bump, no schema
change, no code path that activates. The crossing is a measurement
event:

1. `memoryctl self-profile` adds a section `## Intuition signal`
   showing current ratio + 30d trend (and a one-line interpretation:
   "rules referenced more than applied" / "applied more than
   referenced" / "balanced").
2. `memoryctl decisions review` reports
   `intuition-milestone: reached` — a **positive** event, distinct
   from `pressure` / `overdue` which signal degradation. Existing
   review-trigger schema is extended with a `direction: above|below`
   modifier so milestones (above-threshold-is-good) and pressure
   triggers (above-threshold-is-bad) coexist without confusing the
   reporter.
3. On crossing, the maintainer writes a dated note in
   `docs/measurements/<YYYY-MM-DD>-intuition-reached.md` with the
   30-day distribution and a snapshot of the corpus state. That
   note is the artifact; the ADR itself remains active.

## Why

1. **Resists the urge to ship a release that doesn't exist.** The
   original concept-roadmap promised v1.0 «Интуиция» as the apex
   of the system. Reality showed that the apex is not a release —
   it is a state. Tagging emergent behaviour as a code release was
   the wrong abstraction; tagging its **measurement** is the right
   one.

2. **Closes a gap in the precision narrative.** `measurable
   precision` already counts `silent-applied` in the numerator —
   that is the discovery that v0.7-B made (rules applied without
   citation are still useful, not noise). But precision tops out at
   100% well before silent-applied surpasses trigger-useful, so the
   metric does not differentiate "rules are followed" from "rules
   are second nature". The ratio does.

3. **Makes the observational tier disambiguable.** Without this
   ADR, an outsider reading `notes/memory-system-roadmap.md` would
   see v1.0 «Интуиция» described as poetry. The promote here gives
   v1.0 a concrete, falsifiable definition: when the ratio crosses,
   the milestone is reached; when it does not, the system has not
   reached intuition tier — regardless of how mature the corpus
   feels.

4. **Cheap by design.** The ratio's components (silent_applied,
   trigger_useful) are already tallied in self-profile every
   session-stop. This ADR adds one division, one section in the
   markdown render, and one new event type for `decisions review`.
   No data layer changes.

## Trade-off accepted

- **Ratio = 1.0 is a soft boundary.** A 30-day window can hover at
  0.99 / 1.01 / 0.97 across consecutive samples without signalling
  anything meaningful. Mitigated by requiring **sustained** crossing
  (≥7 consecutive daily snapshots above 1.0) before the milestone
  fires. Once fired, it remains in the dated note even if the ratio
  later falls back below 1.0 — the crossing happened.
- **Single-install measurement.** Like every metric in this
  project, the ratio is per-corpus. A maintainer with a tiny
  trigger-citation habit will cross sooner than a maintainer who
  cites everything. That is fine: the milestone is per-author,
  per-corpus. Cross-install comparison is out of scope (matches
  the threat model boundary).
- **Positive-event reporting requires schema change.** The current
  `decisions review` only knows `[ok | pressure | overdue]`. Adding
  `reached` as a fourth state means every consumer (self-profile
  renderer, doctor) needs an update. Accepted because the
  alternative — overloading `pressure` for positive crossings —
  inverts semantics and is worse.

## When to revisit

This is the metric being watched, so the calendar trigger only:

- Calendar 2027-05-08 — twelve months from proposed date. By then
  either:
  - The ratio has crossed (write the dated note, leave ADR active
    as documentation of the milestone) — **OR**
  - The ratio has not crossed but is trending up (extend, no
    action) — **OR**
  - The ratio is structurally below 1.0 with no trend (re-examine:
    is `silent-applied` underreported? Is `trigger-useful`
    over-reported by mechanical citation? Calibrate or retire the
    milestone).

A manual revisit is warranted if the ratio crosses **above 2.0**
(rules applied twice as often as cited). At that point,
trigger-useful events may be too rare to ground a precision metric;
investigate whether `precision-class-ambient` should expand to more
slug types.

## Origin

Aspirational v1.0 «Интуиция» tier from the original
concept-roadmap was reframed in the 2026-05-07 rewrite of
`notes/memory-system-roadmap.md` as poetry. On 2026-05-08, the
maintainer promoted it to a concrete observational milestone after
discussion: the apex is not a release, it is a measurable state,
and the system is mature enough to give it a falsifiable
definition.

The `silent_applied / trigger_useful` ratio was the natural
candidate because both numerator and denominator were already
defined and counted by `self-profile.md` (introduced in v0.8.1
when `precision_class: ambient` separated noise from genuine
silent application). The choice of `> 1.0` as the threshold is
deliberate: anything less is a soft preference; the crossing point
where silent application overtakes explicit citation is the point
at which "rules" stop being separate things and become how the
agent thinks.

## Implementation pointer (v1.1)

This ADR is **proposed**, not active, until the underlying metric
is reported. Implementation work, in order:

1. `internal/profile/profile.go` — count `silent_applied_30d` and
   `trigger_useful_30d` (windowed by `HYPOMNEMA_NOW`-relative
   filter), expose on `walSignals`.
2. `renderProfile` — append `## Intuition signal` section with
   ratio, 30d trend (compare against prior 30d), one-line
   interpretation.
3. `internal/decisions/review.go` (or equivalent) — extend the
   trigger schema with `direction: above|below` and emit
   `reached` state when a milestone with `direction: above` and
   sustained crossing condition is satisfied.
4. ADR `status: proposed` → `status: active` once the above three
   lands.
5. Add `review-triggers:` block to this file at activation time:
   ```
   review-triggers:
     - metric: silent_applied_to_useful_ratio_30d
       operator: ">"
       direction: above
       threshold: 1.0
       sustained_days: 7
       source: self-profile
     - after: "2027-05-08"
   ```
