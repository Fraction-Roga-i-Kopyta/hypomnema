# Architecture decisions

Project-level architecture decisions, written in the compact format
defined by `templates/decision.md` — outcome, forcing functions, trade-
off accepted, and the observable signal that would re-open the question.

These files are the public counterparts of the `decisions/` memory type
described in `CLAUDE.md`: individual agents may maintain their own
decision records in `~/.claude/memory/decisions/`, but anything that
constrains what goes into the repository itself lives here.

Conventions:

- Filename is the slug the rest of the repo (CHANGELOG, CONFIGURATION,
  FORMAT, ARCHITECTURE) references. Renaming a decision breaks those
  references — amend in place with a follow-up "Status" note instead.
- `description:` in frontmatter is the one-line outcome, same
  convention as any other hypomnema memory record.
- `review-by:` is advisory, not a deadline. Empty means "until the
  observable signal in § When to revisit fires".

## Index

### Active (v2)

1. [file-based-memory](file-based-memory.md) — on-disk markdown + YAML as the single source of truth; no database, no embeddings.
2. [native-primary-sidecar](native-primary-sidecar.md) — Claude Code native memory is the canonical content store; hypomnema is a governance + ranking layer over it; metadata lives in a rebuildable SQLite sidecar; a hypomnema-owned global store fills native's per-project-only gap. **Defines the v2 substrate.**
3. [go-primary-bash-shims](go-primary-bash-shims.md) — Go (`memoryctl`) is the primary implementation; bash is reduced to ~10-line exec shims. Flips `bash-go-gradual-port`. **Defines the v2 implementation split.**
4. [wal-four-column-invariant](wal-four-column-invariant.md) — four pipe-delimited columns (`date|event|target|session`) are immutable within the WAL format.
5. [silent-fail-policy](silent-fail-policy.md) — every hook exits 0 on internal failure; only the PreToolUse write guard is allowed to return 2.
6. [precision-class-ambient](precision-class-ambient.md) — rules that shape behaviour without being cited are excluded from the measurable-precision denominator.
7. [intuition-milestone](intuition-milestone.md) — when `silent_applied / trigger_useful > 1.0` sustained over a 30-day window, the system has crossed the intuition tier; observational milestone, not a release.
8. [wal-header-v2-marker](wal-header-v2-marker.md) — optional first-line `# hypomnema-wal v2` marker; readers MUST tolerate absence, MAY warn on absence.

### Superseded (v1 mechanics retired in v2.0)

These record historical decisions whose mechanics no longer exist in v2.
Bodies are preserved as an archive; each carries a superseded banner and a
`superseded-by:` pointer in its frontmatter.

9. [two-scoring-pipelines](two-scoring-pipelines.md) — two ranking algorithms (SessionStart composite + UserPromptSubmit priority key). → merged into one relevance ranker (`internal/rank`); see `go-primary-bash-shims`.
10. [bash-go-gradual-port](bash-go-gradual-port.md) — bash as reference implementation with an opt-in Go parity gate. → reversed by `go-primary-bash-shims`; the parity harness (`scripts/parity-check.sh`, `make parity`) is gone.
11. [substring-triggers-with-negation](substring-triggers-with-negation.md) — reactive injection via substring match with a ±40-character negation window. → replaced by keyword-overlap ranking; `triggers:` and negation windows removed.
12. [fts5-shadow-retrieval](fts5-shadow-retrieval.md) — FTS5 + BM25 shadow recall signal alongside substring triggers. → shadow pass removed; the unified ranker makes it unnecessary.
13. [quota-pool](quota-pool.md) — adaptive 12/10/8 per-type injection slot pool plus a 3+3 mistake cap. → replaced by a single relevance-ranked top-8 with no per-type slots.
14. [cold-start-scoring](cold-start-scoring.md) — Bayesian/TF-IDF components stay dormant until the corpus accumulates signal. → cold-start gates removed; ranking validated directly by `memoryctl ab`.
15. [scoring-weights](scoring-weights.md) — fixed weights for the five SessionStart components (incl. TF-IDF ×2). → replaced by the v2 additive blend (overlap / ref_count / recency / effectiveness).
