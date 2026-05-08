# Project invariants

Live catalog of contract-level invariants that **every implementation
in this project must uphold**. The list is short by design: only
load-bearing rules that crossing would corrupt data, break parity,
or violate documented contracts.

This file is the single place to consult before adding a new writer,
porting a function across the bash↔Go boundary, or shipping a new
hook. If you find a place in the codebase that violates an invariant
listed here, that's a bug — open an issue or fix in the same PR that
introduced the violation.

**This is not a guide.** Tutorials live in `docs/QUICKSTART.md`, deep
designs in `docs/decisions/`, narrative changes in `CHANGELOG.md`.
This file is a **rule book** — terse, dense, scannable.

## Reading the catalog

Each invariant has:

- **What** — one-line normative statement.
- **Why** — load-bearing constraint (data integrity, parity,
  contract).
- **Where to enforce** — code locations that must apply the rule;
  reviewer should check these before merge.
- **How to check** — automated check (if exists), grep pattern, or
  human-judgment criterion.

Invariants are NOT versioned. Adding one requires PR + ADR (if it
introduces a new constraint surface). Removing one requires PR +
explanation in CHANGELOG.

---

## I1 — WAL grammar: four pipe-delimited columns

**What.** Every line in `~/.claude/memory/.wal` has exactly four
pipe-delimited columns: `date|event|target|session`. The shape is
immutable within format v2; a fifth column requires a major version.

**Why.** Every reader (bash awk, Go `internal/profile`,
`internal/decisions/snapshot`) keys off `awk -F'|'` with NF=4 or
`strings.Split(line, "|")` with len=4. A row with the wrong column
count silently corrupts the metric pipelines. See ADR
[`wal-four-column-invariant`](decisions/wal-four-column-invariant.md).

**Where to enforce.** Every WAL writer:

- `internal/wal.Append` (Go)
- `hooks/lib/wal-lock.sh::wal_append` (bash)
- Any future writer added to either tree.

**How to check.** `memoryctl wal validate` runs the full check
against the current `.wal`. CI gate planned in v1.2; until then,
maintainer-side manual run. Historical WAL rows that violate (e.g.
`metrics-agg` with 3 columns from v0.x) are tolerated by readers
(skip on `len(fields) < 2`); they are not fixed retroactively.

---

## I2 — Slug sanitisation in WAL writes

**What.** Slugs written to the `target` column (`$3`) MUST have
`|`, `\n`, `\r` replaced with `_` before write. This applies
symmetrically to bash and Go, both writer paths and shadow paths.

**Why.** A hostile filename (`foo|outcome-positive|bar.md`) would
inject extra columns into the WAL and fake events for unrelated
slugs. Bash side enforced this since audit-2026-04-16 (marker S6);
Go side gained `pathutil.SlugFromPath` sanitisation in v1.0.1 (E1);
bash shadow reader gained it in v1.1.0 (#17 — was the regression
that the parity matrix expansion exposed).

**Where to enforce.**

- Go: `internal/pathutil.SlugFromPath` is the canonical sanitiser.
  Every Go caller must use it (not raw `filepath.Base + TrimSuffix`).
- Bash writers: `wal_append` callers should pass slugs already
  sanitised by `basename` + explicit `tr '|\n\r' '_'` if there is
  any chance the source basename came from filesystem (LLM-author
  filenames are the realistic threat).
- Bash shadow: `bin/memory-fts-shadow.sh` `tr '|\n\r' '_'` on the
  derived `_slug`.

**How to check.** Parity-check fixture `edge-slug-pipe`
(`scripts/parity-check.sh`) exercises both sides with a hostile
basename. Adding new writers without this check is a bug — extend
the parity fixture to cover them.

---

## I3 — Atomic writes for multi-line files

**What.** Every writer that emits a multi-line file in the
maintainer's memory dir or its derivatives MUST use the
`os.CreateTemp` + `os.Rename` (Go) or `mktemp` + `mv` (bash)
idiom. Direct `os.WriteFile` / `>` redirect leaves a window where
a concurrent reader sees a half-written file or a crash leaves a
truncated file on disk.

**Why.** Multi-line files (`self-profile.md`, `.tfidf-index`,
`evidence` frontmatter mutations, `MEMORY.md`) are read by other
hooks and by Claude Code itself. Crash-mid-write corruption is
recoverable but observably bad; tmp+rename costs ~zero and removes
the window.

**Where to enforce.** Every Go writer in `internal/` that writes a
file with > 1 line. Currently:

- `internal/profile/profile.go::atomicWriteSelfProfile` — self-profile.md
- `internal/tfidf/tfidf.go::Rebuild` — .tfidf-index
- `internal/evidence/evidence.go::AppendToFrontmatter` — frontmatter mutations

Bash writers via `perl -i` are atomic-on-rename by perl semantics;
no fsync but acceptable per ADR (no incident in 18 months).

**How to check.** Grep audit: `grep -nE 'os\.WriteFile' internal/`
should match only test code or single-line writers. Any new match
in `internal/` outside `_test.go` requires justification or refactor
to `os.CreateTemp` + `Rename`. Planned automation: `memoryctl audit
invariants` (v1.x) will mechanise this grep.

---

## I4 — Boundary check on exported `targetPath`-style args

**What.** Every exported Go function (or top-level bash function
callable from outside its source file) that accepts a path argument
which will be **read or written** must check that the path lives
inside the expected root before any I/O. Failure mode: silent no-op
for hook-style "must never break the caller" contracts; explicit
error for CLI / explicit-call APIs.

**Why.** External review 2026-05-08 finding E3: `dedup.Run` accepted
arbitrary `targetPath`, read it, on similarity match deleted it.
A rogue caller (or a malformed hook) could feed it a path on disk
and dedup would silently consume it. Boundary check
(`strings.HasPrefix(absTarget, absMem+sep)`) collapses the attack
surface.

**Where to enforce.**

- `internal/dedup.Run` — `targetPath` ⊂ `MemoryDir`. Done in v1.0.1.
- Future exported functions taking path args: same pattern.
- Bash hooks calling the Go binary should not need this check
  themselves; the binary enforces it. But hook-internal `case`
  statements that match path prefixes should also use a quoted
  pattern that won't match outside `$CLAUDE_MEMORY_DIR`.

**How to check.** Manual review at PR time (no automation yet).
Heuristic: any `func` taking an arg ending in `Path` whose body
calls `os.ReadFile` / `os.WriteFile` / `filepath.Walk` without first
calling `filepath.Abs` + prefix check is a candidate. Planned for
inclusion in `memoryctl audit invariants`.

---

## I5 — Fail-safe on hook errors

**What.** Every hook (anything `~/.claude/hooks/*.sh` or
`internal/` function called by a hook) MUST exit 0 on internal
failure. The single exception is `memory-secrets-detect.sh` and
`memory-dedup.sh` PreToolUse paths, which are explicitly allowed to
return 2 to block the offending tool call.

**Why.** Hook-broken should never break a Claude Code session.
Observability shifts to WAL events + `memoryctl doctor`. See ADR
[`silent-fail-policy`](decisions/silent-fail-policy.md).

**Where to enforce.**

- Every hook entry point: `set -o pipefail` (NOT `-eu`!) at the
  top, internal failures end with `exit 0`.
- Go internal funcs called by `cmd/memoryctl` from hooks: the CLI
  wrapper must catch errors and exit 0 (`runSelfProfile` does this:
  the CLI prints the error to stderr but exits 1, and the calling
  hook `2>/dev/null` swallows it).

**How to check.** `grep -lE '^set -[eu]' hooks/` should return only
test files (`hooks/test-memory-hooks.sh`) and one-shot scripts
(`hooks/memory-fts-sync.sh`). Production hooks use `pipefail` only.

---

## I6 — `pinned` does not bypass scoring quotas

**What.** `status: pinned` protects a memory file from rotation
(stale → archive). It does NOT reserve an injection slot. Pinned
records compete in the same flex-pool as `status: active` records;
ranking is by score, not status priority.

**Why.** Documented in `FORMAT.md § 3.1`. Pinned-as-injection-guarantee
was a tempting interpretation but would break the adaptive 12/10/8-
slot quota that responds to keyword signal strength. The flex pool
must remain pure-by-score.

**Where to enforce.**

- `hooks/session-start.sh` ranking pipeline. Status is one bucket of
  the priority key, not a hard reservation.
- `internal/profile` and any analysis tool — if it reports «pinned X
  not injected this session», that is **not a bug**, just a
  quota outcome.

**How to check.** Read `FORMAT.md § 3.1` literally. If a future
contributor proposes to change pinned semantics, that proposal needs
a follow-up ADR, not a one-line frontmatter tweak.

---

## I7 — Cross-implementation parity for shared contracts

**What.** Any function or behaviour duplicated across bash and Go
(WAL writing, shadow retrieval, self-profile generation, TF-IDF
indexing) MUST have a parity test in `scripts/parity-check.sh` that
compares both implementations on identical fixtures, and the matrix
MUST include at least one **adversarial input** per contract.

**Why.** External review 2026-05-08 finding E4: a parity matrix of
"4 cases, all happy path" gave a false sense of coverage. The
moment we expanded the matrix to include hostile inputs (PR #17,
v1.1.0), real drift in `bin/memory-fts-shadow.sh` surfaced
immediately. Parity tests without adversarial fixtures are weaker
than no parity tests, because they document a guarantee that isn't
enforced.

**Where to enforce.** `scripts/parity-check.sh` per contract:

- WAL shadow events (bash `bin/memory-fts-shadow.sh` ↔ Go `internal/fts`)
- Self-profile output (bash `bin/memory-self-profile.sh` ↔ Go `internal/profile`)
- TF-IDF index output (bash `hooks/memory-index.sh` ↔ Go `internal/tfidf`)
- Future ports added under `make parity`.

**How to check.** `make parity` exit 0 on every PR (CI gate).
README's claim of "byte-for-byte parity" is honest only if the
matrix exercises both happy and adversarial inputs. New contracts
ported across implementations require a new `_run_*_case` function
+ at least 2 fixtures (happy + adversarial).

---

## When this file changes

Adding I8+:

1. Open a PR with the new entry plus the code that makes it
   enforceable (test, validator rule, code change).
2. Cross-link to related ADR if the invariant is downstream of an
   architectural decision.
3. Add a one-line CHANGELOG entry referencing the invariant ID.

Removing an invariant requires CHANGELOG entry under "Removed" with
an explanation of why the constraint no longer applies. Don't remove
invariants because they feel inconvenient; remove them because the
underlying need has changed.
