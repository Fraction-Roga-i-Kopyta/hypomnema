# synthetic-mature

## Claim

A corpus with sufficient `outcome-*` signal **and** TF-IDF vocabulary
activates all five SessionStart scoring components. Opposite of
`synthetic-cold-start`; together they bracket the cold-start ADR.

Concretely:

- `doctor.scoring_components_active.extra.active == 5`.
- `dormant_components` is empty.
- WAL carries ≥ `MIN_OUTCOME_SAMPLES` (default 5) `outcome-*` events
  within `OUTCOME_WINDOW_DAYS` (default 14) — so Bayesian gate lifts.
- `.tfidf-index` holds ≥ `MIN_TFIDF_VOCAB` (default 100) unique terms
  — so TF-IDF gate lifts.
- Bayesian and TF-IDF both contribute non-neutral scoring (regression
  against accidentally keeping the gate on when it shouldn't be).

## Structure

- `memory/mistakes/` — 5 hand-authored mistakes with realistic
  severity/recurrence/root-cause/prevention fields.
- `memory/feedback/` — 5 feedback entries with `evidence:` fields.
- `memory/knowledge/` — 3 knowledge files with multi-sentence bodies
  (so TF-IDF vocab crosses the threshold).
- `memory/strategies/` — 2 strategies with `trigger:` and
  `success_count`.
- `memory/.wal` — ~70 events across 14 days: 45 inject, 10+
  outcome-positive, 4 outcome-negative, 6 clean-session, a mix of
  trigger-useful / trigger-silent / strategy-used.
- `memory/.tfidf-index` — pre-built TF-IDF index (committed so the
  fixture is self-contained; regenerated on demand by running
  `CLAUDE_MEMORY_DIR=<fixture>/memory bash hooks/memory-index.sh`).

## Expected behaviour

- Bayesian gate: lifted (≥5 outcomes in 14d).
- TF-IDF gate: lifted (≥100 unique terms in index).
- `memoryctl doctor --json` returns `scoring_components_active` with
  `active: 5, dormant_components: []`.

## Non-claim

This fixture does NOT verify:

- Absolute precision numbers — scoring magnitudes depend on WAL
  ordering and keyword hits against specific prompts, which this
  fixture doesn't probe end-to-end. Presence/absence of dormancy is
  the binary claim.
- P1.1 behaviour — feedback has `evidence:`, but no synthetic
  transcripts here. `synthetic-feedback-heavy/` covers evidence-match.
