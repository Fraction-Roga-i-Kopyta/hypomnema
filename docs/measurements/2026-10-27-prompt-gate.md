# UserPromptSubmit relevance — prompt gate, home-dir basename, Russian function-word forms

Date: 2026-10-27. Install: one live v2.16.0 install, Russian-language prompts,
69 facts in the home-directory scope (project store + global) plus four
project stores; WAL ~66 K lines. Sessions analysed: everything since the
v2.16.0 upgrade (2026-10-17).

**Result.** On human prompts inside replayed sessions, precision rose from
12.9 % to 27.8 % at the same injection volume (A2, B). Ranked per prompt
without dedup, it rose from 11.9 % to 40.0 % with 63 % fewer injected facts
(A). Long machine prompts are unaffected and are now the main source of
noise.

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
injected on prompts went **up 31 %** (768 → 1008 over 777 prompts, 1.80 →
2.28 MB). With the
zero-overlap facts gone, the window was no longer filled by the same
recency/popularity winners (deduplicated after their first injection) but
by prompt-specific facts — each passing on one word. Of 637 passing
(prompt, fact) pairs, 388 matched exactly one word, mostly conversational
filler: "сегодня" (today), "сделали" (we did), "работает" (works), "пока",
"готово" (done), "проверь" (check), "просто" (just). The name/description
rule plus "two terms anywhere" cut half of the passing pairs (770 of 1529);
none of the surviving pairs passed on a number alone.

One-term prompts were not relaxed: 135 of 774 real prompts (17 %) had a
single relevance term, and every one was conversational ("progress",
"done", "go ahead", "continue", "image"), not a topic query.

## Measurements

### A. Blind relevance judgement (precision)

100 random real prompts (seed 20261027). Each prompt was ranked
independently — no session dedup — by v2.16.0 and by this branch against
copies of the same sidecar. The union of (prompt, fact) pairs was shuffled
and labelled relevant / not relevant by a separate model (Claude Sonnet,
high effort) that did not know which binary produced a pair. The labelling
rule: "relevant — the fact would plausibly help answer or act on *this*
message (same topic, project, tool, problem or procedure)".

| | v2.16.0 | this branch |
|---|---|---|
| injected (prompt, fact) pairs | 320 | 120 |
| relevant | 38 | **48** |
| irrelevant | 282 | **72** |
| precision | 11.9 % | **40.0 %** |
| share of all relevant pairs found (57 in the union) | 67 % | **84 %** |
| prompts that got any injection | 100 | 51 |
| prompts that got noise only | 73 | **19** |

Pairs only v2.16.0 produced: 242, of which 9 relevant. Pairs only the branch
produced: 42, of which 19 relevant.

### A2. Blind relevance judgement inside replayed sessions

Measurement A credits each prompt with its full top-K. In a session the
once-per-context dedup decides what actually lands, so the same blind
procedure was repeated on what the session replay (B) really injected:
100 random prompts that got an injection from either binary, 256 pairs.

| | v2.16.0 | this branch |
|---|---|---|
| injected pairs | 198 | 183 |
| relevant | 33 | **43** |
| irrelevant | 165 | **140** |
| precision | 16.7 % | **23.5 %** |
| prompts that got noise only | 47 | **39** |

Split by prompt origin — 106 of the 256 pairs come from one kind of machine
prompt, the security-review `claude -p` run a Stop-hook plugin starts after
every turn ("Review this change for security vulnerabilities…", a list of
changed files plus code):

| | v2.16.0 | this branch |
|---|---|---|
| human prompts: relevant / irrelevant | 12 / 81 | **22 / 57** |
| human prompts: precision | 12.9 % | **27.8 %** |
| machine prompts: relevant / irrelevant | 21 / 84 | 21 / 83 |
| machine prompts: precision | 20.0 % | 20.2 % |

On human prompts the branch roughly doubles precision. It does nothing for
the machine prompts: a long prompt full of identifiers shares two terms with
almost any fact, so they now carry most of the remaining noise (83 of 140).

### B. Session replay (volume)

165 sessions, 776 prompts, replayed in order — SessionStart, then every
prompt with the real once-per-context dedup — by both binaries, each against
its own copy of the sidecar and WAL (`CLAUDE_MEMORY_DIR`), with the live WAL
size checked unchanged afterwards.

| | v2.16.0 | this branch |
|---|---|---|
| facts injected at SessionStart | 522 | 526 |
| facts injected on prompts | 764 | 765 |
| bytes injected on prompts | 1.80 MB | 1.74 MB |
| prompts with no injection | 472 | 449 |

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
- `go test -race ./...` green. `staticcheck` v0.8.2 is clean on the changed
  packages; the Makefile's pinned v0.7.0 cannot load Go 1.27 export data
  ("export data version 5 is greater than maximum supported version 2") —
  a toolchain issue, independent of this change.

## Caveats

- One model's labels, judged from a 500-character message and a fact
  summary.
- Measurement A ranks each prompt alone, so SessionStart priming is not
  credited. The 9 relevant facts the branch dropped were all "where we left
  off" / project-audit notes answering content-free follow-ups ("yes, push
  both, the second PR as a draft"). In a real session these are typically
  already in context from the ungated SessionStart.
- The `cite-useful` ground truth inside the replayed sessions was too small
  (4 facts) to measure recall from citations.
- Remaining noise is dominated by long machine prompts — headless
  `claude -p` security-review runs that list changed files and code (A2).
  Besides noise, this spends memory budget on every such run. Possible
  follow-up: recognise automated / headless prompts and skip them like
  `source: system`. Not attempted here: how to detect them reliably from the
  hook envelope or environment still needs checking.

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

## Reproduce

```sh
# dry run, nothing persisted (no session_id)
printf '{"cwd":"%s","prompt":"%s"}' "$PWD" "<prompt>" \
  | CLAUDE_PROJECT_DIR="$PWD" memoryctl inject --event=UserPromptSubmit

# isolated replay: copy the sidecar + WAL, point the binary at the copy
mkdir -p /tmp/hm && cp ~/.claude/memory/.sidecar.db ~/.claude/memory/.wal /tmp/hm/
CLAUDE_MEMORY_DIR=/tmp/hm memoryctl inject --event=UserPromptSubmit < envelope.json
```
