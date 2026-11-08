---
type: mistake
name: "wrong-root-cause-diagnosis"
description: "Jumping to the first hypothesis without enumerating causes leads to cascading failed fixes"
created: 2026-04-22
status: active
severity: major
recurrence: 0
scope: universal
keywords: [debugging, hypothesis, root, cause, diagnosis, fix, cascade]
domains: [general]
root-cause: "Claude jumps to the first hypothesis without enumerating possible causes, leading to cascading failed fixes"
prevention: "List 2-3 possible root causes before attempting the first fix; when a fix fails, return to the list instead of generating a new hypothesis on the fly"
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
Usefulness comes from an explicit `<cc-memory filenames="…">` citation of
this fact in a session where it was actually delivered — wrap the sentence
that used it and the session-stop hook emits `cite-useful`; deliver it and
stay silent and it emits `cite-silent` instead. A recurring `cite-silent`
for this rule, paired with a repeat of the same mistake, is the observable
failure counter.

## Customising for your language
The keywords above are English. If you work with Claude in another
language, add keywords in it too — keyword overlap is the main ranking
signal, and the tokenizer is Unicode-aware.
