---
type: mistake
name: "short-slug-name"
description: "one-line summary of the mistake, shown in the MEMORY.md index and injection header"
created: YYYY-MM-DD
status: candidate   # agent-originated facts start as candidate; user-dictated rules use active|pinned
severity: minor            # critical | major | minor
recurrence: 1              # bump when the same mistake recurs
scope: narrow              # universal | domain | narrow — informational: how broadly the lesson applies
keywords: [kw1, kw2]
domains: [general]
root-cause: "One-line description of what went wrong."
prevention: "One-line description of how to avoid next time."
---

<!--
Required v2 frontmatter: name, description, type. status is active|pinned
(stale is sidecar-managed — never hand-set). Do NOT add ref_count,
effectiveness, triggers, or project — ref_count/effectiveness are
sidecar-managed and project is derived from the store location.
keywords are the primary ranking signal — use real tokens, not placeholders.
-->

## Context

What happened, one short paragraph. Concrete enough that a future reader recognises the situation.

## Why this happened

Not just "we did X" — the underlying reason. A wrong mental model, a skipped check, a bad assumption.

## How to avoid

The rule future-you should apply to spot this pattern early.
