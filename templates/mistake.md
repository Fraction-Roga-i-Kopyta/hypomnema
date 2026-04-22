---
type: mistake
project: global
created: YYYY-MM-DD
status: active
severity: minor            # critical | major | minor
recurrence: 1
ref_count: 0
root-cause: "One-line description of what went wrong."
prevention: "One-line description of how to avoid next time."
domains: [general]         # project domains this mistake applies to
keywords: [kw1, kw2]       # tokens used for SessionStart keyword-match scoring
# triggers:                # UNCOMMENT for substring-triggered injection on matching prompts.
#   - "phrase the user would type when this is relevant"
#   - "another phrasing"
# scope: narrow            # UNCOMMENT to restrict injection to explicit keyword match only
---

## Context

What happened, one short paragraph. Concrete enough that a future reader recognises the situation.

## Why this happened

Not just "we did X" — the underlying reason. A wrong mental model, a skipped check, a bad assumption.

## How to avoid

The rule future-you should apply to spot this pattern early.
