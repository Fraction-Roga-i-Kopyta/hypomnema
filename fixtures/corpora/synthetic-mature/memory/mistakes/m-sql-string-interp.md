---
type: mistake
project: global
created: 2026-04-13
status: active
severity: critical
recurrence: 1
scope: universal
root-cause: "User-supplied search input was concatenated directly into an SQL WHERE clause, creating an injection surface."
prevention: "Always use parameterized queries; never build SQL via string interpolation even for internal tools."
keywords: [sql, injection, queries, database]
domains: [backend, security]
triggers: ["string interpolation in sql", "sql injection risk"]
---

A reporting endpoint built a `WHERE name LIKE '%{input}%'` clause via f-string concatenation. An internal tool, but still exploitable.

Parameterized queries via the driver placeholder syntax fix the bug and cost nothing.
