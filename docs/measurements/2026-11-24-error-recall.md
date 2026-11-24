# Error recall — calibration replay

Date: 2026-11-24. Source: Bash failures in the session transcripts of one
live install (the author's), replayed read-only through the error-recall
selection under test (`SelectForFailure`, not merged — see Outcome) against
each failure's own project store plus the global store. Only counts and generic
vocabulary appear here — no command or error text, no project names
(projects are P1…P7 by failure count).

## 1. Corpus

Transcripts `~/.claude/projects/*/*.jsonl` and
`~/.claude/projects/*/*/subagents/*.jsonl` modified 2026-11-10 … 11-24. A
failure is a Bash `tool_use` whose `tool_result` has `is_error: true`. The
corpus is frozen at the commit of the selection API (2026-11-24 13:10
+02:00): later failures, from the calibration session itself, are left out.
API request snapshots (lines without a timestamp) repeat earlier results and
are not counted.

| | Count |
|---|---|
| Raw Bash failures | 648 |
| … in subagent transcripts | 284 |
| Harness pre-execution rejections (`<tool_use_error>`) — never fire PostToolUseFailure | −16 |
| `SkipFailure` (hook blocks, permission denials, worktree isolation, user rejections, classifier no-verdicts, timeouts, `memoryctl`) | −166 |
| **Filtered — the failures the hook would act on** | **466** |
| … in subagent transcripts | 174 |
| … of a subagent type on the skip list (the live hook delivers nothing) | 19 |
| … dated before 2026-11-10 (a session that started earlier) | 117 |

The spec counted 404 failures; this replay also reads subagent transcripts
and the 14 days up to the freeze, hence 648 raw.

Replay rules: store and project root from the session's launch directory
(the `cwd` of the first transcript line; for a subagent, of its parent
session) via `native.StoreFor` / `native.CanonicalRoot`, never from the
failing line's `cwd`; `native.Collect` of that store and the global store,
no sidecar; a fact is eligible only if its `created` (fallback `modified`)
is strictly before the failure date; frontmatter `stale`/`retired` facts
are not candidates; `today` = the failure date. All 7 launch stores exist;
1 launch directory no longer does (its store still resolves).

Not modelled: the "top-1 already in the session's context" rule, the
per-session / per-subagent caps and the signature and fact claims (each can
only turn a delivery into none, so live no-fact shares are at least these);
facts are read as they are today, not as they were on the failure date.

## 2. Two fixes found by the replay

The tightening runs (§4) were made before these two fixes; the final run
(§5) is after them.

- **Harness refusals.** 42 failures were harness texts in four forms
  `SkipFailure` did not list: the subagent variant of the worktree-isolation
  refusal, a built-in safety-check denial worded "Permission for this
  command…", the user's rejection of a tool call, and the auto-mode
  classifier's no-verdict message. The tightened gate delivered a fact on 8
  of them. They are now skipped, which takes the filtered set from 508 to
  466.
- **Command head.** Pipe filters after a `|` (`tail`, `head`, `grep`, `sed`,
  `sort`, `jq`, `xargs`, `cat` and the like) and shell setup segments (`set`,
  `cd`, `export`, `source`, `.`) no longer give head terms: they shape
  output or prepare the shell, and do not name the failing tool. Before the
  fix, `tail` and `pipefail` were among the most frequent head hits with the
  default gate. A subcommand such as `run` stays.

## 3. Error-noise list

Error-tail terms (`TermsForFailure` without noise: paths, identifiers and
stopwords already removed) counted per failure over the 508 failures
filtered before the fixes. A term qualifies when it is in at least the
threshold share of the failures of each of at least two projects, counting
only projects with 10 or more failures (P1, P2, P3; a smaller project
cannot carry the share). Every qualifying term was then checked to be a
common word.

| List | Threshold | Qualifying | Dropped by the word check | Size |
|---|---|---|---|---|
| First | 10 % | 19 | 1 product name | 18 |
| Widened (tightening step 3) | 5 % | 67 | 2 abbreviations, 1 product name | 64 |

The widened list is the one used in the final run (it lived in
`internal/inject/errornoise.go` on the unmerged branch), because the last
tightening step ran. It was not re-derived after the
fixes. It still contains words of the refusal texts that are now skipped
(`agent`, `isolated`, `worktree`, `complex`, `verify`, `refusing`); they are
common words, and keeping them changes nothing for the skipped failures.

## 4. Tightening steps (508 failures, before the fixes)

Target: **≥ 70 %** of the filtered failures get no fact. Each tightening
step keeps the previous ones.

| Run | Noise | Gate | Delivered | No fact | No-fact share |
|---|---|---|---|---|---|
| 0 | none | MinHits 2 | 449 | 59 | 11.6 % |
| 1 | first (10 %) | MinHits 2 | 414 | 94 | 18.5 % |
| 2 — step 1 | first | + `NameHitFromTail` | 366 | 142 | 28.0 % |
| 3 — step 2 | first | + `MinHits: 3` | 240 | 268 | 52.8 % |
| 4 — step 3 | widened (5 %) | `NameHitFromTail`, `MinHits: 3` | 175 | 333 | 65.6 % |

The target is missed after all three steps.

## 5. Final run (466 failures, after the fixes)

Gate options `GateOptions{MinHits: 3, NameHitFromTail: true}` and the
widened 64-word list, unchanged.

| Delivered | No fact | No-fact share |
|---|---|---|
| 165 | 301 | **64.6 %** |

By fact: 117 mistake, 39 knowledge, 9 strategy; 144 from the project
store, 21 from the global store; 92 distinct facts, the most frequent
delivered 16 times. 82 of the 165 deliveries are in subagents.

Per-project split:

| Project | Filtered failures | Delivered | No-fact share |
|---|---|---|---|
| P1 | 334 | 127 | 62.0 % |
| P2 | 111 | 33 | 70.3 % |
| P3 | 12 | 1 | 91.7 % |
| P4 | 4 | 2 | 50.0 % |
| P5 | 3 | 2 | 33.3 % |
| P6 | 1 | 0 | 100 % |
| P7 | 1 | 0 | 100 % |

P1 is 72 % of the denominator and decides the overall share.

## 6. What drives the deliveries

Gate hits of the delivered facts (final run): the few command-head hits
are `run` as a subcommand and `git`; most hits are error-tail words of the
project's own domain — service, module and field vocabulary that also
names the project's facts. That vocabulary is project-specific by
construction, so the two-project noise rule cannot remove it.

## 7. Relevance

Every (failure, fact) pair of the final run (165) goes to an independent
reviewer: "would this fact change what the agent does next?" Target
**≥ 50 % yes**.

Relevance: **5 yes / 160 no — 3.0 %** (target ≥ 50 %). The reviewer saw
only the pairs, not the selection rule.

| Why "no" (anonymised) | Pairs |
|---|---|
| Shell noise in a read-only command (zsh word expansion, unmatched glob, missing file in `ls`/`cat`, `cd` into a missing dir, BSD flag mismatch); the fact was about the project's domain | 90 |
| Red lint / format / type gate (line length, import order, unused import); the fact was a domain bug from the same module | 34 |
| Same tool or API, different failure mode; the fact described a neighbouring trap (in ≥ 3 cases a better neighbouring fact existed in the store) | 14 |
| Ad-hoc script, database or network error; the fact matched on words only | 12 |
| Real test failure; the fact was on topic but not about the cause | 10 |

| Why "yes" | Pairs |
|---|---|
| The fact named the failure mechanism directly | 2 |
| The fact described this exact trap and the fix | 2 |
| The fact explained why the type check fails and what to update | 1 |

One fact (`import-guard-preload-stale-during-run.md`) took 16 pairs, all
lint/format failures, 0 relevant.

## 8. Outcome

Both targets are missed: 64.6 % of failures get no fact (target ≥ 70 %)
and 3.0 % of delivered facts are relevant (target ≥ 50 %) — the same
precision as prompt-time push before v3.2.0 (30 useful / 975 silent).
Shared words between a failure's command/error text and a fact's head do
not predict that the fact helps: tightening the gate shrank the output but
did not raise precision.

**Decision: error recall is not shipped.** No hook, no selection code in
`main`; only this measurement is kept. A follow-up would need a trigger
that is precise by construction — e.g. a `mistake` fact declaring the
exact error signature it explains — rather than lexical matching.
