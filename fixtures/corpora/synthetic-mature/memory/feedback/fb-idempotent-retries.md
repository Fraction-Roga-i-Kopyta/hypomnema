---
type: feedback
project: global
created: 2026-04-14
status: active
description: Retry only idempotent operations; non-idempotent ones need an idempotency key.
keywords: [retry, idempotent, resilience]
domains: [backend]
triggers: ["idempotent retry", "retry with idempotency key"]
evidence:
  - "retry only idempotent"
  - "idempotency key"
  - "duplicate requests are safe"
---

A blind retry on a payment POST can double-charge. Either make the endpoint idempotent by design or require an idempotency header the client generates.
