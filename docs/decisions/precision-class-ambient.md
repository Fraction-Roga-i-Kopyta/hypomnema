---
type: decision
project: global
created: 2026-04-20
status: active
description: Rules that shape behaviour silently (tone, language, security baselines) carry `precision_class: ambient` and are excluded from the measurable-precision denominator.
keywords: [precision, ambient, silent-applied, metric]
domains: [retrieval, observability]
review-triggers:
  - metric: measurable_precision
    operator: "<"
    threshold: 0.40
    source: self-profile
  - after: "2027-04-20"
---

## What

A memory file may declare `precision_class: ambient` in its
frontmatter. At evaluation time (`memory-self-profile.sh` and
`internal/profile`), any `trigger-useful` / `trigger-silent` event
for such a file is counted separately as "ambient activation" and
excluded from the numerator and denominator of measurable-precision.
Ambient rules continue to be injected and ranked exactly like any
other record — the classification is measurement-only.

Example: a feedback file "always respond in Russian when the user
writes in Russian" is applied on every Russian-language session
but is never quoted back verbatim by Claude. Without the ambient
mark, every such session logs a `trigger-silent` and drags the
precision metric toward 0 even though the rule is perfectly
effective.

## Why

The original precision metric assumed that a useful rule produces a
textual reference in the assistant reply (the evidence-match in
Stop-hook logic picks up the slug or an `evidence:` phrase). Many
rules in practice are **behavioural shaping** — tone, language
preference, "don't hardcode secrets", "parameterize queries". Their
effect is on *what Claude did*, not *what Claude wrote*. The
measurement wasn't wrong; it was measuring a narrower thing than
its name suggested.

Two options were considered:

1. **Change the metric definition** to accept behaviour inferred
   from actions (tool calls, diff shape). Expensive — requires a
   classifier that inspects every tool invocation — and
   introduces the classifier as a new source of noise.
2. **Change the set the metric is computed over.** A one-word
   frontmatter field declares "this file's usefulness is not
   textually measurable, don't include it in the ratio". Cheap,
   author-controlled, explicit.

We chose (2) because it preserves the original metric's clarity
(it still measures "fraction of cited-back injections") and moves
the classification problem to authoring time, where the author
already knows whether the rule is ambient.

## Trade-off accepted

- **Classification is subjective.** An author can mark a rule
  ambient to improve the metric without the rule actually being
  ambient. There is no automatic check; the only guardrail is
  review at authoring time.
- **Silent injection remains silent.** Ambient rules whose
  behaviour regresses (Claude starts ignoring them) are hard to
  detect — there's no `trigger-silent` signal to watch. Partial
  mitigation: `.analytics-report § Trigger usefulness` surfaces
  `useful=0 silent=N` rules, which is often a candidate for
  `precision_class: ambient` the author hasn't marked yet.
- **The denominator shrinks.** On corpora with many ambient
  rules, the measurable-precision ratio is computed over fewer
  observations, so per-session noise in the ratio is larger.

## When to revisit

Either:

- A principled test to detect "rule was applied even though not
  cited" lands (e.g., evidence inference from tool calls). At that
  point ambient classification moves from author-declared to
  measurement-derived.
- Or: `.analytics-report` shows that a large fraction of rules
  marked ambient are in fact measurable — the classification is
  being abused to hide noise. Partial automation (require a
  `consistency_evidence:` field on ambient rules) becomes the
  first lever to pull.
