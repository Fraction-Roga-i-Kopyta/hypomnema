# Real-corpus cross-reference log

Record of each cross-reference pass between the committed synthetic
fixtures in `fixtures/corpora/synthetic-*/` and real corpora held
locally by the maintainer (never committed). Protocol is described in
`docs/plans/2026-04-24-synthetic-fixtures.md § Cross-reference protocol`.

This file is **text only** — no data, no slug names from real corpora,
no metrics tied to one person's workflow beyond the verdict. The point
is to track whether synthetic profiles stay representative of what
users actually hit, not to publish a shadow copy of private content.

---

## 2026-04-24 · cold-start ADR calibration (proposed → active)

**Version:** post-commit `3219cc2` (cold-start gates) + tfidf skip lift.

**Thresholds under test:**
- `HYPOMNEMA_BAYESIAN_MIN_SAMPLES` = 5 (default)
- `HYPOMNEMA_OUTCOME_WINDOW_DAYS` = 14 (default)
- `HYPOMNEMA_TFIDF_MIN_VOCAB` = 100 (default)

**Synthetic side:**
- `synthetic-cold-start` — Bayesian DORMANT, TF-IDF DORMANT, 3/5 active.
  Observed: 0 outcomes in 14d, 0 vocab (no tfidf-index).
- `synthetic-mature` — All components ACTIVE, 5/5. Observed: 10 outcomes
  in 14d, 367-term vocab.

**Real corpora (local only):**
- Mature corpus (maintainer) — 5/5 active. Observed: 44 outcomes in 14d,
  4807-term vocab.
- Onboarding corpus (external-ref-corpus, stored locally) — 3/5 active, both
  Bayesian and TF-IDF DORMANT. Observed: 0 outcomes, 0 vocab.

**Outcome:** synthetic and real corpora agreed on the verdict for both
ends of the spectrum. Cold-start synthetic → cold-start real, mature
synthetic → mature real. No surprise to investigate.

**Decision:** ADR status flipped `proposed → active`. Defaults 5/14/100
carried through all four pairings without adjustment.

**Open for next pass:**
- Need a third corpus (beyond maintainer + one onboarding user) to
  detect overfit. Flagged in v1.0 gating — a second adopter profile is
  required before v1.0 anyway.
- `HYPOMNEMA_TFIDF_MIN_VOCAB=100` was never stress-tested at the
  boundary (no corpus had vocab in the 50–150 band). If a near-miss
  case surfaces, add `synthetic-low-vocab` fixture to probe.
- Possible stale-signal case: slug with outcomes 25+ days ago but
  none recent. Current gate (14-day window) dormants it, current
  formula (30-day) still reads its outcomes. Intentional — gate
  is meant to dormant on stale data — but worth re-reading after
  one more corpus.

---

## 2026-04-24 · P1.1 dry-run — outcome-positive beyond mistakes/

**Version:** post-code `classify_outcome_positive_from_evidence` +
session-stop ordering fix (no commit yet at time of pass).

**Method:** no live prod session-stop run on real corpora (would
mutate WAL). Instead, diff'd the existing 14-day WAL:
- trigger-useful events = universe of slugs with evidence-match
- outcome-positive events = currently emitted subset (mistakes + any
  prior path)
- potential new emissions = trigger-useful events whose (slug, session)
  has no matching outcome-positive

**Maintainer corpus (14d window):**
- trigger-useful:            90 events
- outcome-positive existing: 40 events
- P1.1 new emissions (dry):  71 (slug, session) pairs
  → ~1.8× existing outcome-positive baseline

**Top slugs gaining signal** (all non-mistake types, as designed):
- `verification-before-done` (strategy): 8 session-pairs
- `coolify-post-deploy-check` (feedback): 8
- `debugging-approach` (feedback): 7
- `code-approach` (feedback): 7
- `css-layout-debugging` (strategy): 5

**Verdict:** significant lift expected — ≫ ADR's 5-15 pp minimum.
Direction matches plan: non-mistake types start contributing to the
Bayesian numerator. Formula now has data on slugs that previously sat
at the dormant 1.0.

**Not measured:** post-P1.1 `measurable_precision` delta. The formula
change compounds with the cold-start gate lift (P1.0 already lifted
gates on this corpus), so attributing the precision shift between
P1.0 and P1.1 requires a clean before/after run on a fresh synthetic
corpus. Deferred until `synthetic-feedback-heavy` fixture lands (the
task was intentionally scoped out of P1.1 — unit test in
`test-memory-hooks.sh` covers the contractual claims).

**Regression unit test:** 5 assertions in `test-memory-hooks.sh`
pin the behaviour — evidence-match emits, no-match stays silent,
idempotent on re-run, mistake path unchanged.
