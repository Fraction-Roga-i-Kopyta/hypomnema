---
type: decision
name: "short-slug-name"
description: "one-line outcome — what was decided, not what was debated"
created: YYYY-MM-DD
status: active
keywords: [tag1, tag2, tag3]
domains: [domain1, domain2]
# related: [other-slug]      # optional — cluster-link
# review-by: YYYY-MM-DD       # optional — calendar reminder to re-evaluate
---

<!--
Required v2 frontmatter: name, description, type. Do NOT add ref_count,
triggers, or project — they are sidecar-managed or derived from the store.
Prefer ADR-style: What / Why / Trade-off accepted / When to revisit.
-->

## What
<1 line: the choice, in outcome terms. Not context, not alternatives.>

## Why
<2-4 lines: the forcing functions that made this choice necessary.
State pressures and constraints, not the options you considered.>

## Trade-off accepted
<1-3 lines: what was given up. This is load-bearing — future agents
must know the cost so they don't try to "fix" it by reverting.>

## When to revisit
<1-2 lines: an observable signal that would re-open the decision.
If permanent-until-evidence, say so explicitly.>
