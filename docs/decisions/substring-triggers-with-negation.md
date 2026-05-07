---
type: decision
project: global
created: 2026-04-12
status: active
description: Reactive injection uses case-insensitive substring match on `triggers:` phrases with a ±40-character negation window, not regex or embeddings.
keywords: [triggers, substring, negation, matching]
domains: [retrieval]
related: [fts5-shadow-retrieval, two-scoring-pipelines]
review-triggers:
  - metric: shadow_miss_ratio
    operator: ">"
    threshold: 0.60
    source: wal
  - after: "2027-04-12"
---

## What

`hooks/user-prompt-submit.sh` compares the current prompt (lowercased)
against every `trigger:` / `triggers: []` phrase declared in
frontmatter. Match rule:

1. Case-insensitive substring: `"rust async deadlock"` matches the
   prompt `"debugging a Rust async deadlock please"`.
2. A ±40-character window around the match is scanned for a
   negation token (`не`, `без`, `уже`, `нет`, `already`, `fixed`,
   `skip`, `no`, `don't`, `ignore`, `without`, and a few more).
   A negated match is rejected.
3. The first matching phrase for the file wins; no further phrases
   are tried.

Files whose `triggers:` phrases don't appear in the prompt are not
considered for reactive injection (they may still be surfaced by
SessionStart, which uses a different pipeline).

## Why

Three constraints fix the shape:

1. **Author-controlled.** The memory file's author declares
   precisely which prompts it should fire on. Regex invites
   complexity the author doesn't want; embeddings invite
   unpredictable injection the author can't audit.
2. **Measurable.** Substring match has a well-defined recall and
   false-positive rate. Deterministic behaviour is a prerequisite
   for `make replay` to produce stable retrieval metrics across
   runs.
3. **Cheap.** One `case "$LOWER_PROMPT" in *"$phrase"*)` per
   phrase per file — hook p95 stays under a couple of hundred ms
   even with a few hundred memory files.

Negation was added in v0.6 after observing that evidence phrases
like `"CORS"` trigger legitimately on `"had a CORS issue"` and
illegitimately on `"no CORS problem here"`. The ±40-char window is
heuristic — enough to catch typical English / Russian negation
placement without invoking a full parser.

## Trade-off accepted

- **Cold start is bad.** Fresh installs ship with `triggers:`
  fields that need to be populated for the channel to fire.
  `make replay` on seeded corpus: 2.1% trigger-match. Partially
  mitigated by v0.9.1 templates (active `triggers:` fields rather
  than commented) and v0.10.3 followups (the last two templates
  opened).
- **Paraphrases miss.** `"timeout querying database"` and
  `"db query is hanging"` won't cross-trigger unless both phrases
  appear in `triggers:`. FTS5 shadow (see `fts5-shadow-retrieval`)
  exists precisely to make this invisible miss visible.
- **Negation window is fixed at 40 chars and one token list.**
  A negation 50 chars away will fire the rule anyway. We accept
  false positives in that tail rather than grow the window and
  risk false negatives near legitimate uses.

## When to revisit

`make replay` persistently below ~10% trigger-match on a well-
populated (non-seeds-only) corpus, or `shadow-miss` counts in
`.analytics-report § Recall gaps` stay high on slugs whose
`triggers:` are non-empty. Either signal indicates that the
channel is structurally incapable of matching user prompts, not
just that the author has undertrained phrases.

## Calibration history

**2026-05-07 — shadow-miss false-positive cleanup.** This ADR was
flagged at `shadow_miss_ratio = 0.65` (threshold 0.60). Evidence
probe found 8/8 sampled shadow-miss events were FTS5 false-
positives caused by cross-project body-keyword overlap, not by
substring-trigger weakness. The fix landed on the shadow side
(project filter — see ADR `fts5-shadow-retrieval` calibration
history), not on the substring side. The 0.60 threshold here
remains the right escalation: if it still fires after a 14-day
post-patch window with cleaner shadow signal, the channel is
genuinely under-recalling and a structural revisit (broader
trigger sets, regex, or embeddings) is warranted.
