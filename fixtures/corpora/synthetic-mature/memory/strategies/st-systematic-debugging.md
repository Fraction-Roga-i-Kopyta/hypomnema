---
type: strategy
project: global
created: 2026-04-11
status: active
trigger: "flaky test in ci but passes locally"
success_count: 3
keywords: [debugging, flaky, ci, async]
domains: [testing]
---

## Steps

1. Run the test 50 times locally with `pytest --count 50`. If it goes flaky under repetition, it's genuinely racy — local.
2. If green locally and red on CI, suspect environment: slower CPU, tighter memory, different wall-clock resolution.
3. Replace any `sleep(N)` with event-based waits on observable conditions.
4. Add a structured log at each synchronisation point; correlate CI failure to a specific point.

## Outcome

Resolved three CI flakes across blog-api and data-pipeline that had lived for weeks.
