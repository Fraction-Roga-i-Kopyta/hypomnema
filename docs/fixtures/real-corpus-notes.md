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
- Onboarding corpus (external-01, stored locally) — 3/5 active, both
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
