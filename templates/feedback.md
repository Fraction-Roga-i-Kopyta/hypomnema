---
type: feedback
project: global
created: YYYY-MM-DD
status: active
ref_count: 0
domains: [general]
keywords: []
evidence:                        # phrases that signal the rule was applied, for trigger-useful events
  - "phrase Claude would write when following the rule"
  - "another giveaway phrase"
# precision_class: ambient       # UNCOMMENT for rules that shape behaviour silently
                                 # (tone, language preference, security baseline). Excludes the
                                 # file from the measurable-precision denominator so it doesn't
                                 # drag the metric down as false noise. See README § Precision.
# triggers:                      # UNCOMMENT for reactive injection on matching user prompts.
#   - "prompt phrasing that should remind Claude of this rule"
---

One-sentence rule statement.

**Why:** The reason the user enforces this — typically a past incident, a preference, or a guardrail. Include the *why*; it lets future-Claude judge edge cases instead of following mechanically.

**How to apply:** Where and when the rule kicks in. Be specific about the boundary — internal code vs system boundary, tests vs production, which files or tools.
