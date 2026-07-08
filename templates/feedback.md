---
type: feedback
name: "short-slug-name"
description: "one-line statement of the rule"
created: YYYY-MM-DD
status: active
keywords: [kw1, kw2]
domains: [general]
evidence:
  - "phrase Claude would write when following the rule"
  - "another giveaway phrase"
# precision_class: ambient   # optional — uncomment for rules that shape behaviour silently
---

<!--
Required v2 frontmatter: name, description, type. Do NOT add ref_count,
triggers, or project — they are sidecar-managed or derived from the store.

evidence: phrases that signal the rule was applied (case-insensitive
substring), read by the close hook to emit trigger-useful. ≤5 phrases score
best — long lists rarely match verbatim and get classified silent. Pick
phrases Claude would actually write when applying the rule.

precision_class: ambient marks rules that shape behaviour silently (tone,
language preference, security baseline). Such files still inject and rank
normally but are excluded from the self-profile precision denominator.
-->

One-sentence rule statement.

**Why:** The reason the user enforces this — typically a past incident, a preference, or a guardrail. Include the *why*; it lets future-Claude judge edge cases instead of following mechanically.

**How to apply:** Where and when the rule kicks in. Be specific about the boundary — internal code vs system boundary, tests vs production, which files or tools.
