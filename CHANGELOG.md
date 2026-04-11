# Changelog

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
