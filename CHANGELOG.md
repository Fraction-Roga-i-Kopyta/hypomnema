# Changelog

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

- **`hooks/lib/detect-project.sh`** — shared `detect_project()` helper. Eliminates 12-line duplicate block in `session-start.sh:60-71` and `session-stop.sh:191-202`.
- **`hooks/lib/stat-helpers.sh`** — portable `_stat_mtime`/`_stat_size`. Replaces 5 inline `stat -f %m vs stat -c %Y` blocks across `session-start.sh` and `session-stop.sh`. One typo on the wrong OS would have silently disabled lifecycle rotation.

### Deferred to v0.9

- **Decomposition of `session-start.sh` (1288 lines)** into `hooks/lib/{parse-memory,score-records,build-context}.sh` modules. Risky refactor of a monolith with ~50 globals and intertwined AWK heredocs; deserves a dedicated session and plan.
- **Multi-word `domains:` frontmatter values** — currently split on space after YAML parse. Use kebab-case (`machine-learning`) as workaround. Fix requires deeper YAML parsing.

### Tests

183 → 200 (17 new assertions: Test 21 perl injection, Test 22 WAL lock assertion, Test 23 shell-safety meta, Test 24a/b/c Cyrillic + mixed + ASCII regression for evidence_from_body, Test 25 concurrent sessions). ALL TESTS PASSED.

### Architectural notes

Four-agent code review scored: architecture 6.5/10, functionality 8.5/10, bugs 6.5/10, onboarding 5.5/10. v0.8 closes critical bug + onboarding gaps; architecture refactor lands in v0.9. See `docs/plans/2026-04-15-v0.8-cleanup-and-onboarding.md` for full task breakdown.

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
