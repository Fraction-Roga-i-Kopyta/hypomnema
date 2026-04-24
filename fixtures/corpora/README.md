# Synthetic corpora

Parametric synthetic fixtures for hypomnema CI and regression testing.
No real user content — these are hand-authored minimal corpora, each
claiming one specific scoring / lifecycle behaviour.

**Do not use these as templates for your own memory directory.** They
exist to test the scoring pipeline at boundary conditions (empty /
mature / multilingual / edge-cases), not to model a "good corpus".

## Profiles (planned)

| directory | claim | blocks |
|---|---|---|
| `synthetic-cold-start/` | Bayesian + TF-IDF dormant on empty-outcome, minimal-vocab corpus | P1.0 calibration |
| `synthetic-mature/` | All 5 scoring components active; measurable precision ≥ 40% | P1.0 baseline |
| `synthetic-feedback-heavy/` | `outcome-positive` emits for non-mistake files after P1.1 | P1.1 validation |
| `synthetic-multilingual/` | TF-IDF covers Cyrillic / Hebrew / CJK after P1.3 | P1.3 regression |
| `synthetic-edge-cases/` | Doctor WARN on empty status / missing triggers; corrupt-WAL guard | P1.5, P1.7 |

Each fixture directory contains:

- `README.md` — claim, structure, scenario boundaries.
- `expected.json` — SPEC-first frozen snapshot. `hooks/test-fixture-snapshot.sh` diffs actual against this.
- `memory/` — the corpus contents (`.wal`, `feedback/`, `mistakes/`, …).
- `transcripts/*.jsonl` — synthetic session transcripts when evidence-match tests require them.

## One of, not canon

This directory is **one** source of evidence. Real-corpus cross-reference
is kept locally by the maintainer (see `docs/fixtures/real-corpus-notes.md`
once the first cross-reference pass lands). Optimising hypomnema scoring
specifically against these synthetic fixtures risks overfit; the synthetic
profiles must be kept representative, not oracular.

## See also

- `docs/plans/2026-04-24-synthetic-fixtures.md` — full spec, design principles, rollout phases.
- `docs/decisions/cold-start-scoring.md` — the ADR these fixtures first validate.
