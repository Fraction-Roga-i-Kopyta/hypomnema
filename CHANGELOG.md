# Changelog

## [2.9.0] — 2026-07-08

Ranker quality (internal-audit P3a): two of the ranking loops the external and
internal reviews flagged. Redeploy the binary; the sidecar recomputes
effectiveness on the next reproject.

### Fixed

- **Stopwords are filtered from relevance tokens.** The `LoadStopwords` path was
  dead code — every tokenization ran with no stopword set, so overlap (weight
  3.0) counted common function words and a fact stuffed with them out-ranked a
  genuinely relevant one. A built-in en+ru function-word set
  (`tokenize.DefaultStopwords`) is now applied via `tokenize.Relevance()` at
  every relevance site (keyword population + query: prompt, cwd, git context,
  recall, rank), so a stopword can't enter overlap on either side. Content words
  are untouched (corpus-common *content* terms are IDF's job, deferred).
- **Ambient rules are no longer penalized for silence.** `precision_class:
  ambient` (language prefs, security baselines, meta-policy) marks rules that
  shape behaviour without being cited. Their `trigger-silent` events were
  counted as negative evidence, spiralling effectiveness down until `effGate`
  damped their ranking — the "poor get poorer" loop. Ambient rules now hold
  at/above the neutral prior; explicit useful citations still raise them.

### Deferred (design cycle)

- IDF weighting of overlap (changes the score distribution — needs A/B) and
  decoupling recency from mere injection (entangled with the honest
  usefulness-signal rework, P3b) remain open.

## [2.8.0] — 2026-07-08

Replaces the WAL lock with `flock(2)` (internal-audit design item C1). This is
the first of the deferred design changes. Behaviour is unchanged in the normal
case; the fix is in the crash/contention edges the old mkdir lock couldn't get
right.

### Fixed

- **WAL lock uses `flock(2)` instead of a mkdir lock.** The mkdir lock recovered
  an orphaned lock via a staleness heuristic that had three audit-confirmed
  defects: a microsecond free window during stale-takeover permitting a brief
  double-acquire (C1); a `Release` that could delete a lock another process had
  legitimately taken over after the holder stalled (C2); and a partial
  `LockConfig` (`{MaxWait}` alone) that left `StaleAfter` at 0 and made a fresh
  lock instantly stealable (C3). `flock` eliminates all three by construction —
  the kernel drops the lock when the holding process exits, so there is no
  staleness to guess at and no takeover to race.

### Notes

- The lock at `~/.claude/memory/.wal.lockd` is now a regular file, not a
  directory; a legacy lock directory left by ≤ v2.7.x is migrated automatically
  on the next acquire. The `Acquire`/`AcquirePath`/`Release`/`LockConfig` API is
  unchanged (`LockConfig.StaleAfter` is retained for compatibility but ignored).
- `flock(2)` is advisory and reliable on local filesystems (where `~/.claude`
  lives); Linux and macOS only, matching the project's supported targets.

## [2.7.1] — 2026-07-08

The deferred local bugfixes from the internal audit — correctness fixes with no
format or behavior-semantics change. (The deeper design items — WAL project
attribution for a composite key, the flock lock rewrite, and the ranker's
usefulness-signal rework — remain deferred to their own design cycle.)

### Fixed

- **Overlap query is chunked** (audit E6). `OverlapScores` built one `IN(...)`
  over every query term; a large prompt plus `git status` could tokenize past
  SQLite's 32766 bound-variable limit, which errored and got swallowed in the
  injection path — silently zeroing all keyword overlap. The list is now
  chunked at 30000, and injection degrades to empty overlap explicitly on any
  query error instead of dropping it.
- **`migrate` no longer claims "sidecar rebuilt" when it wasn't.** The sidecar
  rebuild step is a rebuildable projection, so its failure doesn't fail the
  migration — but `Execute` now returns whether it succeeded and the CLI warns
  you to run `memoryctl sidecar rebuild` instead of reporting success.
- **`doctor` smoke-tests the binary.** `memoryctl_available` verified only that
  a file existed at `~/.claude/bin/memoryctl`; a symlink to a text file read as
  healthy. It now checks the exec bit and actually runs `memoryctl --help`.

### Changed

- The sidecar `keyword` reproject now persists the `domains` CSV into each row
  (audit E7, data half). This does **not** activate domain filtering — the
  ranker's domain filter stays inert (`Query.Domains` is empty); wiring it is a
  behavior change left to the design cycle.

## [2.7.0] — 2026-07-08

Documentation release: the "burn v1 out of the docs" pass (internal-audit
Priority 2). No code or behavior change — but the docs finally describe the
system that actually ships. Before this, ~9 of 10 `docs/` files described the
deleted v1 bash system in the present tense, a fresh `git clone` following
`QUICKSTART` could not install (the binary is built, not checked in, and the
docs called the build "optional"), and the shipped templates/seeds produced
files that violate the v2 memory schema.

### Documentation

- **README + QUICKSTART**: `make build` is documented as the required first
  step; 6 shims/hooks (not 4); ranker formula includes `effGate`; install
  section matches real behavior; singular `type:` values + `skill-learning`;
  the "8–20×" A/B number carries its circularity caveat.
- **ARCHITECTURE / EVENTS / hooks-contract / FORMAT**: rewritten from v1
  (700-line bash hooks, `hooks/lib/`, FTS5, negation windows, two pipelines,
  `.config.sh`, PreCompact, quotas, 9-directory tree) to the real v2 (6 thin
  shims, one merged ranker, flat native + global stores, actual WAL vocabulary,
  the v2 frontmatter contract). FORMAT.md §5 (WAL grammar) kept — it matches code.
- **CONFIGURATION / TROUBLESHOOTING / FAQ**: deleted the fictional `.config.sh`
  and nonexistent verbs (`decisions review`, `evidence learn`); documented the
  real env knobs and decay thresholds; centered troubleshooting on
  `memoryctl doctor`; stated honestly that v2 has no WAL compaction.
- **INVARIANTS / MIGRATION / ADRs**: corrected the automation status (I1 CI
  WAL-validate, I3/I5 Go checkers); removed the nonexistent `migrate --auto`
  flag and the fictional doctor parity check; indexed all 15 ADRs and flipped 7
  retired-mechanic ADRs to `superseded`.
- **SECURITY / CONTRIBUTING**: named the real v2 mitigations (guard
  path-boundary, `internal/secrets` gate with the v2.5.1 pattern set,
  `.secretsignore` + `HYPOMNEMA_ALLOW_SECRETS`); real build/test/lint/CI workflow.
- **templates/ + seeds/ + skills/memory-system.md**: regenerated to valid v2
  frontmatter (required `name`/`description`/`type`; no v1 `triggers`/`seed`/
  `decay_rate`/`ref_count`/`project`). Verified via the real collector — all 15
  files parse as valid v2 native memory. The `memory-system` skill no longer
  mis-trains agents with v1 mechanics.

## [2.6.0] — 2026-07-08

Effectiveness-signal data-quality release (internal-audit cluster E1–E5). The
self-profile metrics and cross-project overlap that the Bayesian ranker rests
on were being computed from corrupted inputs — inflated ~20x by per-turn
duplication, blind to ambient rules, poisoned by giant transcript lines and
missing transcripts, and clobbered across same-named files in different
projects. This corrects **what gets measured**, so effectiveness/precision now
reflect reality. Redeploy the binary; the sidecar auto-rebuilds on the next
inject/close (schema bumped v2→v3).

### Fixed

- **Self-profile counts sessions, not turns** (E2). `close` writes a
  session-metrics + trigger rows every turn, so per-event counters inflated
  `total sessions`, `silent-noise`, and `measurable precision` roughly 20x
  (e.g. 1657 rows for 79 real sessions). Each (slug, session) is now classified
  once, useful winning over silent within a session.
- **Ambient rules are recognized** (E1). Trigger events carry a `.md`-suffixed
  slug (`close` writes `f.Slug`) while ambient slugs are bare, so the ambient
  lookup always missed and `ambient activations` read 0. The lookup now
  normalizes the suffix; the `ambient_fraction` denominator switched to the
  per-session measurables to match its numerator.
- **A huge transcript line no longer truncates the scan** (E3). `decodeStream`
  read with a 4MB-capped `bufio.Scanner`; a larger line (a giant `tool_result`)
  stopped the scan and silently dropped every assistant line after it —
  fabricating "silent" for facts cited later. It now reads with
  `bufio.Reader.ReadBytes`, skipping oversized lines but continuing.
- **A missing/unreadable transcript skips classification** (E4). An unreadable
  transcript left the assistant text empty, which `Classify` scored as "every
  injected fact was silent" — fabricated negative evidence for the whole
  session. `close` now only classifies when the transcript actually read;
  session-metrics/close are still recorded.
- **Cross-project keyword clobber** (E5). The `keyword` table gained a `project`
  column; `PopulateKeywords` clears rows scoped by (slug, project). A Reproject
  for one project (fired on every Stop of any project) no longer wipes another
  project's keyword rows for a shared basename slug (`continuity.md`/`notes.md`
  exist in every project), which had zeroed the other project's overlap.

### Known residual

- The deeper cross-project effectiveness poisoning — the `memory` table's
  bare-slug primary key plus WAL events that carry no project — still merges
  same-named files' history across projects. A true composite key needs project
  attribution in the WAL (a format change) and is deferred to its own release.

## [2.5.1] — 2026-07-08

Hardening release from an internal audit (nine independent review lenses, each
finding empirically reproduced). It closes residual holes in the very things
v2.5.0 hardened — the secret gate and install/doctor — plus new
harness-contract, concurrency, and operational defects. Five of the fixes are
regressions v2.5.0 itself introduced. No format or schema changes; redeploy the
binary and re-run ./install.sh.

### Fixed

- **Secret gate — snake_case / SCREAMING_SNAKE keys** (`db_password`,
  `client_secret`, `access_token`, `AWS_SECRET_ACCESS_KEY`) now match; the
  `\b`-anchored keyword only caught bare/camelCase keys, letting the canonical
  config/env form through — the single biggest remaining hole.
- **Secret gate — URL false positive** (regression from v2.5.0): the
  connection-string pattern no longer flags ordinary dev URLs
  (`http://host:5173/@vite/client`, npm registry with a port, callback URLs with
  an email); a password may not span `/`.
- **Secret gate — a code-fence delimiter/info-string line is now scanned** (a
  secret parked as ```` ```<secret> ```` rendered as an empty block but sat in
  plaintext), and `~~~` fences are handled like backtick fences.
- **Secret gate — `.secretsignore` over-broad patterns** (`*`, `**`, `**/*`)
  are ignored, so an agent can't self-disable the gate with one non-secret write.
- **`memoryctl guard` scans MultiEdit `edits[].new_string` and NotebookEdit
  `new_source`**, not just Write `content` / Edit `new_string` (was fail-open).
- **Injection envelope budget** now holds: emitted with HTML-escaping off, so
  `< > &` stay one byte instead of six — a code/markup-heavy fact at the 8KB
  markdown budget no longer produces a ~2x envelope that Claude Code diverts to
  a file the model never reads inline. `skill-inject` gained the same total
  budget (it had none).
- **`close` writes a non-empty session id (`unknown`)** when the hook envelope
  omits one, instead of empty-session WAL rows that `wal validate` rejects.
- **WAL lock ownership token**: a holder that stalled past the stale threshold
  and was taken over can no longer delete the new holder's live lock on Release;
  partial `LockConfig` values default each field independently (a `{MaxWait}`
  alone no longer makes every lock instantly stealable).
- **Corrupt-sidecar recovery only wipes on genuine corruption** (regression from
  v2.5.0): a transient `SQLITE_BUSY` from a concurrent process no longer deletes
  a live sidecar. Concurrent first-open of a fresh sidecar retries the schema
  DDL and seeds the version row idempotently instead of failing fast.
- **Atomic writes** (tmp+rename via `pathutil.WriteFileAtomic`) for the
  session injected-list and the dedup merge rewrite — a Stop-hook reader no
  longer sees a truncated list, and a crash can't truncate a native memory file.
- **doctor** parses settings.json (an invalid file, which Claude Code can't load,
  now FAILs instead of reading OK) and verifies each shim is wired under the
  right event and is a regular file (a directory carrying exec bits no longer
  passes).
- **install.sh** registers hooks under `$CLAUDE_DIR` (not a hardcoded `$HOME`),
  so a custom-dir install wires the right tree; `--patch-claude-md` writes v2
  instructions (flat stores + `type:`) instead of v1 subdirectory paths that
  silently lost memory; a dangling shim symlink no longer aborts the installer
  mid-run; the settings gate is skipped under `--skip-base`; the v1-upgrade
  hint fires on a real v1 store (subdirectories), not the always-present runtime
  dir.
- **uninstall.sh** strips the CLAUDE.md section it added, matches only the six
  known shim names (preserving a user's own `hooks/v2` hook), removes runtime
  state (`.wal`/`.sidecar.db`/`.runtime`/`self-profile.md`) under
  `--purge-memory`, and routes dry-run notices to stderr.
- **`migrate --rollback`** restores the newest `memory.v1-backup-*` instead of
  deriving today's path — rollback worked only on the migration day.
- **Frontmatter block scalars** (`root-cause: |`) no longer clobber real keys:
  an indented `description:`/`name:` inside the block is treated as continuation.

### Changed / CI

- `make test-go-cover` runs the subprocess pass with `-count=1`, so warm-cache
  runs actually write the coverage profile instead of silently reporting an
  absent file.
- `hooks/v2/shims_test.sh` now exercises the secret-gate shim (`pre-tool-write.sh`).
- The I3 atomic-write invariant checker walks `cmd/` as well as `internal/`.

### Known residual

- The WAL lock's stale-takeover has a microsecond free window that can permit a
  brief double-*acquire* (bounded by the ownership token + atomic `O_APPEND`, so
  it cannot corrupt the WAL). A full fix requires moving to `flock(2)` and is
  deferred.

## [2.5.0] — 2026-07-08

Hardening release closing the Priority-1 findings of the 2026-07-08 external
review: the secret-gate P0 (quoted values bypassed detection) plus the
operational false-OKs in install/doctor, non-transactional Reproject,
unbounded git calls, and missing CI gates. No format, schema, or API changes;
redeploy the binary and re-run ./install.sh.

### Fixed

- **Secret gate detects quoted keys and values** (P0). `password: "…"`,
  `api_key = '…'`, `"token": "…"` previously produced zero hits — the value
  regex required a non-quote first character. One optional quote is now
  allowed around the key and before the value.
- **An unclosed code fence no longer disables secret scanning to EOF.** Orphan
  fence openers are treated as plain text; only properly paired fences keep
  the code-block exemption.
- **Reproject runs in a single transaction.** Concurrent injects never see a
  half-rebuilt sidecar, a crash mid-rebuild cannot leave `outcome` empty, and
  one commit replaces thousands of per-statement fsyncs on every Stop hook.
- **Git calls in keyword extraction are bounded (500ms total + WaitDelay).**
  A git hung on a lock or network mount — or a child process holding the
  output pipe — degrades to "no git signal" instead of stalling the
  SessionStart hook.
- **Corrupt-sidecar recovery wipes `-wal`/`-shm` siblings**, matching the
  schema-mismatch path — a poisoned WAL file no longer re-corrupts the fresh DB.
- **install.sh validates settings.json up front and fails loudly.** A corrupt
  settings.json previously let the installer print "Done" with zero hooks
  registered (reproduced in sandbox). The Claude Code version gate now also
  runs before any file is copied, hook registration dies loudly on jq
  failure, and `--dry-run` no longer trips an arithmetic syntax error on
  stale symlinks.
- **"Next steps" recommends `memoryctl doctor`** — `memoryctl status` never existed.
- **uninstall.sh removes all six shims** (the two Skill shims were left behind).
- **doctor verifies shim files exist and are executable** (`shim_files_present`);
  substring-matching settings.json alone reported a wiped hooks/v2/ as healthy.
- **Lock tests use 500ms+ wall-clock budgets** — the 60–80ms budgets were the
  source of irreproducible CI flakes.

### Added

- **Value-based secret patterns**: AWS `AKIA…`/`ASIA…`, GitHub `ghp_…`/
  `github_pat_…`, Anthropic `sk-ant-…`, Slack `xox*-…`, PEM private-key blocks,
  JWTs, and `scheme://user:pass@` connection strings.
- **`scripts/install_test.sh`** — sandboxed install/uninstall integration test
  (corrupt settings, dry-run upgrade, full shim lifecycle); runs in CI on both OSes.
- **CI gates**: `hooks/v2/shims_test.sh`, the install sandbox test, a WAL
  grammar check against `fixtures/wal-clean/`, and staticcheck (`make lint`,
  pinned v0.7.0 / 2026.1).

### Removed

- **`install.sh --discover`.** The wizard called `memoryctl project add`, which
  has never existed; v2 derives per-project stores from the working directory,
  so there is nothing to register. The flag now exits with an explanation.

## [2.4.0] — 2026-06-28

Earned-popularity ranking. The `ref_count` term in the injection ranker is now
gated by effectiveness, so a fact injected hundreds of times that rarely proved
useful can no longer coast on volume. Diagnosed from the live corpus: notes with
effectiveness 0.03–0.20 and ref_count in the hundreds (generic
`debugging-approach`, `design-system-migration-claude-ai-handoff`,
`parallel-worktree-subagent-migration`) were out-ranking proven facts purely on
injection count. Ranking-only change — no format, schema, or API impact; redeploy
the binary to pick it up.

### Changed

- **`ref_count` reward is gated by effectiveness.** The popularity term is now
  `1.0 × log10(1+ref_count) × effGate`, where `effGate = clamp(2×effectiveness,
  0, 1)`. The gate is neutral (1.0) at the Bayesian prior 0.5 and capped at 1.0,
  so it only *damps* unearned popularity — it never amplifies a high-effectiveness
  fact's volume reward. Zero-safe by construction: an extreme low effectiveness
  requires substantial negative evidence (the `(pos+1)/(pos+neg+2)` prior holds
  new and low-evidence facts near 0.5), so a fresh fact's ref reward is untouched
  and only well-evidenced under-performers are suppressed. Overlap, recency, and
  the standalone effectiveness term are unchanged, so no candidate is annihilated
  (spec §3.3). This subsumes "generic strategy demotion": evergreen-but-rarely-
  cited notes fall by data, not by a name blocklist, while a proven generic note
  (e.g. `verification-before-done`, eff 0.53) keeps its rank.

## [2.3.0] — 2026-06-15

Self-improving skills (Phase 1). Skills accrete usage-learned knowledge as a
non-destructive memory layer — a new `skill-learning` type, keyed by a `skill:`
field — injected into context when the skill activates. `SKILL.md` is never
modified. Capture is semi-manual in Phase 1; auto-distillation of recurring
patterns is Phase 2. Redeploy the binary and re-run `./install.sh` to register
the two new `Skill`-matched hooks, then restart Claude Code.

### Added

- **`type: skill-learning` memory type.** A `skill:` frontmatter field binds a
  learning to a skill name. Parsed into `native.MemFile.Skill`, counted in the
  Bayesian corpus gate, and decayed on a 120-day threshold (durable). Lives in
  the global store, keyed by skill — a learning applies wherever that skill runs.
- **`memoryctl skill-inject` verb.** Reads a `PostToolUse(Skill)` hook envelope
  from stdin, retrieves the activated skill's learnings (query-less, ranked by
  effectiveness+recency, top-5 with the 2.5KB-per-body injection cap), and emits
  them as `additionalContext`. Records a `recall` WAL event per delivered
  learning keyed to the stdin `session_id`, so the close loop attributes
  effectiveness to the right session. Fail-safe: silent exit 0 on empty/unknown
  skill or when the skill has no learnings yet.
- **`memoryctl skill-active` verb.** Reads a `PreToolUse(Skill)` envelope and
  writes a session-scoped active-skill marker under `~/.claude/memory/.runtime/`
  (atomic tmp+rename; filename sanitized via `pathutil.SafeFileName`) so
  semi-manual capture can tag `skill:` reliably. Stale markers age out at close.
- **Two `Skill`-matched hook shims** — `skill-learnings-inject.sh` (PostToolUse)
  and `skill-active.sh` (PreToolUse) — registered by `install.sh` and tracked by
  `doctor`'s `settings_hooks_registered` check.

## [2.2.0] — 2026-06-10

Adds the pull side of retrieval. Injection is push-only — the ranker sees
the user's prompt, not what the agent hits mid-task. `memoryctl recall`
closes that gap: an explicit query against the same ranker, wired into the
same effectiveness loop. No migration needed; redeploy the binary to get
the new verb.

### Added

- **`memoryctl recall <query words...> [--k N]` — pull retrieval.** Ranks
  the current project + global memory against an ad-hoc query and prints
  the best fact's body (2.5KB cap, the injection budget) plus an index of
  runner-ups with file paths (default 6 results). Only the rendered top-1
  counts as delivered: it emits a `recall` WAL event (whole-line dedup —
  same-day repeats are one delivery) and joins the session's injected
  list, so push dedup and close classification cover the pull path too.
  Session id comes from `$HYPOMNEMA_SESSION_ID`, then
  `$CLAUDE_CODE_SESSION_ID` (exported by Claude Code into Bash), with a
  `cli` fallback in the WAL when neither is set. Interactive-verb error
  policy: visible errors, no hook fail-safe.
- **`recall` WAL event.** `Reproject` counts it like `inject` toward
  ref_count and `last_injected` — which is also what revives a stale fact:
  recall includes stale rows (marked `[stale]`), and a recalled one comes
  back to life at the next close.
- **`rank.Query.IncludeStale`.** The pull path widens the status allowlist
  to stale; push injection is unchanged.

### Changed

- **`internal/inject` exports `Candidates`, `CapBody`, `MaxBodyBytes`** —
  thin wrappers so the pull path reuses the push pipeline's candidate
  assembly (scoped, self-healing, degraded fallback) and body budget.
- **Session-list writer is now an honest set-union.** The helper shared by
  inject and recall (`writeSessionList`) deduplicates slugs instead of
  concatenating; the inject path produced disjoint slugs by construction,
  but the contract is now safe for any caller.

## [2.1.0] — 2026-06-09

Closes the five feedback contours a live-install audit found broken in
v2.0: the injection payload never reached the model inline, the shared
sidecar tombstoned other projects' memory, effectiveness was frozen at
its prior, the secrets gate did not cover v2 stores, and ranking
frontmatter was silently discarded. Upgrade note: re-run `./install.sh`
(or set the PreToolUse matcher to `Write|Edit` manually) and expect the
sidecar to rebuild itself once (`schema_version` 1 → 2).

### Added

- **`evidence:` classification is back.** The close-path classifier
  marked a memory useful only when the assistant literally wrote its
  slug or name — the documented `evidence:` phrases (the whole point of
  the feedback type) were ignored since the v2 cutover. Frontmatter
  `evidence:` lists are parsed again and any phrase match counts as
  applied; slug/name citation still counts too.
- **Real session metrics.** The `session-metrics` WAL event carried
  hardcoded zeros; it now reports actual `tool_calls`, `error_count`
  (is_error tool results) and `duration` derived from the transcript
  timestamps.

### Fixed

- **`audit invariants` passes on its own repo and gates CI.** Migration
  file writes go through tmp+rename (I3), the shim test runner is
  explicitly allowlisted for fail-fast (I5), allowlist entries pointing
  at files deleted in the v2 cutover are gone, and the audit now runs
  in the test workflow so violations can't accumulate silently again.
- **Cross-project tombstoning.** The sidecar DB is shared across all
  projects, but `Reproject` treated "current project + global" as the
  whole universe — every `close` marked all other projects' rows
  `deleted`, making their memory invisible to the ranker (on the live
  install all 8 hypomnema project facts were tombstoned while another
  project's continuity note injected 612 times). Reconciliation is now
  scoped: rows are stamped with their owning store (`project` column),
  deletions only reconcile inside `current project ∪ global`, and
  injection candidates are filtered to the same scope, so one project's
  facts no longer leak into another's sessions. The rank `projectBoost`
  is finally wired up (project-local facts outrank global ones on equal
  signals). Sidecar `schema_version` bumped to 2; an old sidecar is
  wiped and rebuilt automatically on first open.
- **Keyword table survives scoped rebuilds.** `PopulateKeywords` no
  longer truncates the whole table (which wiped other projects'
  relevance signal); it clears only the supplied files' rows, and
  tombstoned rows drop their keywords.
- **Effectiveness actually learns again.** The Bayesian effectiveness
  read only `outcome-positive/negative` WAL events, which nothing in v2
  writes — every v2-born fact sat at the neutral 0.5 forever and the
  documented "effectiveness feeds back into ranking" loop was open.
  `Reproject` now also folds in the `trigger-useful`/`trigger-silent`
  (+`trigger-silent-retro`) events `close` already writes, deduplicated
  to one observation per (slug, session) with useful winning over silent
  (close fires every turn). Legacy outcome events still count.
- **Staleness follows use, not file age.** `MarkStale` measures from
  `last_injected` (fallback `created`), so a fact in active rotation no
  longer goes stale purely by calendar — previously even the
  highest-effectiveness feedback rules aged out while unused records
  survived.
- **Ranking frontmatter is parsed again.** The native adapter read only
  `name`/`description`/`type`, silently discarding the documented
  `keywords:`, `domains:`, `created:` and `status:` fields — authors
  were writing the "primary ranking signal" into the void and
  `status: pinned` was unreachable (every close hard-reset rows to
  active). Keywords/domains (inline and block-style lists) now feed the
  keyword table on both the sidecar and degraded paths, frontmatter
  `created` wins over WAL-earliest as the recency claim (a brand-new
  file finally gets fresh recency instead of zero), and `pinned`
  survives reproject and exempts the row from decay.
- **Secrets gate covers what v2 actually stores.** `guard` gated only
  the legacy `~/.claude/memory` tree, while v2 content lives in native
  per-project stores and `~/.claude/memory-global` — the documented
  protection did not apply to a single v2 memory file. The gate now
  covers all three store kinds, the hook matcher registers for
  `Write|Edit` (an Edit could previously slip a credential past it),
  and `.secretsignore` is honoured at its documented location in the
  global store (legacy locations still work).

- **Injection now fits the harness inline limit.** The rendered
  `additionalContext` is capped at 8KB total (per-body cap unchanged at
  2.5KB). Claude Code persists oversized hook output to a file with only
  a ~2KB inline preview, so an uncapped 8×2.5KB payload never actually
  reached the model. Facts cut by the budget are not booked as injected
  and surface on a later prompt.
- **A fact injects once per session.** `inject` now reads the session's
  `.runtime/injected-<sid>.list` and skips already-injected slugs; the
  list accumulates across SessionStart/UserPromptSubmit instead of being
  overwritten by each call. `close` therefore classifies the whole
  session's injected set, not just the last batch, and `ref_count` stops
  growing by mere prompt count. Session lists older than 7 days are
  pruned.
- **Session ids are sanitised before filesystem use**
  (`pathutil.SafeFileName`) — a hostile `session_id` can no longer
  escape `.runtime/`.
- **v2 cutover completion** (landed on main right after the v2.0.0 tag,
  first changelogged here): the orphaned v1 `MEMORY.md` the harness kept
  injecting is regenerated from native, `doctor`/`self-profile`/`dedup`
  read the native stores instead of the deleted v1 tree, and migration
  normalises plural `type:` values.

## [2.0.0] — 2026-05-29

MAJOR. Repositions hypomnema as a governance + ranking layer over
Claude Code's native file memory (v2.1.59+). The self-managed
`~/.claude/memory/` tree is no longer the primary content store.
Existing users on older Claude Code should stay on the v1.1.x tag.

### Breaking changes

- **Native memory required.** Claude Code ≥ 2.1.59 with auto-memory
  enabled is a hard prerequisite. `./install.sh` fails fast with an
  actionable message on older versions; no legacy-store fallback mode.
- **Pure-bash install removed.** `make build` (Go ≥ 1.22) is now
  required. Four thin bash shims replace the hook scripts; all logic
  lives in the `memoryctl` binary.
- **Substring triggers removed.** `triggers:` / `trigger:` frontmatter
  fields are ignored. The ranker uses keyword-overlap over name /
  description / body; prompt tokens are signals, not pattern-match gates.
  Existing files with trigger frontmatter continue to load without error.
- **Two-pipeline scoring removed.** SessionStart and UserPromptSubmit
  now run the same `internal/rank` relevance ranker. The lexicographic
  priority key is gone.
- **FTS5 infrastructure removed.** `index.db`, `memory-fts-shadow.sh`,
  and `memoryctl fts {shadow,sync,query}` are deleted. The Unicode
  tokeniser from `internal/tfidf` is preserved inside `internal/rank`.
- **Cold-start gates removed.** Bayesian effectiveness and the TF-IDF
  vocab gate are gone. Effectiveness is always live; the A/B harness
  validates ranking quality directly.
- **`decision-review-triggers` evaluation removed.** Feature deferred;
  the `review-triggers:` frontmatter fields are parsed but not evaluated.
  `memoryctl decisions review` is not available in v2.0.

### Added

- **`memoryctl inject`** — unified ranked injection for both SessionStart
  and UserPromptSubmit events. `internal/rank` is a pure function
  (no I/O) combining overlap, `log10(1+ref_count)`, recency, and
  Bayesian effectiveness signals. Zero-safe: a new fact with ref_count=0
  is injectable from day one.
- **`memoryctl close`** — outcome attribution, WAL append, decay
  recompute, self-profile regeneration, continuity write.
- **`memoryctl guard`** — secrets-gate + dedup check (PreToolUse:Write).
  Secrets detection ported from bash to Go; fail-open on scanner error.
- **`memoryctl migrate`** — guided one-shot conversion from the v1 store
  to native format. Flags: `--dry-run` (review plan), `--execute`
  (backup + convert + seed sidecar), `--rollback` (restore backup +
  re-register v1 hooks), `--auto` (non-interactive).
- **`memoryctl ab`** — A/B null-hypothesis harness. Replays historical
  WAL sessions, compares ranked top-K recall against random selection.
  Result on maintainer corpus (n=49 sessions): ranked recall is 8–20×
  random at every budget tested (K=3,5,8,12), on static signals alone
  (keyword-overlap component not exercised historically). See
  `docs/measurements/2026-05-29-v2-ranker-ab.md` for the full report.
- **Global store** (`~/.claude/memory-global/`, configurable). Fills
  native's per-project-only gap. Global facts (e.g. `language-always-russian`)
  are injected in every project session. The sidecar tracks both global
  and project-scoped candidates.
- **SQLite sidecar** — derived projection keyed on native file slugs.
  Stores ref_count, effectiveness, decay status, keyword index, and
  outcome history. Rebuildable from WAL + native frontmatter scan at
  any time. Degraded-ranker mode activates automatically when the
  sidecar is absent or corrupt: injection continues with overlap +
  recency; exit 0 always.
- **Four thin bash shims** (~10 lines each): `session-start.sh`,
  `user-prompt-submit.sh`, `pre-tool-write.sh`, `session-stop.sh`.
  Each marshals env/stdin → `memoryctl <verb>` and relays stdout/exit.
  Zero logic in shell.

### Changed

- **Storage substrate.** Content moves from `~/.claude/memory/<type>/`
  to `~/.claude/projects/<slug>/memory/` (project-scoped) and
  `~/.claude/memory-global/` (global). Native frontmatter schema
  (name / description / type) is the content surface; the full
  hypomnema schema lives in the sidecar.
- **WAL** — four-column invariant and event grammar preserved unchanged.
  Existing WAL carries over intact; `migrate --execute` seeds the v2
  sidecar from it.
- **`memoryctl doctor`** — updated for v2: sidecar health, global store
  visibility, parity check vs prior injection behaviour.
- **`memoryctl profile`** — self-profile no longer includes TF-IDF or
  cold-start dormancy sections.
- **ADRs superseded or retired** (six total):
  `bash-go-gradual-port` (superseded by `go-primary-bash-shims`),
  `two-scoring-pipelines` (superseded — merged),
  `substring-triggers-with-negation` (superseded — removed),
  `fts5-shadow-retrieval` (retired — FTS5 removed),
  `cold-start-scoring` (retired — gates removed),
  `scoring-weights` (revised — new v2 formula, A/B-calibrated).
  Two new ADRs added: `native-primary-sidecar`, `go-primary-bash-shims`.

### Migration

See `docs/MIGRATION.md § v1.x → v2.0` for the full guided upgrade,
compatibility gate, and rollback procedure.

---

## [1.1.2] - 2026-05-17

Docs-only release. Cross-tabbed static frontmatter signals against
per-slug WAL outcomes (n=49 slugs with measurable triggers) and
found two clean authoring-side sweet spots. The five scoring
weights survived the audit — the finding is about how to *write*
memory files, not how to rank them. Recorded as a Calibration
history entry in the existing scoring-weights ADR, with sizing
heuristics surfaced in `CLAUDE.md` and the four authoring templates.

### Documentation

- **`docs/decisions/scoring-weights.md`** — new Calibration history
  entry (2026-05-17): cross-tab table with sweet spots, author-side
  actions taken, and one parked open question. Top empirical signal:
  4-6 triggers hit 85 % useful-rate vs 12 % for 1-3 triggers (narrow
  enough to miss user phrasing, keyword-poor enough to also miss
  the keyword fallback); evidence ≤5 phrases keeps ~83 % useful
  vs 14 % for 6-12 phrases (long lists rarely match verbatim).
- **`CLAUDE.md § How injection ranks files`** — two sizing paragraphs
  added under `triggers:` and `evidence:` referencing the calibration
  history entry so future authors see the heuristics inline.
- **`templates/{feedback,mistake,knowledge,strategy}.md`** — sizing
  comments next to the `triggers:` and `evidence:` example blocks,
  pointing at CLAUDE.md for the rationale.

### Parked (not in this release)

- `wal_spaced_repetition` over-weights ambient rules (high spread ×
  decay × neutral 1.0 effectiveness). Either cap the wal-component
  for `precision_class: ambient` slugs or leave it because ambient
  files are already filtered out of the precision denominator. n=49
  too small to choose — re-evaluate at n≥150.

## [1.1.1] - 2026-05-17

Patch release. `memoryctl doctor`'s `corpus_frontmatter_quality` check
was flagging files that, by design, do not need substring triggers —
producing false-positive WARNs on mature corpora where ambient rules
and evidence-only feedback are normal. The check now understands the
three legitimate escape hatches and only flags files that are actually
unreachable through any reactive path.

### Fixed

- `memoryctl doctor` `corpus_frontmatter_quality` no longer flags
  feedback/knowledge files that are reachable through one of:
  `precision_class: ambient` (silent rule by design), `evidence:`
  (session-stop classifier picks them up), or
  `status: superseded` / `status: archived` (historical record, not
  expected to fire). Pure absence of `triggers:` remains a WARN.
  Hint text updated to enumerate the three escape hatches.

### Internal

- Two new doctor unit tests cover the escape-hatch paths and the
  no-escape-hatch baseline.

## [1.1.0] - 2026-05-08

Minor release covering the v1.1 work cycle that landed across eight
PRs the same day v1.0.1 shipped. Headline: **`memoryctl wal validate`
subcommand**, the **EVENTS.md normative registry**, and the
**intuition-milestone observational ADR going active** with a working
metric. Plus parity-check matrix expansion that exposed (and fixed) a
real bash slug-sanitisation drift, a contributor-DX `make
test-uninstalled` target, and a forward-looking WAL header marker ADR.

### Added

- **`memoryctl wal validate`** (PR #19). Scans `$CLAUDE_MEMORY_DIR/.wal`
  for grammar violations against the four-column invariant: column
  count, date format (`YYYY-MM-DD`), event name (`[a-z][a-z0-9-]*`),
  slug sanitisation, non-empty session. Tolerates the optional v2
  header on row 1 (see `docs/decisions/wal-header-v2-marker.md`).
  Exit codes 0/1/2 (clean / failures present / WAL missing or
  command misuse). Surfaced 39 historical `metrics-agg` rows with
  3 columns on the maintainer's personal corpus — known v0.x event
  shape no current code path emits, treated as historical noise.
- **`docs/EVENTS.md` normative registry** (PR #16). Single source of
  truth for WAL event types: per-event producer/consumer matrix,
  first-seen version, soft-close set membership, sub-delimiter
  glossary, authoring checklist for new events. Catalogues all 30
  active events plus a Reserved entry for `cascade-predictive`
  (planned v0.9 release candidate, see maintainer's local roadmap).
  `FORMAT.md § 5.1` now points readers here as the authoritative
  source.
- **`make test-uninstalled`** (PR #15). Runs the bash hooks smoke
  suite against the in-repo `$REPO/hooks/` and `$REPO/bin/` instead
  of `$HOME/.claude/{hooks,bin}/`, removing the `./install.sh`
  precondition for contributor runs. New `scripts/run-uninstalled-tests.sh`
  helper materialises a throwaway tmpdir of symlinks that mirrors
  install.sh's `_rename_dest` layout. Test runner reads
  `HYPOMNEMA_HOOKS_DIR` / `HYPOMNEMA_BIN_DIR` with default paths
  unchanged. 296/296 tests pass against an uninstalled checkout.
- **`docs/decisions/intuition-milestone.md`** (PRs #14 + #21).
  Observational-milestone ADR — when the windowed ratio
  `silent_applied_30d / trigger_useful_30d > 1.0` sustains for 30
  days, hypomnema has crossed the v1.0 «Интуиция» tier, formalised
  as a **measurable state** rather than a release. Promoted from
  the aspirational concept-roadmap entry on 2026-05-08. Status
  `proposed` → `active` once the underlying metric implementation
  landed in the same release cycle.
- **`docs/decisions/wal-header-v2-marker.md`** (PR #20). Reserves
  the line shape `# hypomnema-wal v2` as an optional first-line
  marker for `.wal`. Companion to `wal-four-column-invariant.md`:
  that ADR defines a row, this one defines a file. Status
  `proposed` — readers already tolerate the marker (`internal/wal.Validate`
  + every existing reader's `len(fields) < 2` skip), writer
  enablement deferred to v1.2+ pending observed value.
- **`Direction` field on `Trigger` schema + `StatusReached`** (PR #21).
  The `decisions review` evaluator now distinguishes positive
  milestones (Direction = `"above"` + crossed → `StatusReached`,
  marked with `✓ reached:`) from degradation pressure (legacy
  empty Direction or `"below"` + crossed → `StatusPressure`).
  Pre-existing ADRs continue unchanged. `intuition-milestone` is
  the first ADR to use the positive-event channel.
- **`## Intuition signal` section in `self-profile.md`** (PR #21).
  Renders `silent_applied_30d`, `trigger_useful_meas_30d`, the
  ratio, and a one-line interpretation. Window
  ENV-overrideable via `HYPOMNEMA_INTUITION_WINDOW_DAYS` (default 30).

### Changed

- **Parity check matrix expanded 4 shadow → 6 shadow + 1 self-
  profile + 1 tfidf = 8 cases** (PR #17). Two new shadow fixtures:
  `edge-slug-pipe` (hostile basename `foo|outcome-positive|bar.md`,
  validates that both implementations sanitise WAL-grammar-breaking
  characters before emitting `shadow-miss`) and
  `large-wal-256k-prefilled` (~260 KiB pre-filled WAL, validates
  the dedup tail-cap optimisation does not cause divergence).
  `_run_case` now accepts an optional fixture-seed function name
  and snapshots/restores any pre-seeded `.wal` so Go runs against
  the same starting state as bash.

### Fixed

- **`bin/memory-fts-shadow.sh` slug sanitisation regression** (PR #17).
  The bash shadow reader derived `_slug="${_path##*/}"; _slug="${_slug%.md}"`
  and emitted it verbatim, drifting from the Go-side
  `pathutil.SlugFromPath` fix shipped in v1.0.1 (E1) and from the
  audit-2026-04-16 marker S6 invariant. Discovered by the new
  `edge-slug-pipe` parity fixture. Fixed inline with
  `_slug=$(printf '%s' "$_slug" | tr '|\n\r' '_')`. Audit marker S6
  now applies symmetrically to both writer and shadow reader paths.

### CI

- **darwin bench-gate round 4** (PR #18). Same hosted-runner
  variance pattern as rounds 1-3 hit `user-prompt-submit (broad)`
  on PR #16's docs-only run (8651 ms vs 8250 ms ceiling, ratio
  1.57×). Bumped baseline 5500 → 6500 ms; ceiling 9750 ms with
  current 1.5× tolerance. **Decision after round 4:** ratio-vs-
  main is deferred to v1.x (doubles CI runtime; not a copy-paste
  pattern). Sample-count smoothing (≥5 runs, take p50 not avg) is
  the smaller intermediate step worth piloting first.

### Process

- **Auto-merge enabled at the repo level**. Future PR sequences
  with multiple ready PRs queue + auto-rebase + auto-merge once
  CI is green, removing manual chasing during multi-PR releases.

## [1.0.1] - 2026-05-08

Hardening patch addressing three findings from an external review of
v1.0.0. All three are defensive fixes — none corresponds to an
observed in-the-wild incident — but each closes a contract gap that
would matter under hostile filenames or partial-failure conditions.

### Fixed

- **`pathutil.SlugFromPath` sanitises WAL-grammar-breaking characters.**
  Slugs are written into the WAL as one of four pipe-delimited
  columns; a basename like `foo|outcome-positive|other-slug|sess.md`
  used to round-trip into the WAL verbatim, fooling 4-column readers
  and faking `outcome-positive` events for unrelated slugs. Bash side
  already enforced this (audit-2026-04-16 marker S6); the Go pilot
  drifted. `|`, `\n`, `\r` now replaced with `_` defensively. Unit
  test `TestSlugFromPath_DropsControlChars` covers all three control
  characters plus a sanity case (plain slug unchanged).
- **`profile.Generate` writes `self-profile.md` atomically via
  `os.CreateTemp` + `os.Rename`.** The previous `os.WriteFile` would
  truncate the existing file before writing the replacement; a crash
  mid-write left a half-rendered profile on disk. The pattern matches
  `tfidf.Rebuild` and `evidence.AppendToFrontmatter` — every
  multi-line writer in `internal/` now uses tmp+rename. Post-condition
  `TestGenerate_NoOrphanTmpOnSuccess` locks the invariant.
- **`dedup.Run` rejects `targetPath` outside `MemoryDir`.** Without
  the boundary check, a caller (or a malformed hook) could feed dedup
  any path on disk; on a 100 % similarity match dedup would enter the
  posttool merge path, delete the outside file, and write a
  `dedup-merged` WAL event for content that never belonged in the
  memory directory. Failure mode is `Allow` (silent no-op), matching
  the package's "dedup must never break a legitimate write" contract.
  Test `TestRun_RejectsTargetOutsideMemoryDir` reproduces the exploit
  pre-fix and locks rejection post-fix.

### Verification

- `go test -race ./...` clean across all 12 internal packages.
- 3 new tests added; 0 existing tests touched.

## [1.0.0] - 2026-05-07

Format v1 → v2 bump — first breaking grammar change since v0.3 —
plus a sweep of correctness, observability, and CI-quality work
that landed in the same day. Headline: `session-metrics` events
finally carry their `session_id` in the canonical `$4` slot,
matching every other 4-column event. Supporting cast: shadow-pass
project filter, three independent test-fixture time-bombs, four
ADR review-trigger metric gaps closed, and the perf bench moved
from observational to gated.

### Changed (breaking)

- **`session-metrics` wire format.** New shape:

  ```
  <date>|session-metrics|domains:<csv>,error_count:N,tool_calls:M,duration:Ss|<session_id>
  ```

  v1 shape (`<date>|session-metrics|<domains>|<error_count:...>` with
  no session id) is still accepted on the read path indefinitely —
  every reader detects the format per line, keyed on whether `$3`
  begins with `domains:`. New writes use v2 only. No automatic
  WAL migration ships in this release; existing v1 entries stay
  v1 on disk and are read alongside v2 ones.
- **`docs/FORMAT.md` declares `format_version: 2`.** Backward-
  compat reading of v1 `session-metrics` is now mandatory; all
  other event types and frontmatter surfaces are wire-identical
  between v1 and v2.

### Reader compatibility

- `internal/profile/profile.go:splitSessionMetrics` is the canonical
  Go demuxer — accepts both v1 and v2 inputs and returns a uniform
  `(domains_csv, metrics_blob)` pair.
- `bin/memory-self-profile.sh`, `hooks/memory-analytics.sh`, and
  `hooks/wal-compact.sh` parse both formats inline (mirrored awk).
- `scripts/parity-check.sh` mixes v1 and v2 rows in its fixture
  WAL so the bash-vs-Go diff exercises both demuxers.

### Fixed

- **Shadow-pass cross-project false-positives** (PR #1). FTS5
  body-keyword overlap surfaced cross-project files (e.g. an iOS
  Swift strategy in a JIRA Groovy session) and the resulting
  `shadow-miss` events tripped pressure on three review triggers
  on noise. `bin/memory-fts-shadow.sh` and the Go pilot now skip
  files whose `project:` is neither empty/global nor equal to the
  current project — mirrors the priority-key first bucket of the
  primary substring pipeline.
- **Three test-fixture time-bombs** (PR #1) — `score-records.sh`,
  `build-context.sh`, and `internal/doctor/doctor.go` all anchored
  WAL-window cutoffs on real `date` instead of `HYPOMNEMA_TODAY`.
  CI on 2026-04-24 was green because the fixture WAL events
  (2026-04-12..-15) were inside the 14- and 30-day windows; replay
  on 2026-05-07 silently dropped them outside. All three now use
  the resolved-today variable; fixture snapshots add an explicit
  `frozen_today` field.
- **`measurable_precision` regex never matched production** (PR
  #2). Loader regex `[^|]*?` stopped at the markdown-table pipe
  and never reached the value cell. Test fixture used a bogus
  inline form not present in production — classic
  test-fixture-encodes-the-bug. Replaced with a line-wide match;
  unblocks the `precision-class-ambient` and `scoring-weights`
  ADRs that depended on it.
- **`cold-start-scoring` ADR threshold mis-calibrated for
  single-install corpora** (PR #3). Original 0.30 trigger was
  drafted assuming fleet-aggregate measurement; on a personal
  install where rules fire episodically, the structural ceiling
  is around 0.10–0.15 with `MIN_SAMPLES = 5` over 14 days.
  Threshold relaxed to 0.10; defaults stay because lowering them
  degrades Bayesian smoothing.

### Added

- **`ambient_fraction` and `corpus_fraction_with_active_bayesian`
  metrics** (PR #2) — both computed in `internal/profile` and
  `bin/memory-self-profile.sh`, surfaced in `self-profile.md`,
  loaded by `internal/decisions/snapshot.go`. Closes 4/5 SKIPPED
  metrics in `memoryctl decisions review`. The remaining one
  (`bayesian_override_env_usage`) is fleet-level by design.
- **Subprocess coverage aggregation for `cmd/memoryctl`** (PR #4).
  TestMain now uses a persistent `GOCOVERDIR` (per-test
  `t.TempDir()` was auto-cleaned before aggregation, which is
  why `go test -cover ./cmd/memoryctl/...` reported 0.0%). New
  `make test-go-cover` target prints both in-process and
  subprocess coverage; `MEMORYCTL_SUBPROCESS_COVER_OUT` env
  controls the textual profile path. Real number on the maintainer
  tree: 34.6% (was hidden as 0.0%).
- **Perf-regression CI gate** (PR #6).
  `scripts/bench-gate.sh` runs `bench-memory.sh perf`, parses the
  per-scenario `avg=Nms` lines, and fails when observed >
  `baseline × tolerance` for any scenario flagged `gated: true`
  in `docs/measurements/baselines.json`. Replaces the previous
  `continue-on-error: true` observational step. High-variance
  scenarios stay observational. First calibration record at
  `docs/measurements/2026-05-07-bench-gate-calibration.md`.

### Documentation

- README status updated to v1.0 / `format_version: 2`; sub-claims
  brought in line with the current state (Go 1.25+, ubuntu-latest
  CI coverage).
- `docs/decisions/{fts5-shadow-retrieval,scoring-weights,
  substring-triggers-with-negation,cold-start-scoring}.md` —
  calibration history sections added documenting today's
  threshold and metric reasoning.
- `docs/measurements/2026-05-07-bench-gate-calibration.md` —
  initial baseline numbers, both runners' first CI observations,
  recommended Linux tightening pass for a future PR.

### Not in this release

- `memoryctl wal validate` and `memoryctl wal migrate` subcommands.
- `docs/EVENTS.md` normative event-type registry.
- WAL-header v2 marker comment.
- Linux baseline tightening — current values leave 3–4× headroom;
  worth a follow-up after a few CI rounds populate the real
  distribution.

These were originally bundled into the v1.0 plan but are
orthogonal to the wire-format fix itself; they ship as follow-up
PRs without further format breakage.

## [0.15.0] - 2026-04-24

P2-debt release. Twelve hygiene items the v0.13 external review
surfaced and v0.14 deferred. Nothing here changes scoring
behaviour; the entire release is refactor, lock robustness,
install/CI cleanup, and two ADRs documenting magic constants
whose rationale lived nowhere outside the code.

### Added

- **ADR `docs/decisions/scoring-weights.md`** (P2.1). Records the
  five-component weights (keyword ×3, project ×1, WAL 0–10,
  strategy +6 cap, TF-IDF ×2 capped, noise −3) as deliberately
  kept rather than re-calibrated, and sets three review triggers
  — `measurable_precision < 0.10`, `shadow_miss_ratio > 0.50`,
  calendar `2027-04-24`.
- **ADR `docs/decisions/quota-pool.md`** (P2.2). Explains the
  12/10/8 adaptive pool and 5/10 keyword thresholds. Trigger:
  `ambient_fraction > 0.75` on self-profile, or calendar
  `2027-04-24`.
- **`internal/pathutil` package.** Single home for `SlugFromPath`,
  which had drifted into three per-package copies (one of which
  used `strings.LastIndexByte('/')`, breaking non-Unix
  portability).
- **`internal/evidence.ParseYAMLListItem`.** Dedupes the
  `- "phrase"` / `- 'phrase'` parsing that was inlined in both
  `cmd/memoryctl/evidence.go:readEvidenceBlock` and
  `internal/evidence/writer.go:collectExisting`, where the two
  copies had already diverged on case handling.
- **`wal.AcquirePath`.** Public variant of `wal.Acquire` that
  locks an arbitrary directory, used by `runSelfProfile` for a
  dedicated `.self-profile.lockd` (see fix section).
- **`internal/wal/lock_test.go`.** Seven tests covering
  empty-dir acquire, idempotent release, timeout under
  contention, stale takeover, no-takeover on a fresh lock,
  custom lock path, and an 8-worker concurrency test asserting
  no two callers hold simultaneously. The lock's
  mkdir-atomicity contract was previously tested only
  transitively through `Append`.

### Changed

- **`wal.containsLine`** (P2.3) now seeks to a trailing 256 KiB
  of the WAL before scanning. On a 50 MB live WAL this removed
  the O(WAL-size) dedup cost on every append — the hot path no
  longer blocks while the file grew between compactions. Small
  WALs (< 256 KiB) still read whole, preserving previous
  behaviour for test fixtures and fresh installs.
- **`filepath.Walk` → `filepath.WalkDir`** (P2.11) in
  `internal/profile/profile.go:collectAmbientSlugs` and
  `cmd/memoryctl/evidence.go:recentTranscripts`. WalkDir avoids
  the implicit per-entry stat; explicit `d.Info()` only where
  we keep the entry. Net cost is lower.
- **`hooks/lib/wal-lock.sh:wal_run_locked`** (P2.8) no longer
  runs silently on lock-acquisition failure. Always logs the
  failure to stderr so session-stop's journal and CI logs
  surface real contention. `WAL_LOCK_STRICT=1` opt-in returns
  EX_TEMPFAIL (75) instead of running unlocked — use for
  critical writes where skipping is preferable to racing.
  Default stays fail-open for backward compat.
- **`cmd/memoryctl/evidence.go:recentTranscripts`** (P2.5) now
  goes through `claudeDir()` instead of calling
  `os.UserHomeDir()` directly, so `CLAUDE_HOME` (for parallel
  installs and test fixtures) is respected here alongside the
  doctor and FTS code paths.
- **`install.sh` settings backup** (P2.9) now writes
  `settings.json.backup-hypomnema-YYYYMMDD-HHMMSS`. Previous
  installs wrote a single unversioned path that the next
  install happily overwrote, discarding the original
  pre-hypomnema snapshot after one upgrade. The legacy
  unversioned name is kept as a symlink to the first backup
  so existing scripts still find it.
- **`.github/workflows/test.yml`** Go pin (P2.10): `1.22` →
  `1.25` to match `go.mod:3`. The 1.22 pin only passed because
  setup-go's `check-latest` silently up-ranged — removed that
  fragility.

### Fixed

- **`runSelfProfile` race** (P2.12). `profile.Generate` and
  `profile.AppendDecisionsPressure` previously ran back-to-back
  with no shared lock. Concurrent SessionStart could observe
  the core profile before the Decisions section landed;
  overlapping session-stop calls could clobber each other.
  v0.15 serialises both under `.self-profile.lockd` via the new
  `wal.AcquirePath`. The profile lock is separate from the WAL
  lock so regen cannot block WAL appends.

### CI

- **Perf bench step** (observational). `bash hooks/bench-memory.sh
  perf` now runs on every push and PR; session-start /
  user-prompt-submit / stop / secrets-detect numbers land in the
  GitHub step summary next to the in-script baselines. No
  threshold gating yet — blocked on having enough CI runs to
  distinguish real regressions from runner variance, flagged for
  a v0.16 follow-up. Human reviewers can still eyeball the
  summary for > 2× regressions.
- **Fixture snapshot step.** `make test-fixtures` runs on every
  push/PR so synthetic-cold-start / synthetic-mature assertions
  catch regressions in scoring gates before merge.

### Tests

- 296 → 303 Go tests (+7 in `internal/wal/lock_test.go`).
- 296 bash smoke asserts unchanged.
- 27 synthetic-fixture snapshot assertions unchanged.

## [0.14.0] - 2026-04-24

Scoring architecture work driven by the v0.13 external review.
Two main features plus a cluster of supporting fixes. The
headline story is that two of the five scoring components
(Bayesian effectiveness, TF-IDF body match) were silently
dormant on any young or feedback-heavy corpus — Bayesian for
lack of outcome signal, TF-IDF because the builder skipped
every file with a `keywords:` frontmatter. v0.14 diagnoses
each state explicitly and closes the data-collection gap for
non-mistake slugs.

### Added

- **Cold-start scoring gates (ADR proposed → active).**
  `hooks/lib/score-records.sh` returns neutral Bayesian
  effectiveness `1.0` when outcome events within
  `HYPOMNEMA_OUTCOME_WINDOW_DAYS` (default 14) fall below
  `HYPOMNEMA_BAYESIAN_MIN_SAMPLES` (default 5);
  `hooks/session-start.sh` suppresses TF-IDF body match when
  `.tfidf-index` vocabulary is smaller than
  `HYPOMNEMA_TFIDF_MIN_VOCAB` (default 100). All thresholds
  ENV-overrideable. See `docs/decisions/cold-start-scoring.md`
  for the reasoning and `docs/fixtures/real-corpus-notes.md`
  for the calibration pass that activated the ADR.
- **`outcome-positive` beyond `mistakes/`.** New
  `classify_outcome_positive_from_evidence` in
  `hooks/lib/outcome-detection.sh` emits `outcome-positive`
  for every slug feedback-loop marked `trigger-useful`
  (evidence actually referenced in the transcript). Bayesian
  effectiveness now discriminates on feedback-heavy corpora
  where no mistakes are ever written. Dry-run on the author
  corpus: +71 new (slug, session) pairs over a 14-day window,
  ~1.8× the existing outcome-positive baseline, all from
  non-mistake types.
- **`session-close` WAL event.** Emitted alongside
  `session-metrics` on every completed Stop hook regardless of
  error count; carries the session id in the canonical `$4`
  column (`session-metrics $4` is the payload, not an id).
  Consumed by `wal-retro-silent.sh` as a closing signal —
  sessions that had errors > 0 are no longer retroactively
  classified as silent.
- **`.secretsignore` + `.secretsignore.default`.** Two-file
  allowlist for `memory-secrets-detect.sh`. `.default` is
  committed to the repo and overwritten on every install;
  user-level `.secretsignore` is optional and untouched by
  the installer. Minimal `.gitignore` subset (line-by-line,
  `#` comments, `!` negation, `**` glob). Unblocks `seeds/`
  hazard documentation without requiring per-call
  `HYPOMNEMA_ALLOW_SECRETS=1`.
- **Doctor check `scoring_components_active`.** Reports
  `N/5` active components with per-dormant reasons (sample
  counts, vocab size, threshold values) in the JSON `extra`
  field. Makes "low measurable precision" diagnosable without
  guesswork.
- **Doctor check `corpus_frontmatter_quality`.** Scans
  memory files; WARNs on empty `status:` values and on
  `feedback`/`knowledge` files without `triggers:`. Surfaces
  up to 5 sample filenames per issue so the operator knows
  where to look.
- **Synthetic corpus fixtures** under `fixtures/corpora/`:
  `synthetic-cold-start` (claim: Bayesian + TF-IDF dormant on
  zero-outcome / low-vocab corpus) and `synthetic-mature`
  (claim: all 5 components active at ≥ threshold signal). No
  real user content — SPEC-first `expected.json` per fixture.
  `hooks/test-fixture-snapshot.sh` diffs actual doctor output
  against spec; `make test-fixtures` runs the suite.
- **Session-close and soft-close semantics in
  `docs/FORMAT.md § 5.2`.** Canonical list of closing events
  for retro classification, plus justification for keeping
  `session-metrics` OUT of the set (`$4` is payload, not id).
- **CLAUDE.md sections** for Scoring cold-start, Secrets
  detection, Retroactive silent classification,
  "When to write a `decision`", and a
  "Feedback-as-mistake: blurry boundary" subsection that kills
  the double-write ambiguity between the two types.

### Changed

- **`hooks/memory-index.sh` + `internal/tfidf/tfidf.go`** no
  longer skip files that declare `keywords:` in frontmatter.
  The build-time skip was an over-correction that left every
  well-frontmattered corpus with an empty TF-IDF index;
  vocabulary gating at the call site handles the small-corpus
  case instead. TF-IDF tests flipped polarity
  (`TestRebuild_SkipsFilesWithKeywords` →
  `TestRebuild_IndexesFilesWithKeywords`).
- **`internal/doctor/doctor.go`** now counts
  `memory-secrets-detect.sh` among the required hook commands
  (v0.13 registered it but doctor kept counting to seven).
  Full hook coverage on `settings_hooks_registered` again.
- **`hooks/wal-retro-silent.sh`** closing set includes
  `session-close` and `outcome-new`; both WAL passes guarded
  by `NF == 4` so corrupt lines cannot poison the set or emit
  a malformed retro record.
- **`internal/doctor` `Check` struct** gained an optional
  `Extra map[string]interface{}` (omitempty). Lets checks emit
  structured context (counts, thresholds, samples) without
  encoding them into the `Detail` string.

### Fixed

- **`hooks/session-start.sh` `ref_count` migration.** Perl
  was a pure substitution, so files authored before the
  `ref_count` field existed stayed permanently at the zero
  bucket of the priority key. Rewritten to slurp the
  frontmatter block and inject `ref_count: 0` before the
  closing fence when absent, then increment. Exactly-once
  migration on first injection after upgrade.
- **`cmd/memoryctl/main.go`** FTS shadow / sync / query calls
  now use `context.WithTimeout` (2 s / 10 s / 3 s).
  `context.Background()` meant an unhealthy SQLite could
  stall `UserPromptSubmit` indefinitely even though shadow
  is observational and must never block prompt latency.
- **Tautological tests.** Two new guards catch the drift
  pattern that left the real v0.13 hook-registration gap
  invisible: `TestRequiredHookCommands_MatchInstallScript`
  parses `install.sh` directly and diffs against
  `requiredHookCommands`;
  `TestIndexSubdirs_MatchesExpectedSet` compares
  `indexSubdirs` element-for-element against an independent
  whitelist.
- **Documentation consistency pass.** Test counts, composite
  score formula, `status:` enum, and clone URL were
  inconsistent across `README.md`, `CLAUDE.md`, and
  `docs/QUICKSTART.md`. Canonical weights now live solely in
  `CLAUDE.md`; `README.md` summarises components and links
  out. `superseded` removed from `status:` enum (unused by
  any code). `docs/QUICKSTART.md` clone URL aligned to the
  public repo.

### Tests

- 279 → 296 smoke asserts (+17 across
  P1.1 evidence-match / P1.2 ref_count migration /
  P1.5 soft-close / P1.7 `.secretsignore` / P1.8 doctor
  checks).
- `make test-fixtures` target runs snapshot diff against
  synthetic fixtures (27 assertions across cold-start +
  mature).
- `make parity` unchanged — it compares bash vs Go shadow,
  orthogonal to the scoring work.

### Calibration

Cross-reference pass on 2026-04-24 compared two synthetic
fixtures against two local real corpora:
  - `synthetic-cold-start` (3/5 active) ↔ local onboarding
    corpus (3/5 active) — matched.
  - `synthetic-mature` (5/5 active) ↔ author corpus
    (5/5 active, 44 outcomes in 14d, 4807-term vocab) —
    matched.

No threshold needed tuning. Full record in
`docs/fixtures/real-corpus-notes.md`.

## [0.13.1] - 2026-04-24

Recovery patch for v0.13.0. Three gap defects in the two features
that shipped yesterday: the secrets-detect hook missed Edit-tool
inputs entirely, the uninstaller left ghost hook entries in
`settings.json`, and the retroactive-silent classifier raced with
compaction over the WAL lock and could erase its own inputs.

### Fixed

- **`hooks/memory-secrets-detect.sh`** now reads
  `tool_input.new_string` as a fallback to `tool_input.content`.
  The hook is registered on `PreToolUse:Write|Edit`, but previously
  parsed only the Write-style `content` field; every `Edit` that
  wrote a plaintext secret into `~/.claude/memory/` slipped past
  the gate. Agents use `Edit` more often than `Write` for memory
  files, so the practical coverage of the v0.13 feature was
  near-zero. Two regression tests added (`Edit` with secret →
  block; `Edit` with clean content → pass).
- **`uninstall.sh`** now strips every `memory-*.sh` entry from
  `settings.json`, not just the three hard-coded pairs
  (SessionStart, Stop, UserPromptSubmit). `install.sh` registers 8
  hooks; the previous uninstaller left 5 as ghost entries that
  Claude Code would try to execute and fail on each subsequent
  `Write`/`Edit`/`Bash`/`PreCompact` call — visible to the user as
  repeated "no such file" errors for hooks they had told the
  uninstaller to remove. Rewritten as a single `jq` walk over all
  hook events with a regex match on the command field; unrelated
  user hooks are preserved. Two regression tests added.
- **`hooks/session-stop.sh`** now runs `wal-retro-silent.sh`
  synchronously, before the backgrounded `wal-compact.sh`. Both
  were previously fired as `&` jobs with no `wait` between them,
  so the scheduler could let compact acquire the WAL lock first
  and rewrite raw `inject` rows into `inject-agg` — whereupon
  retro's pass-2 matcher (`$2 == "inject"`) found nothing and
  silently emitted no `trigger-silent-retro`. Retro is bounded
  work (~250 ms on a 100K-line WAL), so the added synchronous Stop
  latency is the correct trade-off versus losing retroactive
  classification to scheduler ordering. Three regression guards
  added (source-level check that retro is not backgrounded and
  compact still is).

### Tests

- 272 → 279 smoke asserts (+2 secrets-detect Edit coverage,
  +2 uninstall settings.json cleanup, +3 session-stop race guards).

## [0.13.0] - 2026-04-23

Measurement-bias fix + write-path safety + public-release
preparation. The three things an external reader would notice in
the first hour of use, landed together.

### Added

- **`hooks/wal-retro-silent.sh`** — retroactive `trigger-silent-retro`
  classification. Walks the WAL each Stop hook; raw `inject` events
  whose session never wrote a closing event AND are older than 24 h
  get one `trigger-silent-retro|<slug>|<session>` emitted per inject.
  Runs before `wal-compact.sh` in the Stop-hook background fan-out
  (compaction replaces raw `inject` with `inject-agg` and loses
  session_id). Idempotent. Same lock as wal-compact. Author corpus
  measured 93% open quanta pre-retro, 1% after. 39 of the remaining
  injects are sessions younger than 24 h, not truly lost.
- **`hooks/memory-secrets-detect.sh`** — PreToolUse:Write|Edit gate
  that blocks memory-file writes containing plaintext secret-looking
  tokens (api_key, apikey, secret, password, token, aws access/secret
  key variants; value ≥ 8 chars; outside fenced code blocks). Scope
  limited to paths under `$CLAUDE_MEMORY_DIR/`. Escape hatch
  `HYPOMNEMA_ALLOW_SECRETS=1` for one-off legitimate cases.
  install.sh now registers 8 hypomnema commands (up from 7).
- **`CONTRIBUTING.md`** — "issues open, PRs by invitation" framing,
  local-test commands, invariants a PR must not cross, commit-style
  conventions. Points at ADRs and roadmap.
- **`SECURITY.md`** — vulnerability-report channel (GitHub private
  vulnerability advisories), threat-model scope (no network surface;
  in-scope = LLM-authored content escalating beyond markdown-inert),
  enumerated known-not-vulnerabilities (exit-0 fail-safe, LLM-authored
  memory, session_ids are UUIDs).
- `trigger-silent-retro` registered in `docs/FORMAT.md §5.1`.

### Changed

- **`internal/doctor` open_quanta check** counts
  `trigger-silent-retro` as a closing event. Running the check on a
  WAL that's been through one Stop-hook pass since v0.13 shows
  dramatically reduced open-quanta ratios.
- **`memory-analytics.sh § Trigger usefulness`** surfaces a
  `silent-retro=N` column per slug when any retros exist. Keeps the
  natural-silent signal distinct from the posthumous one — a rule
  going from silent=10 to silent-retro=30 is a different problem
  than going to silent=30.
- **`test-memory-hooks.sh safe_cleanup drain pattern`** adds
  `wal-retro-silent.sh` to the backgrounded-children list.

### Repository

- **Repo is now public.** Project infrastructure (LICENSE from
  v0.10.3, description + 9 topics from the same release,
  CONTRIBUTING + SECURITY from this release) is complete enough
  that a clone+install from a stranger's fresh HOME produces a
  working system on the first try.
- Show HN / r/ClaudeAI post drafted from
  `docs/maintenance/DISTRIBUTION_DRAFTS.md` with current v0.13
  numbers.

### Non-goals in this release

- No event retroactive-classification beyond 24 h — sessions past
  that point stay in their current trajectory. Retro is
  forward-looking, not backfill-all-history.
- No hand-tuned secret-detection rules beyond the five keyword
  patterns. More specific scanners (GitHub token prefixes,
  AWS-ARN shapes) are a v0.14 candidate if the false-negative
  rate is high enough to measure.
- No auto-clean for rejected retros. Operator who disagrees with a
  retro (say, they manually traced a crashed session back and
  classified it as useful) edits the WAL by hand.

### Coverage

- 10 new assertions in `test-memory-hooks.sh`: five for retro
  (emits for orphans, skips already-closed, skips under-cutoff,
  idempotent, shell-safety pipefail), five for secrets-detect
  (block, fence-skip, out-of-scope-skip, bypass env, short-value-
  tolerance). 262 → 272.

### Deferred to v0.14+

- **`session-metrics` wire-format fix** (v1 → v2 breaking bump).
  Gated on an external review, per
  `docs/plans/2026-04-23-roadmap-v0.10.5-onward.md § v1.0`.
- **`bench-memory.sh` CI integration** — percentile-vs-baseline
  threshold design still open.
- **`cmd/memoryctl` coverage aggregation tooling** — subprocess
  coverage still 0 % in `go test -cover` output despite real
  functional coverage.
- **Further `session-*.sh` decomposition** — current line counts
  manageable; next touch when a feature forces it.

## [0.12.1] - 2026-04-23

### Fixed

- **CI build on v0.12.0 failed** with four `undefined: evidence.*`
  errors. `internal/evidence/writer.go` and `writer_test.go` existed
  locally and were referenced by `cmd/memoryctl/evidence.go`, but
  the files were never added to git — the earlier commit used
  `git add internal/evidence/miner.go` (specific file) rather than
  the directory, and the writer pair stayed untracked. Local `go
  build` and `go test` passed because the files were on disk; CI
  clones a fresh tree and caught the gap immediately. Pure recovery
  commit — the files are as-local-was.

## [0.12.0] - 2026-04-23

Evidence learn — bootstrap the `evidence:` phrase block on any
memory rule by mining real session transcripts. Closes the cold-
start gap where every new rule ships silent until the author
hand-authors enough phrases to match Claude's actual voice.

### Added

- **`memoryctl evidence learn --target SLUG`** — interactive REPL
  that reads recent session JSONLs under
  `~/.claude/projects/*/*.jsonl`, finds sessions where the target
  fired `trigger-silent` / `evidence-empty` in the WAL, extracts
  2/3-gram candidate phrases from enumeration-style regions of the
  assistant text, filters against the noise baseline (sessions
  where the rule was useful or not injected), and presents a
  ranked list for y/n/p/q approval. Accepted phrases append to
  the target's `evidence:` frontmatter block; pattern rejections
  persist per-slug in
  `~/.claude/memory/.runtime/evidence-rejections/<slug>.list`.
- Flags: `--sessions N` (default 30), `--dry-run` (print-only),
  `--yes` (batch accept).
- **`internal/jsonl`** — streaming reader for Claude Code
  transcripts. Extracts `message.content[].type == "text"` blocks
  from `type == "assistant"` records, skips `thinking` entries,
  ignores non-assistant records. 4 MB line cap covers real
  sessions with large tool output.
- **`internal/evidence`** — deterministic phrase miner. N-gram
  extraction narrowed to enumeration regions (2+ consecutive
  bullet / numbered / keyword-header lines); fallback to whole
  text when no enumeration detected. Filters: `MinSilentSessions=2`
  absolute floor, `MinSilentCoverage=0.20`, `MaxNoiseCoverage=0.10`,
  `MinRuneLen=5`, numeric-only rejection (drops commit hashes and
  "0 0 0" noise). Writer appends atomically via tmp+rename, dedups
  against the existing block case-insensitively, handles embedded
  quotes.

### Coverage

- 12 tests across `internal/jsonl` and `internal/evidence`: assistant-
  text extraction (no thinking / no user prompts leaked), malformed
  JSONL resilience, Cyrillic phrase mining, code-fence / blockquote
  stripping, frontmatter append (existing block / missing block /
  case-insensitive dedup / no-frontmatter error / embedded-quote
  escape), rejection-list round-trip.
- 6 subprocess tests in `cmd/memoryctl` covering unknown subcommand,
  missing `--target`, invalid `--sessions` value, unknown flag,
  missing target file, no-transcripts exit path.

### Changed

- **`docs/CONFIGURATION.md §4 Evidence learning`** new section
  documenting the subcommand, flags, and deterministic-offline
  guarantees (no network, no model inference).

### Non-goals in this release

- No automatic mining in the Stop hook — evidence authoring is
  trust-critical, plan explicitly gates it behind interactive
  approval.
- No LLM in the extraction pipeline — all n-gram + filter is
  stdlib-only.
- No cross-user phrase aggregation — personal voice doesn't transfer
  between users.
- No phrase translation — Russian prompts produce Russian phrases;
  the tool doesn't propose English equivalents.

### Measured on author corpus

A dry-run on 99 most-recent sessions surfaces real candidate phrases
scoped to bullet-list regions. Interactive approval is the human
judgment layer that separates authorial voice from project-specific
code discussion that happens to live in enumeration blocks.

### Deferred to v0.13+

Unchanged from v0.11 deferred list — v0.12 shipped its full scope.

## [0.11.0] - 2026-04-23

Decisions autoreflection — ADRs grow structured `review-triggers:`
that the system evaluates against the current metric snapshot on
demand. Prose-only ADRs keep working unchanged; the new surface is
purely additive.

### Added

- **`review-triggers:` frontmatter field on ADRs.** Two shapes —
  metric trigger (`metric` + `operator` + `threshold` + `source`)
  or calendar trigger (`after: YYYY-MM-DD`). Multiple triggers per
  ADR allowed. Supported metrics: `measurable_precision` and
  `recurrence_top_mistake` (self-profile), `shadow_miss_ratio`
  (WAL rolling 14-day window), `ref_count` (the ADR's own
  frontmatter).
- **`memoryctl decisions review [--dir PATH] [--json]`** — evaluator
  that reads every ADR in the selected directory and reports one
  line per trigger: `[ok|pressure|overdue|skipped] slug — detail`.
  Exit 1 if any trigger fires. Directory precedence:
  `--dir` override → `./docs/decisions/` (project-level) →
  `$CLAUDE_MEMORY_DIR/decisions/` (personal). `--json` for scripts.
- **`memoryctl self-profile` appends `## Decisions under pressure`**
  section when any ADR trigger fires. No-op on installs without a
  personal `decisions/` directory, preserving byte-for-byte parity
  on the fixture that doesn't carry one.
- **`memoryctl doctor` adds `decisions_review` check** — scans
  `$CLAUDE_MEMORY_DIR/decisions/`, WARNs with count + example slugs
  when triggers fire or ADR frontmatter is malformed.
- **`internal/decisions` package** — pure evaluator (17 unit tests),
  strict parser with shape validation (unknown operator, mixed
  metric/calendar fields rejected), snapshot builder with
  configurable `now`. All stdlib-only; no external YAML dependency.
- **8 existing ADRs in `docs/decisions/`** backfilled with
  `review-triggers:`. Two carry structured metric triggers
  (`fts5-shadow-retrieval` fires on `shadow_miss_ratio > 0.30`,
  `substring-triggers-with-negation` on `> 0.60`,
  `precision-class-ambient` on `measurable_precision < 0.40`). All
  eight get a one-year calendar trigger so prose-only decisions
  still surface for periodic review.

### Changed

- **`docs/CONFIGURATION.md §3 ADR review-triggers`** rewritten —
  removes the "planned but not yet implemented" note, documents
  the actual shape, metric table, and directory precedence.
- **`CLAUDE.md` frontmatter schema** gains a `decision` block
  introducing `review-triggers:` with forward links to the CLI.
- **OK-case message formatting** in `decisions review` — was
  "metric = value op threshold" (readable as if fired), now
  "metric = value (trigger: op threshold)" so the condition is
  clearly the threshold spec, not the observation.

### Non-goals for this release

- No automatic action on `pressure` / `overdue` — review is
  advisory. Author decides whether to revisit.
- No LLM in the evaluation loop — deterministic metric comparison
  only, matching the plan's non-goals.
- Bash `bin/memory-self-profile.sh` doesn't yet append the
  pressure section (parity preserved on the fixture; real installs
  with Go get the feature, shell-only installs get it when the
  bash path grows the same logic — out of scope here).

### Coverage

- 17 tests `internal/decisions/` — every operator, both trigger
  shapes, parser validation, snapshot regex parsing, shadow-miss
  window exclusion, graceful absent-file handling.
- 7 subprocess tests `cmd/memoryctl/` — unknown subcommand, all-OK,
  pressure → exit 1, overdue → exit 1, JSON output parses, unknown
  flag, missing dir.
- 3 integration tests `internal/doctor/` + 3 in `internal/profile/`
  for the new check and append paths.

### Deferred to v0.12+

No items added to the deferred list — v0.11 shipped its full scope.
Carried over from v0.10.5: retroactive silent classification,
session-metrics wire-format fix, secrets detector, bench-memory CI,
further session-*.sh decomposition.

## [0.10.5] - 2026-04-23

Pure-refactor release preparing the hook layer for v0.11 and v0.12
additions. Five sections extracted from the two largest hooks into
dedicated `lib/` modules; 15 subprocess tests added for `memoryctl`.
No schema or behavioural changes; every v0.10.4 assertion still
passes byte-for-byte.

### Refactored

- **`hooks/session-stop.sh` 608 → 346 lines** (−43 %):
  - `lib/rotation.sh` — `lifecycle_rotate()` (147 lines).
  - `lib/outcome-detection.sh` — `classify_outcome_positive()` (84 lines).
  - `lib/feedback-loop.sh` — `classify_feedback_from_transcript()` (100 lines).
- **`hooks/session-start.sh` 785 → 680 lines** (−13 %):
  - `lib/detect-domains.sh` — `detect_domains()` (93 lines).
  - `lib/health-check.sh` — `compute_health_warning()` (65 lines).
- Combined hook line count **1393 → 1026** (−26 %).

Remaining content in both files is tightly coupled (REMINDER
assembly in session-stop, scoring/collect-mistakes in session-start)
and needs a design pass before further extraction — not just a
mechanical move. Out of scope for this release.

### Added

- **15 subprocess tests in `cmd/memoryctl/main_test.go`** covering
  top-level dispatch (no-args, -h, unknown command), `fts` (no
  subcommand, unknown subcommand, invalid `query` limit, missing
  args), `dedup` (no subcommand, unknown subcommand), `tfidf`
  (unknown subcommand, full `rebuild` against a 3-file fixture),
  and `doctor` (clean fixture exits 0, `--json` parses, missing
  memory_dir exits 1, unknown flag exits 2). Every dispatch branch
  and most subcommand error paths are exercised.
### Meta on coverage numbers

`go test -cover ./cmd/memoryctl/` still reports 0 % because the
subprocess-invoked binary's coverage data doesn't aggregate into
per-package statistics without shared `GOCOVERDIR` + `go tool covdata`
post-processing. The functional coverage is high — every dispatch
branch is exercised — but the tooling to surface it as a number is
deferred. `internal/fts` (45.3 %), `internal/dedup` (85.8 %),
`internal/fuzzy` (90.4 %), `internal/doctor`, `internal/tfidf` are all
well-covered via in-process tests and show real numbers.

### Deferred to v0.10.6+

Unchanged from v0.10.4's deferred list minus one item. Carried over:

- Retroactive silent classification in `wal-compact.sh`.
- `session-metrics` wire-format fix (v1 → v2 major bump; now
  gated on the v1.0 external-review signal per
  `docs/plans/2026-04-23-roadmap-v0.10.5-onward.md`).
- Secrets detector on PreToolUse.
- `bench-memory.sh` CI integration.
- `cmd/memoryctl` coverage aggregation tooling (subprocess → number).
- Further `session-start.sh` / `session-stop.sh` decomposition if
  a v0.11 / v0.12 feature forces touching the tightly-coupled parts.

## [0.10.4] - 2026-04-23

Observability + integrity follow-up. Closes a strange-loop in the FTS index, surfaces previously-dropped WAL signal in the analytics report, unblocks the substring-trigger channel on fresh installs by opening two templates, and backfills the `docs/decisions/` directory that prior releases kept referencing but never shipped.

### Fixed

- **FTS index excluded auto-generated derivative views.** Before this patch `~/.claude/memory/index.db` contained `self-profile.md`, `_agent_context.md`, and `MEMORY.md` — three files that are periodic snapshots of the state the index is supposed to help rank. A future active-injection path (shadow becoming an injection source, or SessionStart scanning the root) would have closed the loop: Claude reads "0% precision" from self-profile as knowledge → writes regenerated memory → self-profile re-indexed. Both `bin/memory-fts-sync.sh` and `internal/fts/sync.go` now filter by basename; invariant recorded in `docs/ARCHITECTURE.md`. New `internal/fts/sync_test.go` pins the exclusion list and the behavioural contract; `internal/fts` line coverage rises from 8.3% (flagged in the v0.10.3 deferred list) to 45.3%.
- **`docs/ARCHITECTURE.md` stale "sharp edges" removed.** Three entries documented as `→ v0.10.2` were in fact addressed — `incrementRecurrence` WAL ordering (v0.10.2), `incrementRecurrence` race (v0.10.3), `fmt.Sscanf` silent error (v0.10.3). Document was lagging behind code; cleaned up.

### Added

- **`memory-analytics.sh` reads three more WAL event classes.** Hooks emit ~27 distinct event types; the pre-v0.10.4 analytics consumed 8. Three new sections in `.analytics-report`, each emitted only when its source counters are non-zero:
  - **`## Recall gaps`** — top-10 slugs the FTS5 shadow surfaced that the substring-trigger pipeline missed. Direct feedstock for `triggers:` expansion.
  - **`## Trigger usefulness`** — top-10 by silent count with useful/silent breakdown per slug. Surfaces ambient-but-not-marked rules (high silent, zero useful) and evidence-mismatched rules.
  - **`## Schema & format health (last 30d)`** — aggregate counts of `schema-error`, `format-unsupported`, `evidence-missing`, `evidence-empty`. Trend view to complement `memoryctl doctor`'s binary WARN. Portable insertion-sort helper keeps the path BSD-awk-safe (no `asort`).
- **`docs/decisions/` directory with 8 architecture decision records.** The `decisions/` memory type has existed since v0.1 and `templates/decision.md` since v0.10.1, but the actual decisions that shape this repo lived in the author's `~/.claude/memory/decisions/` — invisible to any reader who isn't the author. Files added: `file-based-memory`, `wal-four-column-invariant`, `silent-fail-policy`, `two-scoring-pipelines`, `bash-go-gradual-port`, `fts5-shadow-retrieval`, `substring-triggers-with-negation`, `precision-class-ambient`. `docs/decisions/README.md` indexes them; `docs/CONFIGURATION.md §3` now links to the real files instead of dangling placeholders.
- **`templates/mistake.md` and `templates/knowledge.md` ship with live `triggers:` field.** `feedback.md` and `strategy.md` already had open fields; the two most-used templates had them as commented-out examples only. A naïve copy-paste produced a record with the substring-trigger channel silently disabled — the exact first-use trap documented in the v0.9.1 CHANGELOG post-mortem. Placeholders are formal enough that they cannot substring-match realistic prompts — users must edit them before the record fires.

### Deferred to v0.10.5+

Unchanged from v0.10.3's deferred list minus items addressed in this release:

- **Retroactive silent classification in `wal-compact.sh`** — sessions older than 24 h with inject events but no closing signal would get `trigger-silent` emitted retroactively. Blocked on design (event tagging without violating the 4-column invariant).
- **`session-metrics` wire-format fix** — column 4 should be session_id, not metrics-pairs. Requires a format v1 → v2 major bump.
- **Secrets detector on PreToolUse** — blocked on false-positive calibration.
- **`bench-memory.sh` CI integration** — blocked on percentile-vs-baseline threshold design.
- **`cmd/memoryctl/` Go coverage** — still 0%; blocked on CLI-test-shape decision.
- **`session-start.sh` / `session-stop.sh` decomposition** — pure refactor, deferred until a bug or feature forces the touch.

## [0.10.3] - 2026-04-23

Audit-pass release. Bundles findings from two external audits conducted on v0.10.2 (a paired technical + release-readiness review in `hypomnema-v10.2-comprehensive-audit.md`, and a Theory of Functional Systems review in `hypomnema-tfs-audit.md`), a CI failure that surfaced a gap in hook-child draining, and one legacy-symlink cleanup the doctor subcommand exposed on the author's own install. Cut-line document: `docs/plans/2026-04-23-v0.10.3.md`.

### Fixed

- **`install.sh` missing hook registrations** (audit CRITICAL). Fresh installs had PreToolUse, both PostToolUse entries, and PreCompact symlinked into `~/.claude/hooks/` but never activated in `settings.json` — dedup, outcome/cascade signals, Bash error-detect, and PreCompact reminders were silently off. Registration rewritten to match by command string inside the hooks array, so hypomnema coexists with users' own unrelated hooks instead of being skipped when the event key is already present.
- **`internal/dedup.Run` posttool merge race** (audit HIGH). `incrementRecurrence` + `os.Remove` now run under the WAL lock (`wal.Acquire`) — closes the lost-update window between concurrent `memoryctl dedup check` and PostToolUse:Write invocations merging into the same existing mistake. `TestPosttool_ConcurrentMergesIncrementSerially` pins it (20/20 FAIL without the lock, 50/50 PASS with).
- **`hooks/test-memory-hooks.sh` Linux CI race** — `rm -rf` of test fixtures raced backgrounded hook children still writing into them. The v0.10.2 drain waited only for `memory-fts-shadow.sh`; added `regen-memory-index`, `memory-index`, `memory-analytics`, `memory-strategy-score`, `memory-self-profile`, `wal-compact` via a shared `safe_cleanup` helper applied to all 77 `rm -rf` call sites.
- **`cmd/memoryctl/main.go` fts query limit** — `fmt.Sscanf` silently swallowed parse errors; now `strconv.Atoi` + explicit error for non-integer or ≤0 input (exit 2 with stderr message). ARCHITECTURE.md flag cleared.
- **`install.sh` stale legacy symlinks.** Post-link sweep: for each symlink in `~/.claude/hooks` and `~/.claude/bin`, if its target is inside the repo but no longer exists, drop it. User-owned symlinks pointing elsewhere are untouched; fresh installs sweep 0. Verified on the author's install — swept `memory-dedup.py` left over from the v0.10.0 Python-drop.

### Added

- **`memoryctl doctor` subcommand.** On-demand read-only health check: claude_dir, memory_dir, settings.json hook registrations (9 checks, including open-quanta ratio for last 30 days — see TFS below). Text or `--json` output. Exit 0 on OK/WARN, 1 on FAIL. First run on the author's install surfaced the stale `memory-dedup.py` symlink and 13 `evidence-empty` events. 9 unit tests.
- **`internal/tfidf` package + `memoryctl tfidf rebuild`.** Go port of `hooks/memory-index.sh` with a `unicode.IsLetter` / `unicode.IsDigit` tokenizer, replacing the byte-class `[^a-zA-Z\300-\377]` awk regex that corrupted Cyrillic / CJK / Greek / Arabic bodies. Output TSV schema identical on Latin corpora (verified by the new `make parity` case `tfidf latin-only-fixture`). Atomic tmp+rename. 13 unit tests. Bash fallback stays — pure-shell installs keep the Latin-only limitation as a documented trade-off.
- **Forward-compat gate on `format_version > 1`** in UserPromptSubmit. Files declaring future schema versions are logged (`format-unsupported|<slug>:N|<session>` — packed to preserve FORMAT.md §5.2 four-column invariant) once per day per slug and skipped. New event documented in FORMAT.md §5.1. 4 new asserts (258 → 262).
- **Open-quanta check in `memoryctl doctor`** (`open_quanta_last_30d`). Reads `.wal`, correlates inject events with trigger-useful / trigger-silent / outcome-* / clean-session on the same session_id. WARN at ≥50%. Surfaces the Bayesian `eff` measurement bias documented in `docs/notes/measurement-bias.md` — on the author's corpus, 93% of inject-carrying sessions in the last 30 days have no closing signal.
- **`LICENSE` file** (MIT) with real copyright. README had claimed MIT since v0.1 but the file was missing; GitHub auto-detection now returns `mit`.
- **README comparison section** against CLAUDE.md, Anthropic Skills, mem0, ChatGPT/Cursor memory, Obsidian+MCP. Hero line rewritten utilitarian-first; Greek epigraph moved to an HR-separated epilog.
- **`docs/MIGRATION.md` backfill** for v0.8→v0.9, v0.9→v0.10 (the Python-drop breaking change with explicit `rm -f ~/.claude/bin/memory-dedup.py` cleanup command for users upgrading without the v0.10.2+ sweep), and a summary block for the v0.10 patch band.
- **`docs/notes/measurement-bias.md`** — TFS Phase 6 finding documented: three known causes of open quanta (`MIN_SESSION_SECONDS=120`, missing `transcript_path`, hard exits), current mitigations (Laplace smoothing, `MIN_INJECTS_WINNER=5`), four proposed fixes in effort order. Also documents the side finding that `session-metrics` writes column 4 = metrics-pairs instead of session_id, violating FORMAT.md §5.2.
- **`docs/plans/2026-04-23-v0.10.3.md`** — cut-line document. Enumerates each commit with its audit source and parks the deferred items with "why not now" notes.

### Deferred to v0.10.4+

Re-scoped from the v0.10.2 list plus new items from this release's work:

- **Retroactive silent classification in `wal-compact.sh`** (new; from TFS audit). Sessions older than 24 h with inject events but no closing signal would get `trigger-silent` emitted retroactively, restoring the Bayesian denominator. Blocked on design: event tagging without violating the 4-column WAL invariant, interaction with analytics' rolling window.
- **`session-metrics` wire-format fix** (new; found during TFS audit analysis). Column 4 should be session_id, not metrics-pairs. Requires format v1 → v2 major bump — breaking change, strategic decision.
- **Secrets detector on PreToolUse** (audit MEDIUM security). Blocked on false-positive calibration for regex-only detection.
- **`bench-memory.sh` CI integration** (carried from v0.10.2 plan). Blocked on percentile-vs-baseline threshold design.
- **Go coverage gaps** — `internal/fts` (8.3%), `cmd/memoryctl` (0%). Mechanical; blocked on shape decision for CLI-level tests.
- **`session-start.sh` / `session-stop.sh` decomposition** (audit MEDIUM/LOW). Pure refactor, deferred until a specific bug or feature forces the touch.

### Meta

- Two external audits landed on `main@d154b41` within 24 h of each other; their convergence on `install.sh missing hook registrations` (Agent A CRITICAL for code, Agent B for UX) supported the decision to front-load it as the highest-priority fix of the release.
- The TFS audit was applied not to produce prescriptions but to surface latent patterns in the system's loops. Its highest-leverage finding — open quanta — is now measurable by any operator via `memoryctl doctor` and documented for future work on the Bayesian denominator.

## [0.10.2] - 2026-04-22

Follow-up to v0.10.1, bundling external review #2 findings (pass over v0.9.0→v0.10.1, 43 commits) with phase C internal review + misc cleanup. No schema or behavioural changes beyond the pinned-slot injection fix; all other items are bug fixes, doc accuracy, safety-net hardening.

### Fixed

- **PreCompact hook wire contract.** `docs/hooks-contract.md §7.2` documented the `hookSpecificOutput.additionalContext` envelope, but the hook has been emitting `systemMessage` since `9c99ce9`. §7.2 updated, §7.3 explains the rationale (Claude Code schema restricts `hookSpecificOutput.additionalContext` to PreToolUse/UserPromptSubmit/PostToolUse/SessionStart/Stop — PreCompact must use `systemMessage`).
- **`memoryctl` PATH resolution on fresh installs.** `hooks/memory-dedup.sh`, `bin/memory-fts-sync.sh`, `bin/memory-self-profile.sh` now try `$PATH` first, then fall back to `$HOME/.claude/bin/memoryctl`. External reviewer hit 255/258 silent dedup test failures until they added `~/.claude/bin` to their shell rc.
- **`hooks/memory-error-detect.sh` Russian output.** Last user-facing Russian string in the hook layer; missed by the v0.10.1 i18n pass because its cyrillic char count was below the audit threshold.
- **`hooks/session-start.sh` pinned-slot eviction.** Pinned files now bypass per-type caps, the total scored budget, and the zero-score throttle in `collect_scored`. `CLAUDE.md` already exempted pinned files from rotation; this extends "pinned == permanent" to injection-time. External review reproducer: `user-profile` (pinned, generic-domain) was evicted from `fly-next` CWD by 9 active project domains.
- **`uninstall.sh` hand-rolled symlink list replaced with directory-driven walk.** Removes any symlink under `$HOOKS_DIR` / `$BIN_DIR` whose target points into the hypomnema repo. Eliminates the "new hook added, forgot to update uninstall" class of bug that produced three missing symlinks in v0.10.1.
- **`internal/dedup/dedup.go` WAL ordering.** Posttool merge path now returns `Allow` if `incrementRecurrence` fails, instead of emitting a `dedup-merged` WAL entry for a merge that didn't happen.
- **`scripts/replay-runner.sh` locale leak.** Added `export LC_ALL=C` so `awk %f` renders `10.4%` on every locale, not `10,4%` on ru/uk/de/... Brings the runner into parity with `memory-analytics.sh` and `session-stop.sh`.
- **`README.md` test count and stack description.** Was `200/200/144/200` in four places, actual is 258. Stack updated from "bash + jq" to the full list (bash, jq, perl, awk, sqlite3-FTS5, optional Go). `make replay` surfaced in Testing section (flagged as hidden in review Part 4).
- **`docs/maintenance/RELEASE_CHECKLIST.md` Docker test.** Replaced `curl -LsSf https://astral.sh/uv/install.sh | sh` (uv dropped in v0.10.0) with `golang-go` + `make`; test run now exports `~/.claude/bin` onto `PATH`.

### Added

- **`hooks/memory-analytics.sh` tunable thresholds.** Four new env vars (`ANALYTICS_WINNER_EFF`, `ANALYTICS_NOISE_EFF`, `ANALYTICS_MIN_INJECTS_WINNER`, `ANALYTICS_MIN_INJECTS_NOISE`) let operators tune winner/noise classification without editing the awk block. Laplace-smoothing formula documented inline so future maintainers don't "simplify" it to naive `pos/(pos+neg)` without adjusting thresholds.
- **`internal/dedup` unit tests** (11 cases, coverage 0% → 85.8%). Covers pretool allow/block/candidate bands, posttool merge + the v0.10.2 ordering fix, missing-root-cause, block-scalar markers, empty mistakes dir, graceful degradation.

### Refactored

- **`hooks/memory-outcome.sh` cascade detection** now uses `parse_related` from `lib/parse-memory.sh` instead of a duplicate inline awk parser. Handles inline and block-form `related:` consistently with cluster expansion in SessionStart.

### Chore

- `.gitignore` covers `.tfidf-index*` and `.wal.*` backup patterns alongside existing runtime artefacts.
- Executable bits on `hooks/test-memory-hooks.sh`, `hooks/bench-memory.sh`, `scripts/replay-runner.sh`, `scripts/parity-check.sh` (flagged in external review Part 4).

### Deferred to v0.10.3+

- **`hooks/memory-index.sh` TF-IDF Latin-only tokenizer** — `gsub(/[^a-zA-Z\300-\377]/, ...)` truncates Cyrillic / CJK / Greek. Go FTS5 tokenizer already uses `unicode.IsLetter`; bash and Go diverge on this rule. Port to Go under a new parity case is the proposed fix, sized out of this PATCH scope.
- **`bench-memory.sh` CI integration** (`make bench` target with wall-clock thresholds).
- **Go test coverage for `internal/fts` (8%) and `cmd/memoryctl` (0%).** `internal/fts` is mostly SQLite plumbing that parity-check exercises end-to-end; `cmd/memoryctl` warrants a help/dispatch smoke test.

## [0.10.1] - 2026-04-22

Distribution-readiness patch. No behavioural changes; makes v0.10.0 work out-of-box for users who are not the author. No schema or WAL format changes. Users on v0.10.0 upgrade by pulling `main` or `git checkout v0.10.1`.

### Fixed

- **`hooks/regen-memory-index.sh` hardcoded project slug.** Default `INDEX_PATH` contained the author's username (`-Users-akamash`) — the auto-memory index silently landed in a directory with that name inside every other user's `$HOME`. Now derives the slug from `$CLAUDE_SESSION_CWD` (forwarded by `session-start.sh`) using Claude Code's own `-<PWD-with-slashes-as-dashes>` convention.
- **User-facing strings translated to English.** Self-profile boilerplate (`bin/memory-self-profile.sh`, `internal/profile/profile.go`), PreCompact warnings (`hooks/memory-precompact.sh`), and session-stop reminders (`hooks/session-stop.sh` — CONTINUITY/ERRORS/STRATEGIES checkpoints) switched from Russian to English. Russian stopwords (evidence tokenizer) and Russian negations (trigger detector) were already bilingual and left untouched. The `profile.go` ↔ `bin/memory-self-profile.sh` byte-for-byte parity contract verified by `scripts/parity-check.sh` preserved — translations applied identically to both.
- **`hooks/session-start.sh` user-profile grep pattern** now matches English role/expertise markers (`role`, `expertise`, `language`, `concise`, `english`, `writes in`) alongside the Russian originals.
- **`uninstall.sh` left three symlinks behind** on fresh-HOME installs: `memory-error-detect.sh`, `memory-precompact.sh` (hooks), and `memoryctl` (bin). `install.sh` is directory-driven, but `uninstall.sh` still hand-rolls the symlink list and fell behind. Surfaced by a clean-VM spot check per `docs/maintenance/RELEASE_CHECKLIST.md` step 3b. Minimal fix adds the three names; a directory-driven uninstall refactor to prevent the same class of drift is parked as v0.10.2 follow-up.

### Added

- **`seeds/wrong-root-cause-diagnosis.md`** — universal debugging mistake (EN evidence, `scope: universal`) now ships as a seed. Previously author-only in personal memory. Measured in author's dogfood corpus as the highest-leverage rule (recurrence 6, 231 inject events over 18 sessions).
- **`docs/CONFIGURATION.md`** — documents the three parameter layers: runtime `.config.sh`, decay thresholds in `session-stop.sh`, ADR review-triggers as prose in `decisions/`.
- **`templates/decision.md`** — hypomnema-native ADR template (What / Why / Trade-off accepted / When to revisit). The `decision` memory type had been declared in CLAUDE.md since early versions but no skeleton was shipped.

### Deferred to future releases

- **v0.11 — decisions autoreflection.** Structured `review-triggers:` frontmatter + `memoryctl decisions review` subcommand + `self-profile.md` "Decisions under pressure" section. Plan in `docs/plans/2026-04-22-v0.11-decisions-autoreflection.md`.
- **v0.12 — evidence learn.** Bootstraps personal evidence phrases from session JSONL transcripts, turning cold-start from a 30-session problem into a one-command operation. Plan in `docs/plans/2026-04-22-v0.12-evidence-learn.md`.

## [0.10.0] - 2026-04-22

Stack cleanup + first dogfood measurement tool. Drops Python from the project (bash + Go only now), promotes dedup onto `memoryctl`, and ships a synthetic replay runner so retrieval metrics can be measured without waiting N months for real sessions to accumulate.

### Breaking changes

- **`uv` + `rapidfuzz` + `bin/memory-dedup.py` are gone.** Fuzzy dedup now requires `memoryctl` on PATH (built via `make build`). Users who relied on `uv`-based dedup: run `make build && ./install.sh`. Users who never installed `uv`: dedup was already disabled — no action needed, the binary is still optional.
- **Project now uses two languages (bash + Go), not three.** Removes the Python runtime dependency entirely. Build requires Go 1.22+.

### Added

- **`memoryctl dedup check <file>` subcommand.** Drop-in replacement for the Python version, same pretool/posttool behaviour, exit codes (0 allow, 1 merge, 2 block), and WAL event shape (`dedup-blocked`, `dedup-merged`, `dedup-candidate`). Lives alongside `fts shadow`, `fts sync`, `fts query`.
- **`internal/fuzzy` package.** Pure-Go port of `rapidfuzz.fuzz.token_set_ratio` — `DefaultProcess`, `Ratio` (LCS-based indel similarity), `TokenSetRatio` (three-way max over intersection/diff pairings). Unit tests pin threshold decisions (block/candidate/allow) against known rapidfuzz outputs. Numerical drift tolerated up to ±10 points because rapidfuzz's default-processor path applies an internal normalisation we couldn't fully reverse-engineer; thresholded decisions are unaffected.
- **`internal/dedup` package.** Orchestrates the pretool/posttool decision: parse frontmatter root-cause, scan `mistakes/`, compare via `fuzzy.TokenSetRatio`, emit WAL events, handle the posttool merge (increment recurrence + delete new file).
- **`scripts/synthetic-corpus.txt`** — 48 hand-curated prompts across debugging, shell portability, Python/data, git, API, frontend, and devops domains.
- **`scripts/replay-runner.sh`** — batch-replay the corpus through UserPromptSubmit, produce per-prompt TSV + aggregate metrics. Invokes `memoryctl fts shadow` synchronously to dodge the hook's detached race against WAL reads. Accepts `--memory DIR` for dogfood against a real memory tree or defaults to a seeded fixture.
- **`make replay`** — one-shot measurement. Typical output on a day-one (seeds-only) memory:
  ```
  trigger-match hits : 1   (2.1% of prompts)
  shadow-miss events : 160 (89.6% of prompts)
  ```
  Confirms the field-review hypothesis: the substring-trigger channel is effectively dead when memory files ship with empty `triggers:`/`evidence:` — the FTS5 shadow pass is doing the retrieval work.

### Changed

- **`hooks/memory-dedup.sh`** — simplified to a single `memoryctl dedup check` delegation with the same graceful-degradation exit-0 policy as before.
- **QUICKSTART** — optional-dependency section rewritten for Go + `make build` instead of `uv` + `rapidfuzz`.
- **README** — requirements list updated; architecture diagram points fuzzy-dedup at `memoryctl`.
- **FAQ** — "why bash" answer updated: bash + Go now, Python gone.
- **docs/hooks-contract.md** — dependency-degradation section updated for `memoryctl`.

### Fixed

None this release — it's a stack-consolidation + instrumentation release.

### Migration

```bash
cd /path/to/hypomnema && git pull
make build
./install.sh
```

Done. If `make build` fails because Go isn't installed — either install Go (`brew install go` / `apt install golang`) or accept that fuzzy dedup stays off.

---

## [0.9.1] - 2026-04-22

Onboarding fix + pinned-semantic clarification. No code change in hooks or Go binary — strictly templates and contract docs. Backward compatible.

### Added

- **Working `triggers:` / `evidence:` examples in `templates/*.md`.** Field-usage review of v0.9.0 surfaced that new users copying a template got the substring-trigger channel silently disabled — the fields were absent from `mistake.md` and `knowledge.md`, and `feedback.md` had `evidence:` commented out. Without these fields populated, a memory file can only be ranked by keyword score at SessionStart; the reactive UserPromptSubmit path never fires. Templates now ship with commented-out example triggers/evidence alongside the documented shape, so copy-paste produces something one uncomment away from working.
- **`FORMAT.md § 3.1 — what `status: pinned` guarantees.** A reviewer surfaced that `user-profile.md` (pinned knowledge) was evicted from the flex-pool when a project had 9 active domains, and asked whether that was intended. This section documents the answer explicitly: `pinned` = rotation protection, not injection-time slot reservation. The flex-pool is allocated by score regardless of status, and the trigger-priority key uses `pinned` only as one dimension among seven. If a user needs hard-guaranteed injection, the current contract does not express that; a workaround via `scope: universal` + broad `triggers:` is noted but not promised.

### Contract status

No wire-format or on-disk changes. The pinned clarification is documentation of already-implemented behaviour, not a change to it. Files tagged `pinned` on a v0.9.0 install behave identically under v0.9.1.

### Known open work (not in this release)

- Guaranteeing a slot for `pinned` files in the flex-pool (option `(a)` from session notes) remains deferred pending dogfooding data. Doing it without usage data risks breaking the exact flow the change is meant to protect.

---

## [0.9.0] - 2026-04-22

Infrastructure & portability release. First Go component lands (`memoryctl`, drop-in for the FTS5 shadow pass) alongside a formal on-disk contract (FORMAT.md + hooks-contract.md), matrix CI (ubuntu-latest + macos-latest), and a cleanup uninstall path. Five BSD/GNU portability bugs were caught and fixed by the bash-vs-Go parity gate the pilot installed — the headline justification for the hybrid architecture.

### Added

- **`bin/memoryctl` (Go binary, opt-in).** First Go component in a planned hybrid architecture. Subcommands:
  - `memoryctl fts shadow` — byte-for-byte drop-in for `bin/memory-fts-shadow.sh`, used by UserPromptSubmit. Pure-Go SQLite driver (`modernc.org/sqlite`, CGO_ENABLED=0) means zero cross-compile pain and trigram FTS5 always available regardless of the system `sqlite3`.
  - `memoryctl fts sync` / `fts query` — standalone equivalents of the bash sync + query scripts.
  - Install picks it up automatically if `make build` produced the binary; otherwise bash scripts remain authoritative. No user-visible behaviour change.
- **`docs/FORMAT.md` (v1.0 contract).** Single source of truth for on-disk shape: directory layout, frontmatter schema (universal + per-type), WAL grammar (19 defined event types), derivative indices, parsing tolerances, backward-compat rules. Any implementation claiming format compatibility obeys this document.
- **`docs/hooks-contract.md` (v1.0 contract).** Companion document: stdin/stdout JSON shape for every hook (`SessionStart`, `Stop`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PreCompact`), exit-code semantics, environment variables (`CLAUDE_MEMORY_DIR`, `HYPOMNEMA_TODAY`, `HYPOMNEMA_SESSION_ID`), fail-safe rules.
- **`uninstall.sh`.** Surgical reverse of `install.sh` — removes only our symlinks, strips only our entries from `settings.json` (preserves foreign hooks), keeps `~/.claude/memory/` by default. `--purge-memory --yes` for complete removal.
- **`RELEASE_CHECKLIST.md`.** Manual gate before `git tag`. Section 3 (clean-VM bootstrap) is REQUIRED, not optional — lesson from the five portability bugs that shipped from a macOS-only dev loop.
- **`.github/workflows/test.yml`.** Matrix CI (`ubuntu-latest` + `macos-latest`) runs install → full bash test suite → Go unit tests → `make parity` (bash vs Go shadow diff on identical fixtures). Separate `shellcheck -S error` job across all shell scripts.
- **`HYPOMNEMA_TODAY` env override** in 6 production hooks (`session-start`, `user-prompt-submit`, `session-stop`, `memory-outcome`, `memory-analytics`, `memory-error-detect`). Tests freeze "today" so fixtures with hardcoded WAL dates don't drift out of the 30-day scoring window over time.
- **`install.sh` preflight: sqlite3 FTS5 probe.** Detects sqlite3 binaries that lack the FTS5 module (Android SDK's cut-down version is the common culprit on macOS runners) and fails fast with the path to the offending binary and how to fix PATH.
- **`install.sh` directory enumeration.** Replaces 18 hand-rolled `ln -sf` lines with two loops over `hooks/*.sh` and `bin/*.{sh,py}`. New files get picked up automatically — the "added a hook, forgot to update install.sh" class of bug is eliminated at the source. Adds three previously-missing symlinks: `memory-error-detect.sh`, `memory-precompact.sh`, `memory-dedup.py`.
- **QUICKSTART / TROUBLESHOOTING / README** expanded: optional `uv`/`rapidfuzz` dependency documented, `precision_class: ambient` cross-referenced in TROUBLESHOOTING, upgrade + uninstall snippets in README.

### Fixed

- **`bin/memory-dedup.py` PreToolUse path** (P0 from external review). The script had `if not new_file.exists(): sys.exit(0)` at the top, which short-circuited every PreToolUse invocation before reading stdin. Split `main()` by `pretool_call = not new_file.exists()`; pretool path reads candidate content from stdin, parses root-cause, blocks on ≥80% similarity with a `dedup-blocked` WAL event and exit 2 (what the shell wrapper had always expected). Three previously-failing tests now pass end-to-end.
- **Hard-coded `/Users/akamash/Development/hypomnema/` paths in test suite (14 sites).** Replaced with a `HOOKS_SRC_DIR` header that follows symlinks, so the suite works under any `$HOME`.
- **BSD vs GNU `sed 1,/pat/`** (4 sites). GNU extends the range to the next match when line 1 matches, wiping everything between both frontmatter delimiters. Replaced with the single-range form `/^---$/,/^---$/d` — works identically on both.
- **BSD vs GNU `grep "\\t"`** (R2 test assert). BSD grep interprets `\t` as TAB in the pattern; GNU grep treats it literally. Replaced with the `awk -F$"\t"` idiom already used by sibling asserts.
- **BSD vs GNU `stat -f` / `stat -c`** (`bin/memory-fts-sync.sh`). `-f` is BSD-only; on Linux the sync silently produced empty mtimes and left the FTS index at zero rows. Added the one-shot probe pattern from `hooks/lib/stat-helpers.sh`.
- **`LC_ALL=en_US.UTF-8`** in `bin/memory-fts-query.sh`. Not guaranteed to be generated on Linux runners; replaced with the portable `C.UTF-8`.
- **`install.sh` exit code on `--skip-base`.** The trailing `[ "$DISCOVER" -eq 1 ] && fn` pattern returned rc=1 under `set -e` when flags were 0. Converted to explicit `if/then`.
- **`memory-analytics.sh` never symlinked.** Called by `session-stop.sh` (guarded, silently no-oped) and by the test suite (died with exit 127). Added to the installer's symlink enumeration.
- **`bin/memory-fts-shadow.sh` `set -uo pipefail` placement.** Sat below a 20-line header comment; the `shell-safety` test probe (`head -10 | grep pipefail`) flagged it as missing. Moved under the shebang.
- **WAL float truncation in `hooks/session-start.sh:451`** (Spaced repetition + Bayesian test flakes). `printf "%d"` truncated the float WAL component to 0 when `raw < 1`, losing hash-order ties in sort. Scaled by 100 before int cast; preserves component proportions, keeps downstream bash `-gt 0` / `-eq 0` tests integer-compatible.
- **`hooks/lib/{evidence-extract,wal-lock}.sh`** — added `# shellcheck shell=bash` directive (were missing shebangs → SC2148 errors under `-S error`).
- **`bin/memory-fts-{sync,query}.sh`** — respect `CLAUDE_MEMORY_DIR` per FORMAT.md §1.4 (they hardcoded `$HOME/.claude/memory` and broke fixture-based tests).

### Changed

- **Directory enumeration in `install.sh`** already described above — mentioned again here because the rename-preservation table (`session-start.sh` → `memory-session-start.sh`, `consolidate.sh` → `memory-consolidate.sh`, …) moved from hardcoded lines into a single `_rename_dest` case. bash 3.2-safe (no `declare -A`).
- **`hooks/test-memory-hooks.sh`** — now executable bit set (`chmod +x`). Same for `hooks/bench-memory.sh`.
- **`.gitignore`** — `bin/memoryctl` + runtime artefacts (`.wal`, `.analytics-report`, `index.db`, `self-profile.md`, `.runtime/`, `.cache/`).

### Breaking changes

None. All additions are opt-in or documented-equivalent-to-existing-behaviour. Format v1 is what the bash implementation was already producing; documenting it as a contract is non-breaking for readers.

### Contributor workflow

- `make build` → compiles `bin/memoryctl`.
- `make test` → runs bash + Go suites.
- `make parity` → diffs bash and Go shadow on fixtures; acceptance gate for the hybrid architecture.
- `./uninstall.sh` → reverse of install; safe to run any time.

---

## [0.8.2] - 2026-04-20

FTS5 recall shadow (signal-only; no behavioural change). Retrospective CHANGELOG entry — the commit shipped 2026-04-20 but the entry was not written at the time.

### Added

- **`bin/memory-fts-shadow.sh`.** A parallel pass invoked (detached, fail-safe) from `hooks/user-prompt-submit.sh`. Runs FTS5/BM25 over memory body content for every prompt and writes `shadow-miss|<slug>|<session>` WAL events for files the substring-trigger pipeline did *not* inject. Signal is diagnostic, not prescriptive — it feeds trigger-tuning ("this file shadow-hits repeatedly but never triggers directly → its `triggers:` are too narrow"). Does not alter injection.
- **`bin/memory-fts-sync.sh` + `bin/memory-fts-query.sh`.** Supporting infrastructure: idempotent FTS5 index at `~/.claude/memory/index.db`, lazy-sync on every query (two-stage drift check), trigram tokenizer for Cyrillic + Latin.

### Note

The `shadow-miss` WAL event type was defined retroactively in `docs/FORMAT.md` §5.1 under the 0.9.0 contract.

---

## [0.8.1] - 2026-04-20

Metric hygiene release. Five days of v0.8 dogfooding surfaced two measurement defects and a WAL noise problem. Fixes are small and surgical — one optional frontmatter field, no new hooks. Backward compatible.

> **Note on versioning.** The repo uses standard semver for release tags; the conceptual capability tiers in `notes/memory-system-roadmap.md` (v0.7 "Самопознание", v0.8 "Рефлексия", …) are a different axis. This 0.8.1 tag is a patch on top of the 0.8.0 audit release — the roadmap capability tier remains at **v0.7-A/B** (self-profile + strategy-score shipped 2026-04-13). v0.7-C (meta-pattern detector) is still deferred pending ≥25 mistakes in the corpus. v0.8 "Рефлексия" is not started and is gated on validating current mechanisms against real data first (see H1 open question below).

### Fixed

- **Methodological bias in precision metric** (`bin/memory-self-profile.sh`) — the `trigger-useful / trigger-silent` split treated all injected rules as explicitly-cited or noise. Rules that shape behaviour silently (language preference, security baseline, meta-philosophy) were structurally incapable of producing `trigger-useful` events and were counted as noise, dragging reported precision to 27% while actual useful signal was 53%. Introduced `precision_class: ambient` frontmatter flag — self-profile now excludes ambient files from the precision denominator and reports them as a separate `ambient activations` line.
- **Broken evidence matching on `wrong-root-cause-diagnosis`** (user memory) — the top-1 universal mistake had no `evidence:` field and an empty body, so the v0.7.1 detector could not match it against assistant text. It accumulated `recurrence: 6` without ever registering `trigger-useful`. Added 9 evidence phrases (ru + en) and body content with explicit hypothesis-enumeration markers. This is a content fix in user memory, not a code change — documented here because the infrastructure story matters for audit.
- **`rotation-summary` WAL spam** (`hooks/session-stop.sh:172-175`) — lifecycle-rotation wrote a summary row on every Stop event, producing 40+ identical `checked_N|stale_0,archived_0` lines per active day. Now writes only when stale or archive count is non-zero. The rare `rotation-stale` / `rotation-archive` events remain (they already fire only on actual activity).

### Added

- **`precision_class` frontmatter field** — optional, values: `ambient` (excluded from precision denominator). Future values reserved for `factual` (cited-or-not is meaningful) and `measurable` (default, current behaviour).
- **`silent-applied` metric** (self-profile) — measurable silent events that coincide with an `outcome-positive` event in the same session are reclassified as silent-applied (rule was applied without explicit citation). Reported alongside `trigger-useful` in the effective-precision calculation.
- **`evidence-empty` and `outcome-new` readers** (self-profile) — both events existed in the WAL since v0.7.1 but had no consumer. Self-profile now surfaces `evidence-empty rules (unique)` as a tuning target (rules that cannot match by design — candidates for adding `evidence:` or deletion) and `outcome-new` as a learning-rate indicator.

### Metric output

Self-profile `Meta-signals` table grew from 8 rows to 13, separating ambient activations from the measurable pool and surfacing the previously-hidden noise denominator (`silent-noise`) as the concrete list to tune.

### Dogfood validation

- Before: reported precision 27% (misleading — included ambient rules as noise).
- After: measurable precision 53%, ambient activations 13, evidence-empty 6 (actionable tuning list), silent-noise 37 (remaining real noise).

### Open question (H1 — gates roadmap v0.8 "Рефлексия")

Does an injected rule actually change behaviour? `wrong-root-cause-diagnosis` has recurrence 6 despite being top-1 universal and injected in every session. Previously the metric was blind to this: the file had no `evidence:` and an empty body, so the detector could never register `trigger-useful`. Content fix landed in user memory as part of this work; the next 5 debug sessions will produce a first honest answer. If the rule continues to silent-fire despite being matchable, text-injection is the wrong mechanism for behaviour change and the "Рефлексия" tier needs rethinking before any debrief/prediction infrastructure is built.

---

## [0.8.0] - 2026-04-15

Major audit + safety release. Four-agent code review (architecture, functionality, bugs, onboarding) surfaced 5 critical fixes, large onboarding gaps, and architecture seams. v0.8 ships safety fixes, a frictionless install path with interactive wizard, repo-root agent protocol, four onboarding docs, and three new shared `lib/` modules. Backward compatible.

### Fixed (critical)

- **Perl regex injection in section markers** (`session-start.sh:1010-1023`, `:1117`) — slugs containing regex metacharacters (e.g. `foo)bar`) crashed perl in the `[ПРИОРИТЕТ]` tagging pass and the cascade-review pass, silently nuking section content. Added `_perl_re_escape` helper using `quotemeta`.
- **WAL read race in feedback loop** (`session-stop.sh:466`, `:354`, `:358`, `:435`) — four `awk` reads of `$WAL_FILE` now wrap in `wal_run_locked`. Eliminates undercount of `inject` events and over-reporting of `trigger-silent` when multiple sessions run concurrently.
- **`set -o pipefail` audit** — added to 9 hooks/bin/install scripts where missing. Test 23 enforces at meta level. `set -e` deliberately NOT added to graceful-degradation hooks (`session-start/stop`) — they intentionally use `|| true` patterns.
- **`install.sh` pre-flight checks** — missing `jq`/`perl`/`awk`/`bash 3.2+`/`~/.claude` now fails fast with clear errors instead of silently corrupting `settings.json`.
- **UTF-8 aware `evidence_from_body`** (`hooks/lib/evidence-extract.sh`) — replaced byte-oriented BWK awk tokenization with perl `\w{4,}` under `/u` flag. Cyrillic, CJK, Greek, mixed-language memories now tokenize cleanly without explicit `evidence:` field.

### Added

- **Repo-root `CLAUDE.md`** — agent protocol with full schema and obfuscated examples (mistake/strategy/feedback). New agents entering the repo learn the memory system on first read.
- **`docs/QUICKSTART.md`** — 5-minute zero-to-first-injection walkthrough.
- **`docs/TROUBLESHOOTING.md`** — common failures (silent injection, missing jq, broken symlinks, WAL growth, fresh-start procedure).
- **`docs/FAQ.md`** — cross-machine sync via git, upgrades, scope semantics, scripting patterns, evidence in non-ASCII.
- **`docs/MIGRATION.md`** — v0.7 → v0.8 changes (no required actions, all backward-compatible).
- **`install.sh` interactive wizard:**
  - `--discover` — scans `~/Development`/`~/code`/`~/projects`/`~/src` for git repos, prompts y/n/q, jq-merges accepted entries into `projects.json`.
  - `--patch-claude-md` — appends a four-line memory section to `~/.claude/CLAUDE.md`, idempotent via marker comment.
  - `--dry-run` — preview without writes.
  - `--skip-base` — run only flagged actions on already-installed systems.
  - `--help` — full usage with examples.
- **`templates/projects.json.example`** — annotated config template.
- **`~/.claude/memory/.config.sh`** — runtime constants (caps, limits) externalized; install copies `templates/.config.sh.example` on first run. Existing installs continue using built-in defaults.
- **Concurrent-session smoke test** (Test 25) — verifies parallel SessionStarts do not corrupt WAL or dedup lists.

### Refactor

- **`hooks/session-start.sh` decomposed** from 1289 → 722 lines (−44%). Heavy lifting moved to three new lib modules:
  - `hooks/lib/parse-memory.sh` — `AWK_MISTAKES`, `AWK_SCORED`, `parse_related`, `find_memory_file`, `domain_matches`
  - `hooks/lib/score-records.sh` — `compute_wal_scores` (spread × decay × Bayesian effectiveness), `compute_tfidf_scores`, `load_noise_candidates`
  - `hooks/lib/build-context.sh` — `expand_clusters` (forward + reverse + cross-injected scans + provenance), `apply_priority_markers`, `assemble_context`, `apply_cascade_markers`
- **`hooks/lib/detect-project.sh`** — shared `detect_project()` helper. Eliminates 12-line duplicate block in `session-start.sh` and `session-stop.sh`.
- **`hooks/lib/stat-helpers.sh`** — portable `_stat_mtime`/`_stat_size`. Replaces 5 inline `stat -f %m vs stat -c %Y` blocks. One typo on the wrong OS would have silently disabled lifecycle rotation.

### Known non-issue

- **Multi-word `domains:` frontmatter values** — currently split on space after YAML parse. Use kebab-case (`machine-learning`) as workaround. Fix requires deeper YAML parsing; out of scope for v0.8.

### Tests

183 → 200 (17 new assertions: Test 21 perl injection, Test 22 WAL lock assertion, Test 23 shell-safety meta, Test 24a/b/c Cyrillic + mixed + ASCII regression for evidence_from_body, Test 25 concurrent sessions). ALL TESTS PASSED.

### Architectural notes

Four-agent code review scored: architecture 6.5/10, functionality 8.5/10, bugs 6.5/10, onboarding 5.5/10. v0.8 closes critical bug + onboarding gaps AND lands the architecture decomposition. See `docs/plans/2026-04-15-v0.8-cleanup-and-onboarding.md` for full task breakdown.

## [0.7.1] - 2026-04-15

Фикс мёртвого feedback-детектора. v0.7 dogfooding показал **1472 инжекта, 0 `trigger-useful`, 39 `trigger-silent`** — сигнал полностью не работал, потому что `session-stop.sh:486` делал `grep -F "$slug"` по имени файла памяти в тексте ассистента, а на практике правило применяется молча ("добавил tz-aware datetime", без упоминания `code-approach`).

### Fixed

- **`session-stop.sh` feedback detector** — заменён slug-grep на content-based matching через новую библиотеку `hooks/lib/evidence-extract.sh`. Теперь сигнал отражает реальное применение правила.

### Added

- **`hooks/lib/evidence-extract.sh`** — две чистые функции:
  - `evidence_from_frontmatter` — парсит опциональное поле `evidence:` (YAML-массив) из frontmatter памяти. Порог: ≥1 фраза в ответе ассистента (case-insensitive substring) → `trigger-useful`.
  - `evidence_from_body` — fallback. Извлекает уникальные content-токены из тела памяти (исключает frontmatter, `**Why:**`/`**How to apply:**`, fenced code blocks, blockquotes, сам slug). Фильтры: длина ≥4, встроенный ~70-словный ru+en stop-list. Порог: ≥2 уникальных токена совпало → `trigger-useful`.
- **`evidence:` frontmatter field** — опциональный массив фраз-доказательств применения. Автор указывает концепты решения ("parameterized query", "tz-aware"), не проблемы.
- **Новые WAL-события:**
  - `evidence-missing|slug|session` — инжектнутый файл памяти не найден на session-stop (подозрение на сломанный MEMORY.md regen или удаление в сессии).
  - `evidence-empty|slug|session` — у памяти нет `evidence:` и body-mining дал 0 токенов. Сигнал, что память слишком короткая / целиком во frontmatter — кандидат на доработку автором.

### Signal bias

Детектор сознательно смещён в сторону `trigger-useful` (низкий порог). Обоснование: downstream auto-disable шумных триггеров должен избегать штрафа работающих памятей. False `silent` дороже false `useful`.

### Limitations

- Body-mining ASCII-ориентирован (BWK awk на macOS трактует Cyrillic побайтно). Памяти с кириллическим телом должны использовать явное `evidence:` поле.
- Memories с несколькими независимыми правилами (например `code-approach` с 5 правилами) могут всегда показывать useful по overlap любого из них. Если auto-disable потом это высветит — сигнал расщепить такую память на атомарные, что и так хорошая практика.

### Tests

177 → 183 (6 новых assert'ов; Test 20 перезаписан с slug-grep на content-based end-to-end + evidence-missing/empty/case-insensitive тесты). ALL TESTS PASSED.

### Dogfood validation

После установки: провести 2-3 сессии, проверить `awk -F'|' '$2=="trigger-useful"' ~/.claude/memory/.wal | wc -l` — ожидаем ≥1 (раньше 0). Если всё ещё 0 — добавить `evidence:` к core memories (`code-approach`, `debugging-approach`, `wrong-root-cause-diagnosis`).

---

## [0.7.0] - 2026-04-13

"Самопознание" — система теперь видит себя через WAL-метрики. Плюс гигиена v0.6 (H1-H4).

### Self-awareness (v0.7 MVP)

- **`bin/memory-strategy-score.sh`** — success_count в strategies/*.md синхронизируется из WAL `strategy-used` events. Идемпотентно: пересчёт с нуля на каждый вызов, запись только при изменении. Пишет `strategy-rescored` WAL-event при обновлении.
- **`bin/memory-self-profile.sh`** → `memory/self-profile.md` — агрегированный профиль: meta-signals (sessions, clean, outcome pos/neg, trigger useful/silent), strengths (top стратегии), weaknesses (top mistakes с маркером 🔴 для scope:universal), calibration (error-prone domains). Генерируется в фоне из session-stop.sh.
- **C (meta-pattern detector)** отложен до ≥25 mistakes в корпусе — на 14 записях кластеры слишком шумные.

### Hygiene (v0.6 backlog, closed)

- **H1. Avatar-notes cluster consolidated** — 8 файлов `notes/avatar-*` → 1 мастер (`avatar-unified-analysis.md`) + 7 источников в `notes/avatar-sources/` (не попадают в MEMORY.md благодаря `-maxdepth 1`).
- **H2. decay_rotation observability** — lifecycle_rotate в `session-stop.sh` теперь пишет в WAL: `rotation-stale`, `rotation-archive`, `rotation-summary|checked_N|stale_M,archived_K`. Раньше всё подавлялось `2>/dev/null || true` — нельзя было проверить, работает ли вообще.
- **H3. Scope field для mistakes** — новое поле `scope: universal | domain | narrow` в frontmatter. Логика в `session-start.sh`: `narrow` инжектится ТОЛЬКО при явном keyword-match, не по domain-фильтру. 14 mistakes мигрированы (1 universal, 5 domain, 8 narrow). Pinned bypass сохранён. Защищает SessionStart от шума узкоспецифичных ошибок (bcrypt/shell-escape, html2canvas-oklch и т.п.) в backend/frontend-сессиях.
- **H4. MEMORY.md regeneration timestamp** — `<!-- regenerated: YYYY-MM-DD HH:MM:SS -->` в шапке. Помогает заметить, если автогенерация сломалась.

### Infrastructure

- `install.sh` добавил симлинки для `memory-strategy-score.sh` и `memory-self-profile.sh` в `~/.claude/bin/`.

---

## [0.6.0] - 2026-04-12

Post-v0.5 audit revealed silent failures, data corruption risks, and ranking issues. This release ships all findings from a multi-agent review (reliability / matching / architecture).

### Reliability

- **WAL locking** — new `lib/wal-lock.sh` (portable `mkdir`-based lock, no external deps). Applied around `wal-compact.sh` read-modify-write and multi-line append loops in `user-prompt-submit.sh` and `session-start.sh`. Prevents interleaved writes from parallel sessions and lost events during compaction.
- **Runtime state moved out of `/tmp`** — dedup lists (`injected-${session}.list`) now live in `$MEMORY_DIR/.runtime/`. Fixes pollution (29 stale files had accumulated) and — as a side effect — eliminated silent test isolation failures (baseline was actually 136/144, not 144/144).
- **Hook timeouts bumped** — SessionStart 5→15s, UserPromptSubmit 3→10s. Previous values caused silent SIGTERM on cold start.
- **WAL auto-compaction in Stop** — previously only SessionStart triggered compaction. Stop hook now invokes `wal-compact.sh` (which self-checks threshold) to prevent unbounded growth between sessions.
- **Large stdout truncation** — `memory-error-detect.sh` caps `RESPONSE` at 50 KB before awk heredoc. Protects against megabyte-stdout memory blasts.
- **Session ID sanitization fix** — `user-prompt-submit.sh` now sanitizes both `|` and `/` (previously only `/`, causing WAL corruption for pipe-containing session IDs).
- **Bash 3.2 substring fix** — `${var: -N}` returns empty when N > len on macOS bash; negation window now uses explicit offset arithmetic.

### Trigger matching quality

- **Quote / code block filter** — fenced ` ``` `, inline backticks, and `> blockquote` lines stripped from prompt before substring-match. Eliminates the false positive where trigger phrase examples in cited text self-triggered.
- **Negation detection** — ±40 char window around each match scanned for не/без/уже/нет/already/fixed/skip/no/don't. Candidate dropped if negation found — "не используй X" no longer injects X.
- **Project filter** — `project:` frontmatter compared to cwd-derived current project. New `project_rank` (match=2 > global=1 > mismatch=0) is highest-priority key field. Cross-project triggers demoted below global/local matches.
- **Recency-boosted ranking** — `recency_rank` from `created:` date (3/2/1/0 for 0-7/8-30/31-90/91+ days) inserted between severity and ref_count. Fresh records surface above old popular ones within same severity.
- **Log-bucketed ref_count** — `log₁₀(ref+1)` replaces raw ref_count in priority key. 100 isn't 100× better than 1; diminishing returns prevent rich-get-richer domination.
- **New priority key**: `project → status → severity → recency → log(ref) → recurrence → ref_count`.

### Observability

- **MEMORY.md auto-regeneration** — new `regen-memory-index.sh` rebuilds `~/.claude/projects/.../memory/MEMORY.md` on SessionStart (background). Descriptions pulled from frontmatter `description` → `root-cause` → `trigger` → first body line.
- **`_agent_context.md` multi-rule support** — awk now captures all body lines until `**Why:**`, not just the first. Multi-rule feedback like `code-approach` (5 rules) no longer shows only rule #1.
- **Schema-error detection** — malformed frontmatter (missing closing `---`) produces `schema-error|<slug>|<session_id>` WAL event. De-duplicated per day.
- **Health warnings surface in SessionStart output**:
  - Broken symlinks / missing companion scripts (`lib/wal-lock.sh`, `wal-compact.sh`, `regen-memory-index.sh`) → actionable warning
  - 50+ injects with 0 trigger-matches in last 200 WAL entries → UserPromptSubmit likely failing
  - Accumulated `schema-error` events → surface slug names
  - Warnings now emit even when no memories would inject (previously early-exit hid them).

### Feedback loop (foundation for per-trigger precision)

- **`session-stop.sh` scans transcript** — for each injected slug in session, writes `trigger-useful` to WAL if assistant referenced slug by name, `trigger-silent` otherwise. Signal for future per-trigger precision aggregation and auto-disable of noisy triggers.

### Docs

- README `Frontmatter schema` updated with: `ref_count`, `decay_rate`, `description`, trigger matching rules (quote filter, negation, priority key), `schema-error` behavior.
- README project structure lists `.runtime/` and `.wal.lockd/`.

### Migration

- `install.sh` now symlinks `hooks/lib/` and `hooks/regen-memory-index.sh`. Existing installs: re-run `install.sh` or manually symlink. SessionStart health check will warn if missing.

### Tests

- **160/160** (up from 136/144 baseline — 8 of which were silently broken by `/tmp` pollution). New coverage: quote filter (4), negation (3), project ranking, recency ranking, severity-beats-fresh, feedback loop (2), schema-error (2), health surface (2).

---

## [0.5.0] - 2026-04-12

Context triggers. Memory files with declared trigger phrases activate on matching user prompts, complementing CWD/git-based scoring with direct task context reaction.

### Triggers in frontmatter

- **`triggers:` field** — array of case-insensitive phrases, e.g. `- "tailwind hsl"`
- **`trigger:` field** — single-phrase form (legacy, supported alongside `triggers:`)
- **Plain substring match** — no regex; phrases matched as case-insensitive substring against prompt text (works for Latin + Cyrillic)
- **Recommended usage** — mistakes with characteristic symptom phrases, strategies with explicit task triggers, knowledge with domain terminology

### UserPromptSubmit hook

- **New `hooks/user-prompt-submit.sh`** — activates on every user prompt
- **Dedup with SessionStart** — skips files already injected at session start (tracked via `/tmp/.claude-injected-${SESSION_ID}.list`)
- **Sorted candidates** — `status: pinned` > `active`; tiebreak by severity, recurrence, ref_count
- **Cap `MAX_TRIGGERED=4`** — prevents noisy injections on broad prompts
- **`## Triggered (from prompt)` section** — matched records appear under dedicated heading with `(matched: "phrase")` annotation

### WAL integration

- `trigger-match|<slug>|<session_id>` event logged per match
- `trigger-match` contributes to `pos_count` in WAL spaced-repetition scoring (weighted same as `outcome-positive`) — triggered files surface more readily in future sessions

### SessionStart changes

- **Dedup list write** — always writes `/tmp/.claude-injected-${SESSION_ID}.list` (even when no injections), enabling user-prompt-submit to detect a started session
- Writes happen before the `CONTEXT` early-exit to ensure the list exists for empty sessions

### Testing

- 144 smoke tests (+12 new: single trigger match, case-insensitive, array triggers (2 phrases), no-match silence, dedup, pinned priority, WAL logging, cap enforcement, superseded ignored, session-start dedup list)
- 18 benchmark scenarios — all passing, zero regressions

---

## [0.4.0] - 2026-04-12

Related links and graph. Memory files now connect to each other through typed relationships, forming clusters that activate together.

### Related links

- **`related:` field in frontmatter** — typed connections between memory files: `reinforces`, `contradicts`, `instance_of`, `supersedes`
- **Unidirectional links** — the more specific record declares the link; reverse references built on-the-fly during injection
- **Flat YAML map format** — `- target-slug: link_type`, compact and shell-parseable

### Cluster activation

- **Forward scan** — when a file is injected, its `related:` targets are loaded as a cluster
- **Reverse scan** — files that reference an injected file are also pulled in
- **Cross-relationship scan** — detects `contradicts` links between already-injected files
- **Score filter** — cluster candidates must have composite score > 0 (keyword OR WAL OR TF-IDF signal)
- **Budget cap** — max +4 cluster files above MAX_FILES (total up to 26), prioritized: contradicts > reinforces > instance_of > supersedes
- **Provenance markers** — cluster-loaded records show `(via source → type)` in output
- **`## Related` output section** — cluster-loaded records appear in a dedicated section

### Contradicts handling

- **`[ПРИОРИТЕТ]` marker** — the newer record (by `referenced` date) gets priority annotation on its header
- **`⚠ Конфликт` warning** — explicit warning when contradicting records are both injected
- **`superseded` status** — records with `status: superseded` are excluded from injection entirely

### Cascade signals

- **Cascade detection** — when a memory file is updated, `memory-outcome.sh` scans for `instance_of` children and logs `cascade-review` events to WAL
- **`[REVIEW]` display markers** — injected files with pending cascade-review (< 14 days) show `[REVIEW: parent updated YYYY-MM-DD]` on their header
- **Non-destructive** — cascade is a signal for Claude to check actuality, not automatic content modification

### WAL events

- `cluster-load|<slug>|<session_id>` — file loaded by cluster activation
- `cascade-review|<child-slug>|parent:<parent-slug>` — child needs review after parent update

### Testing

- 132 smoke tests (+24 new: cluster forward/reverse scan, provenance, WAL events, superseded exclusion, contradicts warning/priority, cluster cap, cascade detection cross-dir, cascade display)
- 18 benchmark scenarios — all passing, zero regressions

---

## [0.3.0] - 2026-04-11

Memory lifecycle. The system now tracks access frequency and ages records for archival.

### Lifecycle fields

- **`ref_count`** — auto-incremented on each injection; high ref_count = core memory
- **`decay_rate`** — `slow` (fundamentals, 180d stale), `normal` (project notes, 60d), `fast` (situational, 14d)
- **Auto-assignment** — decay_rate inferred from file type/location: projects and user-profile → slow, continuity and journal → fast, pinned → always slow

### ref_count tracking

- **Per-injection increment** — single perl call updates `referenced` date and `ref_count` atomically across all injected files
- **Backfill script** (`memory-backfill-refcount.sh`) — populates ref_count from WAL history for existing files
- **Enrichment script** (`memory-enrich-frontmatter.sh`) — adds missing `decay_rate` and `ref_count` fields to legacy files

### Archival (Stop hook)

- **Stale detection** — files not referenced within type-specific thresholds get `status: stale`
- **Auto-archival** — stale files exceeding archive threshold move to `archive/` subdirectory
- **Type-specific thresholds** — mistakes 60→180d, strategies 90→180d, knowledge 90→365d, feedback 45→120d, notes 30→90d
- **decay_rate override** — fast records stale at 14d/archive at 45d; slow records stale at 180d/archive at 365d
- **Projects exempt** — project files never archived

### Testing

- 3 new benchmark scenarios (ref_count auto-increment, ref_count feedback increment, decay_rate preserved after injection)

---

## [0.2.0] - 2026-04-11

Meta-analytics and positive patterns. The system now tracks what works, not just what breaks.

### Meta-analytics

- **Outcome-positive detection** — Stop hook detects when injected mistake warnings successfully prevented repeats (domain-filtered to avoid false positives)
- **Session metrics** — WAL events for error_count, tool_calls, duration per session
- **Clean session tracking** — sessions with zero errors logged with domain context
- **Bayesian effectiveness scoring** — replaces binary negative_ratio with `(positive+1)/(positive+negative+2)` Laplace smoothing; records with no outcome data score 0.5 (neutral) instead of 1.0 (optimistic)
- **Batch analytics** (`memory-analytics.sh`) — weekly WAL analysis generating `.analytics-report` with winners, noise candidates, strategy gaps, and unproven records
- **Noise penalty** — records flagged as noise in analytics report get -3 score penalty in session-start injection
- **Auto-trigger** — Stop hook launches analytics rebuild in background when report is stale (>7 days)

### Positive patterns

- **Strategy-used tracking** — clean session + injected strategy = confirmed usage event in WAL
- **Strategy-gap detection** — clean session without strategies = signal for analytics (domain-level gap tracking)
- **Strategy bonus** — strategies with confirmed usage get up to +6 score bonus (`min(used_count*2, 6)`)
- **Strategies reminders** — PreCompact and Stop hooks prompt to record successful approaches on long clean sessions (>10 min)
- **Structured strategy template** — trigger, steps, outcome format

### Improvements

- **WAL compaction v2** — preserves outcome/strategy events during compaction; aggregates old session-metrics to monthly summaries (`metrics-agg`)
- **collect_mistakes subshell fix** — INJECTED_FILES array was lost in subshell; refactored to use result variable pattern (bug found by integration test)

### Testing

- 116 smoke tests (+35 new: outcome-positive, session-metrics, clean-session, Bayesian scoring, strategy bonus, strategy-used/gap, analytics, noise penalty, full pipeline integration)
- 15 benchmark scenarios — all passing, zero regressions

## [0.1.0] - 2026-04-11

First release. Persistent file-based memory system for Claude Code with composite scoring, lifecycle management, and feedback loops.

### Core

- **SessionStart hook** — single-pass awk parsing, composite scoring (keywords + TF-IDF + WAL spaced repetition), domain filtering, adaptive quotas, project detection via longest-prefix match
- **Stop hook** — lifecycle rotation with type-specific decay rates, auto-generated continuity from git state, background TF-IDF rebuild
- **Outcome tracking** (PostToolUse) — detects when injected mistake warnings fail to prevent repeats, feeds negative outcomes back into WAL scoring

### Memory types

- **mistakes** — bugs and anti-patterns with recurrence, severity, root-cause, prevention (3 global + 3 project quota)
- **strategies** — proven approaches with trigger and success count (cap 4)
- **feedback** — behavioral rules with why/how structure (cap 6)
- **knowledge** — domain facts, API docs, infrastructure (cap 4)
- **decisions** — architecture and technology choices with alternatives and reasoning (cap 3)
- **notes** — long-form knowledge, references, URLs (cap 2)
- **projects** — project descriptions and stack (1 per project, never archived)
- **continuity** — session handoff state (auto-generated from git)

### New in this release

- **Error pattern detection** — PostToolUse hook on Bash catches warnings and deprecation notices on successful commands via configurable `.error-patterns`, with session dedup and cross-reference to existing mistakes
- **Blocking fuzzy dedup** — PreToolUse hook blocks creation of duplicate mistakes using rapidfuzz trigram similarity (via `uv run`); similarity >= 80% blocks write, >= 50% warns
- **PreCompact checkpoint** — reminds Claude to save unsaved insights before context window compression; stronger warning if nothing was saved in session
- **Cold start domain fallback** — when git keyword signal is weak (< 3 keywords), project domains from `projects-domains.json` are used as pseudo-keywords for baseline scoring

### Testing

- 81 smoke tests covering injection, domain filtering, WAL, TF-IDF, outcome tracking, error detection, fuzzy dedup, decisions, precompact, cold start
- 15 benchmark scenarios for precision, domain filtering, priority ranking, edge cases
