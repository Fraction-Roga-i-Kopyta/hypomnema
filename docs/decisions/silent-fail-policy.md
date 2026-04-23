---
type: decision
project: global
created: 2026-04-22
status: active
description: Every hook exits 0 on internal failure; only PreToolUse/dedup may return non-zero, and only to block a write.
keywords: [hooks, fail-safe, contract, exit-code]
domains: [architecture, integration]
related: [two-scoring-pipelines]
review-triggers:
  - after: "2027-04-22"
---

## What

`docs/hooks-contract.md §1.3` states: a hypomnema hook must exit 0 on
any internal failure — broken memory file, unreadable config, failed
subprocess, malformed WAL line. Observability moves to WAL events
(`schema-error`, `format-unsupported`, `evidence-missing`) and the
SessionStart health-check block. The only intentional non-zero exit
is 2 from `memory-dedup.sh`, and only when a proposed mistake is
≥80% similar to an existing one.

## Why

Hypomnema is attached to a developer workflow through Claude Code
hooks. A hook that returns non-zero can break the user's session —
abort a tool call, truncate a response, or surface an error dialog
the user didn't cause. The contract inverts the default: hypomnema
is never the reason a legitimate interaction fails.

The second-order effect is cheap: losing one `inject` event is a
smaller regression than bricking the user's conversation. WAL
accumulation makes the lost signal visible in aggregate via
`memoryctl doctor` and `.analytics-report`, so silent-fail is not
silent in the long run.

## Trade-off accepted

- Diagnosis is delayed. A bug that silently exits 0 every hook
  invocation is invisible until someone runs `memoryctl doctor` or
  reads the WAL. Partial mitigation: SessionStart health-check
  surfaces three classes (broken symlinks, 50+ injects without
  trigger-match, schema-error buildup).
- The contract forbids the simplest debugging gesture — letting a
  hook crash visibly. A `HYPOMNEMA_LOUD=1` escape hatch has been
  proposed and is deferred until the cost of delayed diagnosis
  outweighs the cost of a failure mode on the user's primary path.
- `2>/dev/null` appears ~370 times across the hook layer. Each one
  is a policy choice, but the density makes it hard to distinguish
  "expected subprocess noise" from "real bug we're hiding".

## When to revisit

A case where silent-fail actually harmed a user — say, a corrupted
WAL that accumulated for weeks undetected and eventually produced
misleading analytics. At that point introduce the loud-mode escape
hatch and a scheduled `memoryctl doctor` alarm, rather than
inverting the contract.
