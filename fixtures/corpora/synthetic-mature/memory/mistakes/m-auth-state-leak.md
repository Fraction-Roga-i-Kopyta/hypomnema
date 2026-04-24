---
type: mistake
project: global
created: 2026-04-12
status: active
severity: major
recurrence: 2
scope: universal
root-cause: "Shared mutable authentication state between worker threads caused sessions to inherit each other's tokens under load."
prevention: "Keep auth context per-request; never store tokens in module-level singletons accessed concurrently."
keywords: [auth, threading, state, session]
domains: [backend, security]
triggers: ["shared auth state", "tokens leaking between sessions"]
---

Authentication middleware cached the current token in a module-level variable so subsequent handlers could read it. Under concurrent load two requests raced and the second one saw the first request's token.

The fix was to thread the auth context through the request object explicitly instead of relying on thread-local or module-level storage.
