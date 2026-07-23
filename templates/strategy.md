---
type: strategy
name: "short-slug-name"
description: "one-line summary of the approach and when it applies"
created: YYYY-MM-DD
status: candidate   # agent-originated facts start as candidate; user-dictated rules use active|pinned
success_count: 1           # bump each time the strategy works again
keywords: [kw1, kw2]
domains: [general]
---

<!--
Required v2 frontmatter: name, description, type. keywords should reflect
the situation that should trigger retrieval of this strategy. Do NOT add
ref_count, triggers, or project — they are sidecar-managed or derived.
-->

# Short name

## Steps
1. First concrete action.
2. Second action.
3. Third action.

## Outcome
What applying the strategy typically gets you. One or two sentences — this is what Claude reads to decide if the strategy is worth quoting back to the user.
