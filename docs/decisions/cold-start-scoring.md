---
type: decision
project: global
created: 2026-04-24
status: proposed
description: Complex scoring components (Bayesian effectiveness, TF-IDF body match) stay dormant until the corpus accumulates enough outcome signal to make their weights meaningful, and the dormant state is visible to both user and agent.
keywords: [scoring, cold-start, bayesian, tfidf, dormant, calibration]
domains: [ranking]
related: [two-scoring-pipelines, precision-class-ambient]
review-triggers:
  - metric: corpus_fraction_with_active_bayesian
    operator: "<"
    threshold: 0.30
    source: self-profile
  - metric: bayesian_override_env_usage
    operator: ">"
    threshold: 0.50
    source: self-profile
  - after: "2026-10-24"
---

## What

Two of the five SessionStart scoring components (`wal_spaced_repetition`
via Bayesian effectiveness; `tfidf_body_match`) **degrade silently**
on small or young corpora:

- Bayesian eff uses `(pos + 1) / (pos + neg + 2)`. When `outcome-positive`
  and `outcome-negative` are both zero for a slug, eff is a constant
  0.5 — it neither rewards nor punishes, it just averages the rest of
  the formula toward the middle.
- TF-IDF requires a populated index. On a corpus whose authors diligently
  fill `keywords:` (the documented best practice), the current builder
  rule `if (has_kw) return` excludes them from the body index —
  leaving the component with nothing to match.

The effect on real corpora:

| Corpus | mistakes | outcome-positive events | `.tfidf-index` size | components contributing signal |
|---|---:|---:|---:|---:|
| author (akamash) | 29 | 40 (mistakes-only) | 257KB (legacy files only) | ~3/5 |
| external-01 (onboarding, feedback-heavy) | 0 | 0 | 0 bytes | ~2/5 |

On both corpora, `measurable precision` sits around 4% — not because
rules are bad, but because half the formula is averaging itself out.

This ADR does not remove the complex components. It gates them behind
a **minimum signal threshold** and makes the dormant state visible.

Decision:

1. **Bayesian eff.** When `outcome-*` events for the candidate slug
   over the last `OUTCOME_WINDOW_DAYS` (default 14) number fewer than
   `MIN_OUTCOME_SAMPLES` (default 5), Bayesian eff returns **1.0
   (neutral)**, not 0.5. Rationale: 0.5 systematically halves the
   rest of the formula; 1.0 lets keyword/project carry the signal
   until outcomes accumulate.
2. **TF-IDF body match.** When the index holds fewer than
   `MIN_TFIDF_VOCAB` unique terms (default 100), the component is
   suppressed entirely (returns 0, same cost as current runtime-gate
   on `fkw == "_none_"`).
3. **Visibility — mandatory, not optional.**
   - `memoryctl doctor` prints one line: `[INFO] scoring components active: 3/5 (Bayesian DORMANT — need ≥5 outcomes in 14d, have 0; TF-IDF DORMANT — vocab <100)`.
   - `_agent_context.md` includes a parallel note so subagents know the pipeline is in reduced mode.
   - `hooks/session-stop.sh` emits `scoring-mode|<active_count>|<dormant_list>` to WAL once per day (same spacing as WAL compaction).
4. **All thresholds ENV-overrideable.**
   `HYPOMNEMA_BAYESIAN_MIN_SAMPLES`, `HYPOMNEMA_OUTCOME_WINDOW_DAYS`,
   `HYPOMNEMA_TFIDF_MIN_VOCAB`. Documented in `CONFIGURATION.md §
   Scoring cold-start gates`.

## Why

1. **Overengineering on 30-100-file corpora.** The original five-
   component formula was calibrated against an imagined mature
   corpus (hundreds of files, hundreds of outcomes). First weeks of
   a real install look nothing like that — and complex components
   on an empty corpus are noise multiplied by a weight, not signal.
   Gating them at the edge of the pipeline is cheaper than tuning
   weights that have nothing to act on.
2. **Honesty about capability.** A user watching `measurable
   precision = 4%` has two incompatible hypotheses: *my rules are
   bad* or *the scoring formula is sleeping*. The dormant-visible
   state disambiguates: the doctor line and agent-context note
   tell them which it is. Without this, low precision looks like
   user failure — even when it's the algorithm waiting for data.
3. **Progressive disclosure as onboarding.** When the user
   accumulates five outcome-positive events, Bayesian eff
   activates — and the doctor line changes to `4/5`. That is a
   small but concrete milestone: the system is **learning** from
   your usage, not running a placeholder. This is the kind of
   feedback loop that matches the hypomnema philosophy (build up
   over time) rather than the opposite (hide behind opaque
   scoring).
4. **P1.1 is still needed, not replaced.** Cold-start gating
   handles *how the formula behaves while data is sparse*; P1.1
   (extending `outcome-positive` beyond `mistakes/`) handles
   *how data gets collected for feedback-heavy corpora*. Without
   P1.1, Bayesian stays dormant forever on a corpus that never
   writes mistakes — which is a legitimate user profile, not a
   defect.

## Trade-off accepted

- **Neutral Bayesian (1.0) overpromotes noise slugs.** A slug
  that is objectively useless gets Bayesian 1.0 just by having
  no data — same score as a useful slug with no data. Accepted
  because (a) the other four components still rank; (b) the
  alternative (staying on 0.5) was systematically halving every
  slug's contribution, not just noise slugs.
- **Extra check per scoring pass.** Sample-count lookup against
  WAL on every candidate. Measured cost: ~20µs per file, ~1ms
  on a 50-file session. Below the noise floor of SessionStart
  latency (~300ms).
- **ENV overrides leak into user mental model.** Power users will
  tune thresholds and report apparent precision numbers that
  other users can't reproduce. Accepted because the alternative
  (no overrides) leaves the system un-debuggable.
- **"Dormant" is not "disabled".** Component output is replaced
  with a neutral value, not skipped. This means the dormant
  component still takes up a slot in the weighted sum — just
  contributes nothing distinguishing. Cleaner alternative would
  be renormalizing weights over active components, but that
  introduces a second free parameter (how to renormalize) we
  don't currently need.

## When to revisit

Review is triggered automatically by the `review-triggers:` frontmatter
metrics:

- `corpus_fraction_with_active_bayesian < 0.30` four weeks post-deploy
  across installs — indicates `MIN_OUTCOME_SAMPLES` or
  `OUTCOME_WINDOW_DAYS` are too strict.
- `bayesian_override_env_usage > 0.50` of surveyed installs —
  default thresholds are wrong for the typical user; pull the
  override value into the default.
- Calendar: 2026-10-24. Six months is the minimum window to see
  whether the "progressive disclosure" UX pattern lands or confuses.

A manual revisit is also warranted if P1.1 (outcome-positive
expansion) ships and precision does **not** rise once Bayesian
activates — that would suggest the formula has a deeper problem
than gating can fix.

## Origin

Proposed during external review round 2 (2026-04-24). The structural
insight — *gate complex components until signal accumulates, make
dormancy visible* — came from the reviewer, who observed that
tuning the formula before understanding whether it needed to exist
in this shape was the wrong ordering. This ADR lifts that draft into
hypomnema's ADR schema; the reasoning is preserved unchanged.
