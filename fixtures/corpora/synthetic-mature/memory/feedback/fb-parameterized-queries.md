---
type: feedback
project: global
created: 2026-04-11
status: active
description: Always use parameterized queries; never build SQL via string interpolation.
keywords: [sql, parameterized, injection]
domains: [backend, security]
triggers: ["parameterized queries", "bind parameters to sql"]
evidence:
  - "parameterized queries only"
  - "bind parameters instead"
  - "never concatenate sql"
---

Driver placeholders exist for a reason. Interpolating user input into SQL strings is an injection surface even when the data looks benign.
