# Project invariants

Live catalog of contract-level invariants that **every implementation
in this project must uphold**. The list is short by design: only
load-bearing rules that crossing would corrupt data or violate
documented contracts.

This file is the single place to consult before adding a new writer,
changing an on-disk contract, or shipping a new hook. If you find a place
in the codebase that violates an invariant listed here, that's a bug —
open an issue or fix in the same PR that introduced the violation.

**Automation status (v2).** Three invariants are mechanized and gated in
CI (`.github/workflows/test.yml`); the rest are human-judgment at review
time:

| Invariant | Enforcement |
|---|---|
| **I1** (WAL grammar) | `memoryctl wal validate` on `fixtures/wal-clean` — CI step "WAL grammar gate on fixture". |
| **I3** (atomic writes) | Go checker `atomicWriteChecker` in `internal/invariants`, run by `memoryctl audit invariants` — CI step "Invariants audit". |
| **I5** (hook fail-safe) | Go checker `hookFailSafeChecker` in `internal/invariants`, same CI step. |
| **I2, I4, I6, I7** | Human-judgment (no checker). |

I7 (cross-implementation parity) is largely moot in v2: `memoryctl` (Go)
is the single implementation and bash is reduced to exec shims, so there
is no dual bash/Go code path left to keep in parity.

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

**Why.** Every reader (`internal/wal/validate`, `internal/inject`,
`internal/profile`, `internal/closer`, `internal/doctor`,
`internal/migrate`) keys off `strings.Split(line, "|")` with len=4. A row
with the wrong column count silently corrupts the metric pipelines. See
ADR [`wal-four-column-invariant`](decisions/wal-four-column-invariant.md).

**Where to enforce.** Every WAL writer:

- `internal/wal.Append` / `internal/wal.AppendStrict` (Go) — the sole
  writer in v2. Bash shims call the Go binary; there is no bash WAL
  writer (`hooks/lib/wal-lock.sh` no longer exists).
- Any future writer added to the Go tree.

**How to check.** Mechanized. `memoryctl wal validate` runs the full
check against a `.wal`, and CI runs it on `fixtures/wal-clean` on every
push/PR (CI step "WAL grammar gate on fixture") — a malformed fixture row
fails the build. Historical WAL rows that violate (e.g. `metrics-agg` with
3 columns from v0.x) are tolerated by readers (skip on
`len(fields) < 2`); they are not fixed retroactively.

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
  Every Go caller must use it (not raw `filepath.Base + TrimSuffix`)
  before a slug reaches the WAL `target` column. In v2 this is the only
  writer path — the v1 bash writers and the `bin/memory-fts-shadow.sh`
  shadow reader are gone.

**How to check.** Human-judgment (not a Go checker). The old parity
fixture `edge-slug-pipe` (`scripts/parity-check.sh`) is gone with the
bash reference path; verify at review time that every new Go path building
a WAL `target` routes through `pathutil.SlugFromPath`. Its hostile-input
behaviour is pinned by unit tests in `internal/pathutil`
(`TestSlugFromPath_DropsControlChars`).

---

## I3 — Atomic writes for multi-line files

**What.** Every writer that emits a multi-line file in the
maintainer's memory dir or its derivatives MUST use the
`os.CreateTemp` + `os.Rename` (Go) or `mktemp` + `mv` (bash)
idiom. Direct `os.WriteFile` / `>` redirect leaves a window where
a concurrent reader sees a half-written file or a crash leaves a
truncated file on disk.

**Why.** Multi-line files (`self-profile.md`, `MEMORY.md`) are read by
other hooks and by Claude Code itself. Crash-mid-write corruption is
recoverable but observably bad; tmp+rename costs ~zero and removes
the window.

**Where to enforce.** Every Go writer in `internal/` (and `cmd/`) that
emits a multi-line file. Canonical helper:
`internal/pathutil.WriteFileAtomic` (same-dir temp + rename). Currently:

- `internal/profile/profile.go::atomicWriteSelfProfile` — self-profile.md
- Any `pathutil.WriteFileAtomic` caller.

The `internal/tfidf` and `internal/evidence` packages named by earlier
drafts no longer exist (TF-IDF indexing and evidence-frontmatter mutation
were retired in v2). The single approved bare `os.WriteFile` site is
`internal/wal/lock.go` (single-line ephemeral lock token), whitelisted in
the checker's `approvedWriteFileSites`.

**How to check.** Mechanized. `memoryctl audit invariants` runs
`atomicWriteChecker` (`internal/invariants`), which walks `internal/` and
`cmd/` for `os.WriteFile(` calls in non-test production code and flags any
not listed in `approvedWriteFileSites`. It is a blocking CI step
("Invariants audit"). Add a new bare-`os.WriteFile` site only by
whitelisting it there with a one-line justification, or refactor to
`pathutil.WriteFileAtomic`.

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

**How to check.** Human-judgment — this is **not** one of the mechanized
checkers (`invariants.All()` covers only I3 and I5). Manual review at PR
time. Heuristic: any `func` taking an arg ending in `Path` whose body
calls `os.ReadFile` / `os.WriteFile` / `filepath.Walk` without first
calling `filepath.Abs` + prefix check is a candidate.

---

## I5 — Fail-safe on hook errors

**What.** Every hook (any `hooks/**/*.sh` shim or `internal/` function
called by a hook) MUST exit 0 on internal failure. The single exception is
the PreToolUse write guard (`hooks/v2/pre-tool-write.sh` →
`memoryctl guard`), which is explicitly allowed to return 2 to block the
offending tool call.

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

**How to check.** Mechanized. `memoryctl audit invariants` runs
`hookFailSafeChecker` (`internal/invariants`), which scans `hooks/**/*.sh`
for `set -e` / `set -u` (any dash-flag block containing `e` or `u`) and
flags production hooks — `set -o pipefail` alone is the allowed form. The
only whitelisted fail-fast script is the test runner
`hooks/v2/shims_test.sh` (in `approvedFailFastHooks`). Blocking CI step
("Invariants audit").

---

## I6 — `pinned` does not reserve an injection slot

**What.** `status: pinned` protects a memory file from decay (it never
goes `stale`). It does NOT reserve an injection slot. Pinned records
compete in the same single relevance-ranked top-8 as `status: active`
records; ordering is by score, not status priority.

**Why.** Pinned-as-injection-guarantee is a tempting interpretation but
would break pure-by-score ranking. v2 injects one ranked top-8 across all
types with no per-type quotas — the adaptive 12/10/8 slot pool was retired
(see superseded ADR [`quota-pool`](decisions/quota-pool.md)). Pinned buys
decay immunity, not a reserved seat.

**Where to enforce.**

- `internal/rank` / `internal/inject` ranking pipeline. Status gates
  eligibility (`stale` is excluded) but does not reserve a slot.
- `internal/doctor` and any analysis tool — if it reports «pinned X not
  injected this session», that is **not a bug**, just a ranking outcome
  (the fact fell below the top-8).

**How to check.** Human-judgment. If a future contributor proposes to
change pinned semantics (e.g. a reserved slot), that proposal needs a
follow-up ADR, not a one-line frontmatter tweak.

---

## I7 — Cross-implementation parity for shared contracts

> **Largely moot in v2.** This invariant governed the v1 bash↔Go dual
> implementation. v2 is Go-primary (see ADR
> [`go-primary-bash-shims`](decisions/go-primary-bash-shims.md)): bash is
> reduced to ~10-line exec shims with no duplicated logic, so there is no
> second implementation to keep in parity. Every artefact it named —
> `scripts/parity-check.sh`, `make parity`, `bin/memory-fts-shadow.sh`,
> `internal/fts`, `internal/tfidf` — has been removed. Retained here as a
> standing rule: **if** a behaviour is ever duplicated across languages
> again, it MUST get a parity test with at least one adversarial fixture.

**Why (historical).** External review 2026-05-08 finding E4: a parity
matrix of "4 cases, all happy path" gave a false sense of coverage. The
moment the matrix was expanded to include hostile inputs (PR #17,
v1.1.0), real drift in the bash shadow reader surfaced immediately.
Parity tests without adversarial fixtures are weaker than no parity
tests, because they document a guarantee that isn't enforced.

**How to check.** N/A in v2 (single Go implementation). Human-judgment if
a future change reintroduces a duplicated cross-language contract.

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
