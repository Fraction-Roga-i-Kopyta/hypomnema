---
type: decision
project: global
created: 2026-04-24
status: active
description: The five SessionStart scoring components use fixed weights (keyword ×3, project ×1, WAL 0-10, strategy +6 cap, TF-IDF ×2 capped, noise −3). This ADR justifies keeping them as-is rather than calibrating from first principles, and sets the conditions that would force a revisit.
keywords: [scoring, weights, calibration, priority]
domains: [ranking]
related: [cold-start-scoring, two-scoring-pipelines]
review-triggers:
  - metric: measurable_precision
    operator: "<"
    threshold: 0.10
    source: self-profile
  - metric: shadow_miss_ratio
    operator: ">"
    threshold: 0.50
    source: wal
  - after: "2027-04-24"
---

## What

SessionStart ranks candidate memory records with a weighted sum of
five components:

```
score = keyword_hits × 3
      + project_boost × 1              (file.project == current project)
      + wal_spaced_repetition (0–10)   (spread × decay × Bayesian_eff, capped)
      + strategy_bonus (min(success_count × 2, 6))
      + tfidf_body_match × 2 (capped)
      − 3 if record matches no active signals (noise penalty)
```

The concrete numbers (3, 1, 10, 6, 2, −3) have been stable since
v0.3. This ADR does **not** justify them from first principles. It
records why they have not been re-derived, and what evidence would
force a re-derivation.

## Why not calibrate from first principles

1. **Two sampling pipelines, not one.** SessionStart composes a
   score; UserPromptSubmit uses a deterministic priority key (see
   `docs/decisions/two-scoring-pipelines.md`). A "correct" weight
   on SessionStart is only correct against the distribution of
   SessionStart queries — typically git-derived keywords at the
   start of a session, not arbitrary user prompts. The data to
   calibrate against is the WAL, and the WAL is how the current
   weights produced today's corpus state. Regressing the weights
   from WAL samples generated under those weights is circular.

2. **The degenerate components are now diagnosable.** The v0.14
   cold-start ADR
   (`docs/decisions/cold-start-scoring.md`) made Bayesian and TF-IDF
   explicitly dormant on sparse corpora. Pre-v0.14, asking "is ×2
   the right weight for TF-IDF?" silently included "on a corpus
   where TF-IDF always returns 0 because the index is empty". That
   confound is gone — `memoryctl doctor` now prints which of the
   five components are actually contributing signal on this install.
   Revisiting weights without cold-start gating would have mixed
   two problems (empty index + weight calibration) into one.

3. **The weights encode ordering, not absolute scale.** Keyword
   hits dominate by design (×3) because that is the primary
   retrieval mechanism. Project boost breaks ties between otherwise
   equivalent records. WAL and TF-IDF are secondary signals. Noise
   penalty pulls obviously-irrelevant records below the quota cut.
   Tweaking ×3 → ×4 moves rankings within components of similar
   keyword scores but does not change which signals matter.
   Recalibration would need to show that the **ordering** is wrong,
   not that the magnitudes are suboptimal.

4. **Real measurements are non-alarming.** The maintainer corpus
   runs at 70 % `measurable_precision` with all five components
   active. External onboarding corpus runs at 0.5 % until P1.1
   lands, but the problem there is data collection (outcomes not
   registered for feedback), not weight balance.

## Trade-off accepted

- **Weights stay tuned to one person's WAL for now.** This is an
  unknown. The first external audit (this ADR's own trigger) will
  be by a reader who sees the numbers as magic. Until the third
  independent corpus lands (flagged in v1.0 gating), we do not
  have the data to tell "magic but correct" from "magic and wrong"
  apart.

- **The `−3` noise penalty is a rounded-up engineering number.**
  Large enough that a record with only a weak TF-IDF hit falls
  behind a record with a keyword hit; small enough that a record
  with real positive signal (keyword + WAL) still beats an inactive
  penalised record. Experimentally, 2 was too soft (let pure-TF-IDF
  hits sneak in) and 5 was too harsh (killed legitimately weak
  feedback). 3 is the middle, not a derived optimum.

- **`strategy_bonus` caps at 6.** After success_count = 3, additional
  successes don't help visibility — `log`-style diminishing returns
  without a log. The cap prevents a heavily-used strategy from
  crowding out on every session.

## When to revisit

Three automatic triggers via `review-triggers:` frontmatter:

- `measurable_precision < 0.10` on `self-profile` — the current 70 %
  floor dropped to below 10 %. Either the formula is now wrong for
  the author's corpus or something orthogonal (e.g. a hook doing
  wrong work) is breaking measurement. Run
  `memoryctl decisions review` and trace.
- `shadow_miss_ratio > 0.50` on `wal` — the primary keyword pipeline
  is missing more than half of what the FTS5 shadow pass would have
  surfaced. That points at weight-level mis-retrieval, not at
  corpus-level authoring issues.
- Calendar: `2027-04-24`. One year. If nothing else fires by then,
  re-read this ADR and decide whether to close it or extend.

Manual revisits are also warranted if a third user corpus
measures `measurable_precision` materially below the maintainer
baseline and the gap is not explained by cold-start gating.

## Calibration history

**2026-05-07 — shadow-miss false-positive cleanup.** `decisions
review` flagged this ADR at `shadow_miss_ratio = 0.65` (threshold
0.50). Evidence probe found 8/8 sampled shadow-miss events were
FTS5 false-positives — cross-project files surfaced on incidental
body-keyword overlap. The signal does not reflect a weight-level
mis-retrieval problem; it reflected a missing project filter in
the shadow pass itself (see ADR `fts5-shadow-retrieval` calibration
history). After the project filter landed, this threshold remains
correct: if `shadow_miss_ratio` still exceeds 0.50 after a 14-day
post-patch window, the gap is genuine and weight tuning is
warranted.

## Implementation note

The weights live in `hooks/session-start.sh:328-380` (awk-generated
composite). Changing any of them requires:

- Updating the formula in `CLAUDE.md § How injection ranks files`
  (the canonical source).
- Updating the summary in `README.md § Scoring` which intentionally
  does not repeat the formula.
- Running `bench-memory.sh perf` — a weight change that moves one
  record per session through the quota cut does not change latency,
  but a change that adds a new term would.
- Re-running the cross-reference protocol in
  `docs/fixtures/real-corpus-notes.md` so synthetic and real
  corpora still agree.
