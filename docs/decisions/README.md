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

1. [file-based-memory](file-based-memory.md) — on-disk markdown + YAML as the single source of truth; no database, no embeddings.
2. [wal-four-column-invariant](wal-four-column-invariant.md) — four pipe-delimited columns are immutable within format v1.
3. [silent-fail-policy](silent-fail-policy.md) — every hook exits 0 on internal failure; only PreToolUse/dedup is allowed to return 2.
4. [two-scoring-pipelines](two-scoring-pipelines.md) — SessionStart uses a composite score; UserPromptSubmit uses a lexicographic priority key.
5. [bash-go-gradual-port](bash-go-gradual-port.md) — bash is the reference implementation; Go hot paths are opt-in with a byte-for-byte parity gate.
6. [fts5-shadow-retrieval](fts5-shadow-retrieval.md) — FTS5 runs alongside substring triggers as an observational recall signal, not as an injection source.
7. [substring-triggers-with-negation](substring-triggers-with-negation.md) — reactive injection matches substrings with a ±40-character negation window.
8. [precision-class-ambient](precision-class-ambient.md) — rules that shape behaviour without being cited are excluded from the measurable-precision denominator.
