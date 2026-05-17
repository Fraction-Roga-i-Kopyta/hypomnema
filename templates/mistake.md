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
triggers:                  # phrases the user would type when this mistake is relevant.
  - "replace this placeholder with a real user phrase"   # substring-matched, case-insensitive
  - "delete examples you do not need — keep at least one real phrase"
                           # 4-6 phrases is the empirical sweet spot (~85% useful-rate at 4-6,
                           # only ~12% at 1-3 because they match too narrowly). See CLAUDE.md.
# scope: narrow            # UNCOMMENT to restrict injection to explicit keyword match only
---

## Context

What happened, one short paragraph. Concrete enough that a future reader recognises the situation.

## Why this happened

Not just "we did X" — the underlying reason. A wrong mental model, a skipped check, a bad assumption.

## How to avoid

The rule future-you should apply to spot this pattern early.
