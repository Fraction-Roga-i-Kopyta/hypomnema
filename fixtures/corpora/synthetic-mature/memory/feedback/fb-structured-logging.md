---
type: feedback
project: global
created: 2026-04-15
status: active
description: Log events with structured fields, not free-form strings; the log aggregator has to index them.
keywords: [logging, structured, observability]
domains: [backend, observability]
triggers: ["structured logging", "log fields not strings"]
evidence:
  - "structured logs"
  - "log as json"
  - "key-value log fields"
---

A log line like `user 42 failed to login from 10.0.0.1` is a string to everyone. `{level, user_id, event, source_ip}` is queryable and alertable.
