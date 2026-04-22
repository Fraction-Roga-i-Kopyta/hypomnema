# Architecture

Map of how hypomnema turns Claude Code hook events into memory retrieval
and back into memory growth. Read this before editing a hook; refer to
it when reviewing a PR that touches control flow.

The code side of truth is `hooks/`, `hooks/lib/`, and `internal/` (Go).
This document explains *how they fit together*; it does not replace the
code or the per-release CHANGELOG.

## The five hook events

Claude Code emits five hook events. Hypomnema binds one script to each:

| Event | Script | Purpose | Size |
|---|---|---|---|
| `SessionStart` | `hooks/session-start.sh` | Scan memory, score, inject top-N into context | ~700 lines, critical path |
| `UserPromptSubmit` | `hooks/user-prompt-submit.sh` | Substring trigger match + parallel FTS5 shadow | ~300 lines, reactive |
| `PreToolUse:Write` | `hooks/memory-dedup.sh` | Fuzzy dedup via `memoryctl dedup check` | Thin wrapper |
| `Stop` | `hooks/session-stop.sh` | Evidence match + outcome detection + lifecycle rotation | ~700 lines, feedback loop |
| `PreCompact` | `hooks/memory-precompact.sh` | Warn "save insights before context compression" | 45 lines, nudge only |

All five hooks are installed as symlinks under `~/.claude/hooks/` by
`install.sh`. Settings.json wires them by filename into the hook
dispatcher.

## End-to-end data flow

```
SessionStart ────────────────────────────────────────────────┐
   ▼                                                          │
   detect_project (projects.json, longest-prefix match)       │
   detect_domains (projects-domains.json + CWD heuristics)    │
   scan memory/{mistakes,strategies,feedback,knowledge,       │
                decisions,notes,projects,continuity}          │
   score_records:                                             │
     keyword_hits×3 + project_boost + wal_bonus(0-10)         │
     + strategy_bonus + tfidf(body, capped) − noise_penalty   │
   filter:                                                    │
     status ∈ {active, pinned}                                │
     domains ∩ current domains                                │
     project ∈ {current, global}                              │
     recurrence ≥ threshold                                   │
   select:                                                    │
     3 global + 3 project mistakes (hardcoded)                │
     12/10/8-slot flex pool for other types (adaptive         │
       based on keyword signal strength)                      │
   inject as `# Memory Context` block in additionalContext    │
   WAL: inject event per file (Hebbian access frequency)      │
   perl -pi: frontmatter[referenced, ref_count] updated       │
   background:                                                │
     regen-memory-index.sh (writes MEMORY.md for              │
       auto-memory directory, not core hypomnema)             │
     /tmp/.claude-session-<sid> marker (timestamp anchor      │
       for Stop hook)                                         │
                                                              │
UserPromptSubmit (per prompt) ────────────────────────────────┤
   ▼                                                          │
   substring trigger match:                                   │
     for each file.triggers:                                  │
       if phrase ∈ prompt                                     │
       and no negation in ±40-char window                     │
       (ru: не/без/уже/нет/нету/игнор,                        │
        en: already/fixed/skip/no/don't/ignore/without):      │
         → candidate                                          │
   priority key (stable sort, descending on each level):      │
     project_rank → status (pinned > active) →                │
     severity → recency(by created) →                         │
     log₁₀(ref_count) → recurrence → raw ref_count            │
   inject top MAX_TRIGGERED (default 4)                       │
   WAL: trigger-match                                         │
   parallel (detached, fail-safe):                            │
     memoryctl fts shadow <prompt> <session> <injected-list>  │
     BM25 body match over SQLite FTS5 index                   │
     WAL: shadow-miss for files FTS found that substring      │
       pipeline missed — observational, not injected          │
                                                              │
Claude responds (0 or more turns) ────────────────────────────┤
                                                              │
PreToolUse → Write to mistakes/*.md ──────────────────────────┤
   ▼                                                          │
   memoryctl dedup check <file> (Go, required since v0.10.0)  │
     parse root-cause from proposed content                   │
     scan existing mistakes/*.md                              │
     token_set_ratio (rapidfuzz semantics, pure-Go port)      │
     ≥80% similarity: exit 2 (block), WAL dedup-blocked       │
     70-80% similarity: allow + WAL dedup-candidate           │
     <70%: silent allow (no WAL event)                        │
                                                              │
PostToolUse → Write to mistakes/*.md ─────────────────────────┤
   ▼                                                          │
   memoryctl dedup check again (same binary, different flag)  │
     if ≥80%: increment existing.recurrence, delete new file, │
              WAL dedup-merged                                │
     else: allow                                              │
                                                              │
PreCompact (before context compression) ──────────────────────┤
   ▼                                                          │
   systemMessage: "save insights before compression"          │
   stronger message if nothing written this session           │
   one-way nudge — cannot block compaction                    │
                                                              │
Stop (session end) ───────────────────────────────────────────┤
   ▼                                                          │
   read ASSIST_TEXT (aggregated during session)               │
   for each injected file:                                    │
     evidence = frontmatter[evidence:] array                  │
               || evidence_from_body (4+ char tokens,         │
                  stop-words filtered, UTF-8 aware)           │
     threshold = 1 (frontmatter) || 2 (body fallback)         │
     hits = phrases matched in ASSIST_TEXT (case-insensitive  │
            substring)                                        │
     hits ≥ threshold → WAL trigger-useful                    │
     hits < threshold → WAL trigger-silent                    │
     evidence empty  → WAL evidence-empty + trigger-silent    │
   error-detected-{low,med,high} per tool-error pattern       │
   outcome detection (cross-ref injected mistakes):           │
     ¬error-detected(slug) → outcome-positive                 │
     error-detected(slug)  → outcome-negative                 │
     new mistake file written this session → outcome-new      │
   clean-session (0 tool errors, non-trivial duration)        │
   cascade-review (2+ errors from related mistakes)           │
   strategy-used (injected strategy + clean session)          │
   strategy-gap (clean session, no strategy injected)         │
   rotation:                                                  │
     age > stale_days && status=active → status=stale         │
     age > archive_days && status=stale → mv to archive/      │
     (stale/archive days per-type, decay_rate overrides)      │
   reminders (appended to additionalContext):                 │
     CONTINUITY if last continuity update is stale            │
     ERRORS if tool errors detected                           │
     STRATEGIES if clean session > 10 min                     │
   background: _agent_context.md regenerate for subagents     │
   ───────────────────────────────────────────────────────────┘
```

## Two scoring pipelines

The code has **two independent scoring systems**. They are not
interchangeable — they solve different problems.

### SessionStart: composite score (recall-optimised)

```
score = keyword_hits × 3
      + project_boost × 1              (file.project == current project)
      + wal_spaced_repetition (0–10)   (Hebbian access bonus)
      + strategy_bonus (min(success_count × 2, 6))
      + tfidf_body_match × 2 (capped)  (only for records with no keywords)
      − 3 if record matches no active signals (noise penalty)
```

Applied after filtering. Feeds into adaptive quotas. Goal: cast a wide
net, then prune to what's contextually relevant.

Source: `hooks/lib/score-records.sh`.

### UserPromptSubmit: priority key (precision-optimised)

Stable lexicographic sort, descending at each level:

```
1. project_rank     (current > global > other project)
2. status           (pinned > active)
3. severity         (critical > major > minor > unset)
4. recency(created) (≤7d > ≤30d > ≤90d > older)
5. log₁₀(ref_count) (diminishing-returns bucket: 0, 1, 2, 3+)
6. recurrence       (higher wins)
7. ref_count        (raw value, final tiebreaker)
```

Applied to substring-trigger matches only. Deterministic ordering.
Goal: if a trigger fires, the injection is "reliable in the strong
sense" — same prompt, same match, same ranking, every time.

Source: `hooks/user-prompt-submit.sh`.

### Why two

Context assembly (SessionStart) wants relevance, including partial
matches. Reactive matching (UserPromptSubmit) wants determinism, so
replay tools like `scripts/replay-runner.sh` can measure precision
reliably. Conflating them breaks one or the other.

## Library structure (`hooks/lib/`)

Hooks are thin glue. Heavy logic lives in 8 pure-function libraries:

| File | Purpose | Lines |
|---|---|---|
| `build-context.sh` | Context assembly for SessionStart | 353 |
| `parse-memory.sh` | Frontmatter parser + file lookup | 225 |
| `score-records.sh` | Scoring formulas (SessionStart) | 128 |
| `evidence-extract.sh` | Evidence phrases (frontmatter + body fallback) | 96 |
| `wal-lock.sh` | Mkdir-atomicity lock on WAL writes | 73 |
| `load-config.sh` | **Safe parser** for `.config.sh` (not source) | 68 |
| `stat-helpers.sh` | BSD vs GNU stat portability | 32 |
| `detect-project.sh` | Longest-prefix project slug | 28 |

All are explicitly marked as pure functions with no side effects (except
`wal-lock.sh`, which manages a filesystem lock). Each can be unit-tested
by sourcing it with a mock `$MEMORY_DIR`.

`load-config.sh` is a security-critical design choice: `.config.sh` is
writable by any process with access to the memory directory, including
Claude itself via the Write tool. `source`-ing it would let a malicious
or mistaken Claude put arbitrary code in `.config.sh` and have it run
at every session start. The safe parser only extracts integer
assignments and rejects anything else.

## WAL event taxonomy

The write-ahead log (`~/.claude/memory/.wal`) is the sole observability
surface. Every hook emits events; nothing reads them except
`self-profile.md` aggregation and `shadow-miss` tuning.

| Class | Events |
|---|---|
| Observability | `inject`, `cluster-load`, `trigger-match`, `shadow-miss` |
| Feedback loop | `trigger-useful`, `trigger-silent`, `evidence-empty` |
| Outcomes | `outcome-positive`, `outcome-negative`, `outcome-new` |
| Session meta | `session-metrics`, `clean-session`, `strategy-used`, `strategy-gap`, `cascade-review`, `error-detected-{low,med,high}` |
| Lifecycle | `rotation-summary`, `rotation-stale`, `rotation-archive`, `strategy-rescored` |
| Dedup | `dedup-blocked`, `dedup-merged`, `dedup-candidate` |

Invariants:

- **Append-only.** Nothing in hypomnema rewrites WAL lines. Corrections
  are additions, not edits.
- **Locked.** `wal-lock.sh` uses `mkdir` atomicity (POSIX, no `flock`
  dependency) so concurrent hook processes don't interleave partial
  lines.
- **Pipe-delimited, 4 fields.** `YYYY-MM-DD|event-type|slug-or-data|session-id`.
  Session id sanitised (`|` → `_`) to prevent field-count corruption.

Event format is not versioned. Schema changes require a coordinated
cutover documented in `docs/MIGRATION.md`.

## Go binary (optional hot path)

`memoryctl` is an optional Go binary built via `make build`. It
delegates hot paths that are bad fits for bash: SQLite FTS5 access,
UTF-8-correct token filtering, single-pass WAL aggregation.

| Subcommand | Bash fallback | Ported in |
|---|---|---|
| `dedup check` | **None** (required since v0.10.0) | v0.10.0 (from Python) |
| `fts shadow` / `fts sync` / `fts query` | `bin/memory-fts-*.sh` | v0.9.0 |
| `self-profile` | `bin/memory-self-profile.sh` | v0.10.0 |

**Contract:** byte-for-byte output parity between bash and Go
implementations, enforced by `scripts/parity-check.sh`. CI runs parity
checks on every push. Breaking parity is a release blocker.

The bash fallbacks exist so that:

- Users who never ran `make build` still get a working system
  (minus dedup, since v0.10.0).
- Hook internals remain readable without going through Go code.
- Parity-check acts as a dual-implementation oracle — if Go and bash
  disagree, one of them is wrong.

## Invariants (code-level contracts)

These are the rules that shouldn't break silently. Breaking one is
either a bug or an explicit architectural decision that needs an ADR
in `decisions/`.

1. **Body is agent-owned, frontmatter auto-fields are hook-owned.**
   Hooks run `perl -pi -e` only within the frontmatter block (guarded
   by `$in_fm` flag) and only touch `referenced`, `ref_count`,
   `status`. Body text is never modified by a hook.

2. **Write-by-agent, read-by-hook.** Hooks never create new memory
   files. Agents (Claude) create them via the Write tool. Hooks only
   react to existing files.

3. **Append-only WAL.** Never rewrite; corrections are new events.

4. **Parity contract.** Bash and Go implementations of the same
   subcommand produce byte-for-byte identical output.

5. **No network calls.** Zero `curl`/`wget`/`net/http` in `hooks/`,
   `internal/`, or `cmd/`. Hypomnema is entirely offline.

6. **No secret-material in `.config.sh`.** It's parsed, not sourced,
   to prevent arbitrary-code execution via Write-tool content.

## Known sharp edges

Architectural debt worth knowing before touching these files:

- **`session-stop.sh` is 700 lines and mixes evidence match,
  outcome detection, rotation, and reminders.** Decomposition to
  `hooks/lib/` is partially done (`evidence-extract.sh`) but not
  finished. A split into `outcome-detection.sh`, `rotation.sh`,
  `reminder-builder.sh` would help.

- **Two scoring systems are not documented side-by-side.** A reader
  has to find `score-records.sh` and `user-prompt-submit.sh`
  independently to understand the contrast. This document is the
  first place they appear together.

- **Reminders (CONTINUITY / ERRORS / STRATEGIES) are hardcoded in
  Stop.** Cannot be disabled independently. Candidate for
  configuration in `.config.sh`.

- **Project slug derivation** is done in multiple places
  (`detect-project.sh`, `regen-memory-index.sh`). v0.10.1 unified
  the path-derivation convention (`-$(echo $PWD | tr / -)`) but
  older paths may still assume the hardcoded form.

## Cross-references

- Directory-level layout of `~/.claude/memory/` and decay policy:
  `CLAUDE.md` (project root).
- Frontmatter field schema per type: `CLAUDE.md` → `docs/FORMAT.md`.
- Hook contract surface (inputs, outputs, exit codes):
  `docs/hooks-contract.md`.
- Tunable parameters: `docs/CONFIGURATION.md`.
- Decision records: `decisions/` in the user's memory dir; template
  in `templates/decision.md`.
- Release discipline: `docs/maintenance/RELEASE_CHECKLIST.md`.
