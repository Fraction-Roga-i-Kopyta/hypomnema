# synthetic-cold-start

## Claim

A young corpus with **no `outcome-positive` / `outcome-negative` events**
and **minimal body vocabulary** MUST report Bayesian effectiveness and
TF-IDF body match as dormant. The scoring formula degrades gracefully
to `keyword × 3 + project × 1 + recency` until signal accumulates.

Concretely:

- `memoryctl doctor` prints a `scoring_components_active` check with
  `active: 3, total: 5, dormant: [bayesian, tfidf]`.
- Injection ranking on a trigger-matching prompt still returns the
  right slug (keyword path is live).
- `self-profile.md` shows 0 outcomes and `measurable precision = null`
  (no denominator yet).

## Structure

8 hand-authored memory files (5 feedback + 3 knowledge), no mistakes,
no strategies, no decisions. WAL has 40 `inject` + 10 `trigger-silent`
events across 10 synthetic sessions — no closing events, no outcomes.

Body length per file: 1–2 lines. Intentional: keeps TF-IDF vocabulary
below the `MIN_TFIDF_VOCAB=100` threshold from the cold-start ADR.

No `evidence:` field in frontmatter — outcome-positive cannot be
emitted from session transcripts against these files even when P1.1
lands. This is the defining property of a cold-start corpus.

## Expected behaviour

- `doctor.scoring_components_active.extra.active == 3`
- `doctor.scoring_components_active.extra.dormant` contains `bayesian`
  and `tfidf` (in any order).
- `compute_wal_scores` returns Bayesian effectiveness `1.0` (neutral)
  for every slug — NOT 0.5 (pre-cold-start-ADR behaviour) and NOT
  the formula value (which would require outcome data).
- `compute_tfidf_scores` returns empty or all-zero for every slug
  when the index has <100 unique terms.

## Non-claim

This fixture does NOT verify:

- P1.1 behaviour (no synthetic transcripts; see `synthetic-feedback-heavy/`).
- That the scoring formula is "correct" at maturity — only that it
  degrades correctly when signal is missing.
- Real-world representativeness — this is a clean minimal corpus, not
  a model of how users actually write memory.

## What breaks if this fixture fails

- **Bayesian returns non-1.0 for zero-outcome slug:** gate in
  `hooks/lib/score-records.sh` is wrong (likely reverted to `0.5`
  constant).
- **Doctor reports `5/5 active`:** the new check
  (`scoring_components_active`) was not added or is not reading the
  gate state.
- **TF-IDF score > 0:** vocab gate is not wired into
  `compute_tfidf_scores` / `session-start.sh:373`.
- **Injection on trigger-prompt returns wrong slug:** degraded
  scoring broke the keyword path, not just the dormant components.
