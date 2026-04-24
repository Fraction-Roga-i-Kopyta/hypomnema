---
type: feedback
project: global
created: 2026-04-13
status: active
description: Raise or return errors explicitly; do not return magic sentinel values from functions.
keywords: [errors, sentinel, api]
domains: [backend]
triggers: ["no sentinel values", "return errors explicitly"]
evidence:
  - "do not return sentinel"
  - "raise the error instead"
  - "explicit error type"
---

A returned `-1` or `None` as a silent error is a bug waiting to be missed by a caller that forgets to check. Raise, return a Result, or use a dedicated error type.
