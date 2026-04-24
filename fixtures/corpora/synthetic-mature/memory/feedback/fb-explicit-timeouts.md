---
type: feedback
project: global
created: 2026-04-12
status: active
description: Set explicit timeouts on every network call; defaults are usually too generous.
keywords: [timeouts, network, http]
domains: [backend]
triggers: ["explicit timeout", "network call without timeout"]
evidence:
  - "set an explicit timeout"
  - "default timeout is too long"
  - "bound the request duration"
---

Most clients default to infinite or minute-scale timeouts. Pick a bound appropriate to the caller — seconds, not minutes — and surface timeout errors distinctly from other failures.
