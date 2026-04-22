---
type: mistake
seed: true
created: 2026-04-22
status: active
domains: [general]
keywords: [debugging, hypothesis, root, cause, diagnosis, fix, cascade]
evidence:
  - "root cause"
  - "not just the first hypothesis"
  - "possible causes"
  - "three possibilities"
  - "two possibilities"
  - "let me enumerate"
  - "my candidates"
  - "i'll rank"
  - "my take"
  - "my vote"
  - "the list of"
  - "before fixing"
  - "shortlist"
  - "option a"
  - "option b"
severity: major
recurrence: 0
root-cause: "Claude jumps to the first hypothesis without enumerating possible causes, leading to cascading failed fixes"
prevention: "List 2-3 possible root causes before attempting the first fix; when a fix fails, return to the list instead of generating a new hypothesis on the fly"
decay_rate: never
ref_count: 0
scope: universal
---

# wrong-root-cause-diagnosis

## The pattern
Before the first fix attempt for a bug, enumerate 2-3 possible causes
explicitly in your reply to the user — do not commit to the first
hypothesis silently in your head.

## Why it matters
One of the highest-recurrence mistakes in Claude's debugging flow. Each
repeat looks the same:

1. See the symptom.
2. Form one hypothesis.
3. Begin the fix.
4. It doesn't help.
5. Generate a second hypothesis on the fly.
6. Continue the cascade.

The real cause is often third or later in a normal list — but the list
was never written down. Each failed fix pollutes context and makes the
real cause harder to spot.

## How to apply
- Write "Possible causes: (1) ... (2) ... (3) ..." or an equivalent
  enumeration in natural language.
- Then pick a first check, with an explicit reason — "starting with (N)
  because ...".
- If the first hypothesis isn't confirmed, **return to the list**
  instead of generating a new one on the fly.

## How this is measured
The `evidence:` phrases in this frontmatter are matched against your
replies at session end (case-insensitive substring). If any phrase
matched, the session-stop hook emits `trigger-useful`. If none matched,
`trigger-silent` — which, paired with a recurrence of the same mistake,
is the observable failure counter for this rule.

## Customising for your language
The evidence phrases above are English-biased — they match the way a
native English speaker enumerates alternatives ("let me enumerate",
"possible causes", "my candidates"). If you work with Claude in another
language, add phrases matching your natural enumeration style. Claude
will propose them after the first session where this rule silently
fails. See `docs/CONFIGURATION.md` for more on evidence authoring.
