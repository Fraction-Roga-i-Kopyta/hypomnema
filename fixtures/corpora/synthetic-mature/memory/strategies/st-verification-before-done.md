---
type: strategy
project: global
created: 2026-04-12
status: active
trigger: "claim done before verifying"
success_count: 5
keywords: [verification, done, testing]
domains: [process]
---

## Steps

1. Before marking a task complete, enumerate the minimum verification set: one unit test, one integration path, one smoke check.
2. Run them all. Record the output in the task log.
3. If any step can't be automated, document what was verified manually and how.

## Outcome

Caught three regressions that would have shipped: a migration that failed on empty tables, an auth flow that worked locally but not in staging, a deploy that rolled forward before migrations completed.
