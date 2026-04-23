# Changelog

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
