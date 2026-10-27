# UserPromptSubmit relevance — prompt gate, home-dir basename, Russian function-word forms

Date: 2026-10-27. Install: one live v2.16.0 install, Russian-language prompts,
69 facts in the home-directory scope (project store + global) plus four
project stores; WAL ~66 K lines. Sessions analysed: the 41 interactive
sessions (652 prompts) since the v2.16.0 upgrade (2026-10-17).

**Result.** Inside replayed sessions, precision on prompts rose from 12.9 %
to 27.8 % at the same injection volume (A2, B). Ranked per prompt without
dedup, it rose from 8.6 % to 42.5 % with 71 % fewer injected facts (A).

> **Correction (same day).** The first version of this report also replayed
> 123 headless SDK sessions — the security-review runs a Stop-hook plugin
> starts after every turn. hypomnema never runs in them: the plugin passes
> `setting_sources=[]`, and none of the 123 sessions appears in the WAL. All
> numbers below cover interactive sessions only. The earlier claim that
> "machine prompts carry most of the remaining noise" was an artifact of
> including them and has been withdrawn.

## Symptom

Most UserPromptSubmit injections were unrelated to the prompt. From the WAL,
22 parent sessions since 2026-10-17 averaged **15.2 injected facts and 0.4
`cite-useful`** — a citation precision of ~3 %. The model under-cites, so the
real precision is higher, but the order of magnitude matched what the
sessions showed.

## Root causes

Found by reading `internal/inject` + `internal/rank` and re-implementing the
score over the sidecar; the re-implementation reproduced a live injection
exactly (same facts, same order).

1. **No relevance floor on UserPromptSubmit.** The blend is zero-safe by
   design, so once the prompt-matching facts are placed, recency,
   effectiveness and popularity alone fill the rest of the top-8 / 8 KB.
2. **The home directory's basename is a query term.** Launched from `~`, the
   cwd basename is the login name. It occurs in every fact that quotes an
   absolute path (`/Users/<login>/…` — 10 of 69 facts here), so those facts
   got +3.0 on every prompt, and it was the *only* SessionStart term — the
   same four facts primed every session.
3. **Russian stopwords list base forms only.** Russian inflects function
   words: "этом", "тому", "тех", "ним", "своих", and the particle "давай"
   stayed in the relevance set, and each hit scores 3.0 — more than the
   recency maximum (2.0). Worked example: a prompt containing
   "…перед этом давай…" pulled an unrelated device-diagnostics note in via
   "этом" plus the login name.
4. *(not addressed here)* **Corpus-common words weigh like rare ones** —
   "2026" occurs in 42 facts, "claude" in 35. IDF alone would not fix the
   noise below: conversational filler words are *rare* in a fact corpus and
   would get a high IDF.
5. *(not addressed here)* **No morphology** — "ошибка" / "ошибки" are
   different terms, so genuine Russian matches are scarce.

## Changes

- `inject/keywords.go` — skip the cwd basename when cwd is `$HOME`.
- `tokenize/stopwords.go` — Russian case forms of the listed pronouns and
  determiners, plus the remaining closed-class particles (no content words,
  matching the file's policy). Query terms pass the same filter, so no
  rebuild is required; `memoryctl sidecar rebuild` drops the dead rows.
- `inject/inject.go` — **prompt gate on UserPromptSubmit**: a fact injects
  only if a prompt term is in its `name` / `description` / `keywords` /
  `domains`, or it shares at least two distinct terms with the prompt
  anywhere. cwd and git terms still rank but do not pass the gate; a gate
  query error fails closed. SessionStart and SubagentStart are unchanged.

### Why not "at least one prompt term"

That was the first iteration, and the session replay rejected it: facts
injected on prompts went **up 62 %** (393 → 636 over 652 prompts, 0.88 →
1.36 MB). With the zero-overlap facts gone, the window was no longer filled
by the same recency/popularity winners (deduplicated after their first
injection) but by prompt-specific facts — mostly passing on one word. Of
1271 (prompt, fact) pairs that variant produced, 860 matched exactly one
word. The most frequent were conversational: "оба" (both), "работает"
(works), "вариант" (option), "сделали" (we did), "пока", "сегодня" (today),
"проверь" (check). Content words occur in that list too ("ipad",
"планеты"), so the stricter rule — a prompt term in the fact's
name/description/keywords/domains, or two terms anywhere — also drops some
genuine single-word matches. It cut 751 of the 1271 pairs. Measurements A
and A2 show the net effect.

One-term prompts were not relaxed: 135 of 652 prompts (21 %) had a single
relevance term, and every one was conversational ("progress", "done",
"image", "go ahead", "continue"), not a topic query.

## Measurements

### A. Blind relevance judgement, per prompt (precision)

100 random real prompts (seed 20261027), of which 87 come from interactive
sessions; only those 87 are counted. Each prompt was ranked independently —
no session dedup — by v2.16.0 and by this branch against copies of the same
sidecar. The union of (prompt, fact) pairs was shuffled and labelled
relevant / not relevant by a separate model (Claude Sonnet, high effort)
that did not know which binary produced a pair. The labelling rule:
"relevant — the fact would plausibly help answer or act on *this* message
(same topic, project, tool, problem or procedure)".

| 87 prompts | v2.16.0 | this branch |
|---|---|---|
| injected (prompt, fact) pairs | 280 | 80 |
| relevant | 24 | **34** |
| irrelevant | 256 | **46** |
| precision | 8.6 % | **42.5 %** |
| share of all relevant pairs found (43 in the union) | 56 % | **79 %** |
| prompts that got any injection | 87 | 38 |
| prompts that got noise only | 69 | **15** |

### A2. Blind relevance judgement inside replayed sessions

Measurement A credits each prompt with its full top-K. In a session the
once-per-context dedup decides what actually lands, so the same blind
procedure was repeated on what the session replay (B) really injected: 65
random interactive prompts that got an injection from either binary.

| 65 prompts | v2.16.0 | this branch |
|---|---|---|
| injected pairs | 93 | 79 |
| relevant | 12 | **22** |
| irrelevant | 81 | **57** |
| precision | 12.9 % | **27.8 %** |
| share of all relevant pairs found (29 in the union) | 41 % | **76 %** |
| prompts that got noise only | 33 | **25** |

### B. Session replay (volume)

41 interactive sessions, 652 prompts, replayed in order — SessionStart, then
every prompt with the real once-per-context dedup — by each binary against
its own copy of the sidecar and WAL (`CLAUDE_MEMORY_DIR`). The copy for
v2.16.0 was rebuilt with the v2.16.0 binary (old stopwords). The live WAL
was checked untouched afterwards.

| | v2.16.0 | this branch | "≥1 term" variant |
|---|---|---|---|
| facts injected at SessionStart | 144 | 144 | 144 |
| facts injected on prompts | 393 | 397 | 636 |
| bytes injected on prompts | 0.88 MB | 0.84 MB | 1.36 MB |
| prompts with no injection | 471 | 451 | 368 |

Volume per session is unchanged: dedup already capped the old ranker, but
the slots went to the same recency/popularity winners in every session. The
branch spends the same budget on prompt-matched facts, which is what A2
measures.

### C. Tests

- Three regression tests, red on v2.16.0 and green on the branch:
  `TestKeywords_HomeDirBasenameDropped`,
  `TestRelevance_RussianInflectedFunctionWords`,
  `TestRun_UserPromptSubmitRequiresPromptMatch`.
- Six existing fixtures now carry `keywords: [docker]`
  (`TestRun_TotalBudgetCapsRender`, `TestRun_FirstEntryLargerThanBudgetIsTruncated`,
  `TestRun_SkipsAlreadyInjected`, `TestRun_AllAlreadyInjectedYieldsEmpty`,
  `TestRun_CiteInstructionAndFactHeaders`,
  `TestInjectVerb_RuntimeListAccumulatesUnderBudget`): their facts matched
  the one-word prompt "docker" only in the body. Their subject is the render
  budget and session bookkeeping, not relevance. `TestRun_HoldoutSkip`'s
  refill facts are now named after a prompt term for the same reason.
- `go test -race ./...` green; every commit passes on its own. `staticcheck`
  v0.8.2 is clean on the changed packages; the Makefile's pinned v0.7.0
  cannot load Go 1.27 export data ("export data version 5 is greater than
  maximum supported version 2") — a toolchain issue, independent of this
  change.

## Caveats

- One model's labels, judged from a 500-character message and a fact
  summary.
- Measurement A ranks each prompt alone, so SessionStart priming is not
  credited. The 9 relevant facts the branch dropped in A were all "where we
  left off" / project-audit notes answering content-free follow-ups ("yes,
  push both, the second PR as a draft"). In a real session these are
  typically already in context from the ungated SessionStart.
- The `cite-useful` ground truth inside the replayed sessions was too small
  (4 facts) to measure recall from citations.
- Most in-session injections are still irrelevant (A2: 57 of 79). Causes 4
  and 5 above remain.

## Also noticed (not changed)

- `OverlapScores` groups by `slug` only, not `(slug, project)`:
  same-basename facts in different projects (`continuity.md`, `notes.md`)
  share their overlap counts. The gate uses the same key as the ranker.
- `memoryctl rank` is unscoped (`s.All()`) and does not add the cwd token,
  so it cannot reproduce a live injection. A faithful dry run is
  `memoryctl inject --event=UserPromptSubmit` with **no** `session_id`
  (nothing is persisted).
- Replay safety: `inject` with a `session_id` appends `inject` events to the
  WAL and writes `.runtime` lists. Any replay must point `CLAUDE_MEMORY_DIR`
  at a copy — and the variable has to actually reach the child process. Our
  first replay missed that, wrote 4 745 synthetic events into the live WAL,
  and had to be cleaned under the `.wal.lockd` flock, followed by a
  per-project `sidecar rebuild`.
- Transcripts of headless SDK runs live next to interactive ones in
  `~/.claude/projects/<project>/`. Any replay or measurement over
  transcripts should filter on `entrypoint` (`sdk-py` here) or on presence
  in the WAL.

## Reproduce

```sh
# dry run, nothing persisted (no session_id)
printf '{"cwd":"%s","prompt":"%s"}' "$PWD" "<prompt>" \
  | CLAUDE_PROJECT_DIR="$PWD" memoryctl inject --event=UserPromptSubmit

# isolated replay: copy the sidecar + WAL, point the binary at the copy
mkdir -p /tmp/hm && cp ~/.claude/memory/.sidecar.db ~/.claude/memory/.wal /tmp/hm/
CLAUDE_MEMORY_DIR=/tmp/hm memoryctl inject --event=UserPromptSubmit < envelope.json
```
