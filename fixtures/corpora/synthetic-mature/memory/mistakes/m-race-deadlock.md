---
type: mistake
project: data-pipeline
created: 2026-04-15
status: active
severity: major
recurrence: 2
scope: domain
root-cause: "Two scheduled jobs acquired table locks in opposite orders, deadlocking under moderate load."
prevention: "Define a global lock ordering for shared resources; document it and audit all writers against it."
keywords: [deadlock, locks, concurrency, database]
domains: [backend]
triggers: ["deadlock under load", "lock acquisition order"]
---

Job A took lock on `events`, then `metrics`. Job B took them in reverse. Under load both blocked waiting for the other.

Fix: a documented ordering (events → metrics → reports) enforced in a wrapper around transaction starts.
