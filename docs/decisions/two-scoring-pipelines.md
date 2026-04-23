---
type: decision
project: global
created: 2026-04-15
status: active
description: SessionStart uses a composite score optimised for recall; UserPromptSubmit uses a lexicographic priority key optimised for determinism.
keywords: [scoring, recall, precision, ranking]
domains: [architecture, retrieval]
---

## What

Two independent ranking algorithms live side by side:

- **SessionStart** (`hooks/lib/score-records.sh`,
  `hooks/session-start.sh`) computes a composite score per record —
  `keyword_hits×3 + project_boost + wal_spaced_repetition +
  strategy_bonus + tfidf_body_match×2 − noise_penalty`. Blending the
  signals gives higher recall when the user has not yet typed a
  specific prompt.
- **UserPromptSubmit** (`hooks/user-prompt-submit.sh`) computes a
  lexicographic priority key — seven ordered levels: project,
  status, severity, recency, `log₁₀(ref_count)`, recurrence,
  ref_count. A substring-trigger match is a prerequisite; the key
  orders only the records that already matched.

Neither pipeline uses the other's function. Neither migrates toward
the other.

## Why

SessionStart fires once per session with no user prompt to ground the
query. It needs to produce a useful context block from the intersection
of signals (domain, WAL spaced repetition, keyword matches against the
CWD path) none of which individually justify selection. Blending is
the right shape when evidence is diffuse.

UserPromptSubmit fires on every prompt with a concrete user query. The
substring trigger pre-filters the candidate set; ordering then needs
to be deterministic (so replay-runner measurements stay stable across
runs) and explainable (so the author can reason about why file X was
chosen over file Y). A lexicographic key over discrete buckets
satisfies both; a composite score does not.

## Trade-off accepted

- Two pipelines mean two places to update when a new ranking signal
  is added. History shows this is real: the `scope: narrow` mistake
  filter was added to both.
- "Scoring" is now an ambiguous word in conversation. README, FAQ,
  and `docs/ARCHITECTURE.md` all have to qualify which pipeline they
  mean.
- Reviewers familiar with one style ask why the other exists. Cost
  paid in documentation, not in code.

## When to revisit

A concrete class of record that ranks high in one pipeline and low in
the other for reasons that aren't explainable by recall-vs-precision
intent. That would mean the two are not actually pursuing different
objectives — either the split is accidental or one of them is
miscalibrated.
