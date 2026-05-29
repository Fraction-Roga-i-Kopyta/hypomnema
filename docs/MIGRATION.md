# Migration Guide

## v0.7 → v0.8

### Required actions: none

v0.8 is backward compatible. Existing memory files continue to work without edits.

### What changed

1. **Pre-flight checks in install.sh.** Missing `jq`/`perl`/`awk`/`bash 3.2+` now fails fast with a clear message instead of silently corrupting `settings.json`.

2. **Cyrillic / non-ASCII bodies fully tokenized.** `evidence_from_body` rewritten in perl with UTF-8 awareness (`\w{4,}` under `/u`). If you previously added explicit `evidence:` frontmatter to non-ASCII files as a workaround, you can keep it (still respected) or remove it — auto-extraction now works.

3. **Perl regex injection fix.** Section markers in injected context are now safe against memory filenames containing regex metacharacters (e.g. `foo)bar.md`). `_perl_re_escape` helper added.

4. **WAL read locking.** Feedback-loop extraction in the Stop hook now acquires the WAL lock before reading. No user action needed; eliminates undercount of `inject` events when multiple sessions run concurrently.

5. **`set -o pipefail` everywhere.** All executable hooks/bin scripts now have it. Test 23 enforces this at meta level.

6. **Portable stat helpers.** `lib/stat-helpers.sh` provides `_stat_mtime` and `_stat_size` — replaces five inline `stat -f %m vs stat -c %Y` blocks across `session-start.sh` and `session-stop.sh`.

7. **Repo-root `CLAUDE.md`.** New agents entering the repo learn the memory protocol on first read.

8. **Docs:** `docs/QUICKSTART.md`, `docs/TROUBLESHOOTING.md`, `docs/FAQ.md`, `docs/MIGRATION.md`.

9. **Interactive install wizard.** `./install.sh --discover` scans dev folders for git repos and proposes `projects.json` entries. `./install.sh --patch-claude-md` adds the four-line memory section to your global CLAUDE.md. `--dry-run` for preview.

10. **Externalized constants.** `~/.claude/memory/.config.sh` overrides defaults (`MAX_FILES`, decay thresholds, etc.). `templates/.config.sh.example` provided.

11. **Architecture refactor.** `hooks/session-start.sh` decomposed into `hooks/lib/{parse-memory,score-records,build-context}.sh` modules. If you forked any hook, rebase against the new structure — most edits move into the relevant lib module.

### Optional cleanup

- Delete legacy explicit `evidence:` blocks added as Cyrillic workaround (now redundant — but harmless if kept).
- If you customized any hook, re-apply your patch on top of the new lib/ structure (see `git log -p` for the v0.8 refactor commits).

### Rollback

```bash
cd /path/to/hypomnema
git checkout v0.7.1
./install.sh
```

Memory files are unaffected; only the hooks revert.

### Known non-issue: multi-word domains

`domains:` in frontmatter is space-separated after YAML parsing. Multi-word values like `"machine learning"` get split into `"machine` + `learning"`. Use kebab-case (`machine-learning`) instead — this is the existing convention across all seeds and templates.

## v0.8 → v0.9

### Required actions: none

v0.9 is backward compatible. Existing memory files, frontmatter, and WAL continue to work without edits. All additions are opt-in.

### What changed

1. **`bin/memoryctl` Go binary (opt-in).** First Go component in a planned hybrid bash+Go architecture. Drops in for the FTS5 shadow pass (`memoryctl fts shadow`), sync (`memoryctl fts sync`), and query (`memoryctl fts query`). Pure-Go SQLite via `modernc.org/sqlite` — CGO disabled, no cross-compile pain. Bash scripts stay authoritative; if `make build` isn't run, everything works as before.
   ```bash
   brew install go                     # or: apt install golang
   cd /path/to/hypomnema && git pull
   make build && ./install.sh          # installs ~/.claude/bin/memoryctl
   ```
   Skipping this leaves the bash implementations in place; nothing breaks.

2. **Formal on-disk contracts.** `docs/FORMAT.md` (frontmatter schema, WAL grammar with 19 event types, derivative indices, parsing tolerances) and `docs/hooks-contract.md` (per-hook stdin/stdout JSON, exit codes, env vars, fail-safe rules) both declare `STATUS: STABLE` at v1.0. If you wrote tooling that reads `~/.claude/memory/` or emitted WAL events, verify against these docs. The bash implementation's existing behaviour is what the contract documents — no code changes, just the contract becoming explicit.

3. **`uninstall.sh`.** Surgical reverse of `install.sh` — removes only our symlinks, strips only our entries from `settings.json` (preserves foreign hooks), keeps `~/.claude/memory/` by default. Pass `--purge-memory --yes` for complete removal including the memory tree.

4. **Matrix CI + clean-VM bootstrap.** Adds `ubuntu-latest` to the test matrix (previously macOS-only) and `docs/maintenance/RELEASE_CHECKLIST.md` making clean-VM install + test a REQUIRED step before `git tag`. Caught five BSD-vs-GNU portability bugs that had shipped from the macOS-only dev loop — notably `sed 1,/pat/` range semantics, `stat -f` vs `stat -c`, BSD `grep "\t"` literal interpretation, `en_US.UTF-8` not guaranteed on Linux runners.

5. **`HYPOMNEMA_TODAY` environment override.** Production hooks now consult this var for their "today" date stamp when set. Used by the test suite to freeze dates so fixtures with hardcoded WAL entries don't drift out of the 30-day scoring window over time. No runtime impact; only matters if you write tests.

6. **`install.sh` symlink enumeration.** Replaced 18 hand-rolled `ln -sf` lines with directory iteration over `hooks/*.sh` and `bin/*`. Previously missing symlinks for `memory-error-detect.sh`, `memory-precompact.sh`, and `memory-dedup.py` are now installed. **If you upgrade to v0.9 without re-running `./install.sh`, these three hooks won't be wired up** — the symlinks are the only activation path.

### Optional actions

- **Run `make build && ./install.sh`** if you want Go-accelerated FTS shadow/sync/query. The bash fallback is equivalent in output but ~5× slower on large corpora.
- **Re-run `./install.sh`** on any existing install to pick up the three previously-missing symlinks (`memory-error-detect.sh`, `memory-precompact.sh`, `memory-dedup.py` — the last one is further addressed in v0.10.0, see below).

### Rollback

```bash
cd /path/to/hypomnema
git checkout v0.8.2
./install.sh
```

Memory files and WAL are unaffected.

## v0.9 → v0.10

### Required actions: **yes** if you had fuzzy dedup working

v0.10.0 is a stack-consolidation release: the Python layer is **removed entirely**. If you installed `uv` + `rapidfuzz` per v0.9 docs and relied on PreToolUse dedup blocking duplicate mistakes, the Python path is gone and you must switch to the Go binary.

```bash
cd /path/to/hypomnema && git pull
make build              # produces ./bin/memoryctl; requires Go 1.22+
./install.sh            # symlinks memoryctl into ~/.claude/bin
```

If `make build` fails because Go isn't installed, either install it (`brew install go` / `apt install golang`) or accept that fuzzy dedup stays off — hypomnema's other hooks continue working and the PreToolUse script exits 0 silently.

If you never installed `uv`/`rapidfuzz` in the first place, dedup was already disabled on your install; no action needed. You'll still benefit from the other v0.10 improvements by a plain `git pull`.

### Breaking changes

1. **`bin/memory-dedup.py` is deleted** along with `uv` + `rapidfuzz` as dependencies. The bash wrapper `hooks/memory-dedup.sh` is retained but now delegates to `memoryctl dedup check <file>` exclusively. Exit codes (0 allow, 1 merge, 2 block) and WAL event shapes (`dedup-blocked`, `dedup-merged`, `dedup-candidate`) are unchanged — this is a same-contract re-implementation, not a semantic change.

2. **Project now uses bash + Go, not bash + Go + Python.** `go.mod` requires Go 1.22+. There is no longer a Python path to fall back to.

3. **Stale symlink warning.** If you installed v0.9.x with `./install.sh`, you got a `~/.claude/bin/memory-dedup.py` symlink pointing into the repo. After `git pull` to v0.10.0+, that target file disappears and the symlink dangles. v0.10.2's `install.sh` automatically sweeps such broken symlinks on re-run; if you're going straight from v0.9 to v0.10.0 or v0.10.1, delete it by hand:
   ```bash
   rm -f ~/.claude/bin/memory-dedup.py
   ```
   `memoryctl doctor` (v0.10.3+) flags this explicitly.

### Added

1. **`memoryctl dedup check`** subcommand — same inputs, exits, and WAL events as the old Python implementation. `internal/fuzzy` package provides a pure-Go port of `rapidfuzz.token_set_ratio`; thresholded decisions (≥80 block, 50–80 candidate, <50 allow) are identical up to numerical drift, which test fixtures pin.

2. **`scripts/replay-runner.sh` + `make replay`** — synthetic-corpus retrieval measurement. 48 curated prompts across seven domains, batch-replayed through `UserPromptSubmit`, producing per-prompt TSV and aggregate metrics (`trigger-match hits %`, `shadow-miss events %`). Lets you measure retrieval quality without waiting N months for real sessions to accumulate. Operates on a seeded fixture by default; `--memory DIR` points it at your real memory tree for dogfooding.

### Rollback

```bash
cd /path/to/hypomnema
git checkout v0.9.1
./install.sh
```

You'll need to restore `uv` + `rapidfuzz` if you want dedup back on that path:
```bash
curl -LsSf https://astral.sh/uv/install.sh | sh   # if not still installed
uv venv && source .venv/bin/activate
uv pip install rapidfuzz
```
Memory files and WAL are untouched.

## v0.10.0 → v0.10.2 (patch releases)

### Required actions: none

Three patch releases in a single day, all backward-compatible bug fixes and documentation. Re-run `./install.sh` to pick them up, nothing else.

- **v0.10.1** — fixes for fresh-install distribution: three symlinks `uninstall.sh` didn't know about (`memory-error-detect.sh`, `memory-precompact.sh`, `memoryctl`), i18n leak in error detection (Russian → English), hardcoded author path in test suite (replaced with symlink-resolving header).
- **v0.10.2** — pinned-slot injection guard (pinned records no longer evicted by domain pressure at SessionStart), directory-driven `uninstall.sh` refactor, `memoryctl` PATH robust fallback across three scripts, `internal/dedup` WAL event ordering fix (the merged event was emitted before the on-disk mutation succeeded), `memory-analytics.sh` thresholds exposed via `.config.sh` with Laplace-smoothing commentary, seven smaller fixes.
- **v0.10.3 → main** (as of 2026-04-23) — `memoryctl doctor` subcommand (on-demand install health check), `install.sh` stale-symlink sweep (auto-cleans legacy `memory-dedup.py` on upgrade), dedup posttool WAL lock (close read-modify-write race), `format_version: >1` forward-compat gate in UserPromptSubmit, `install.sh` registration of PreToolUse / PostToolUse / PreCompact (three hooks that were never actually wired up in prior versions despite being symlinked), `LICENSE` file with real copyright.

If you're on v0.10.0 or v0.10.1 and see `install.sh` silently not registering some hooks, or a dangling `memory-dedup.py` symlink, upgrade to the current `main` — these issues are explicitly addressed.

## v1.x → v2.0

**MAJOR — breaking substrate change.** v2.0 repositions hypomnema as a
governance + ranking layer over Claude Code's native memory. The v1
self-managed `~/.claude/memory/` tree is no longer the primary store.
This migration is guided and reversible, not silent.

### Compatibility gate

Before upgrading, verify two prerequisites:

```bash
claude --version   # must be ≥ 2.1.59
```

Also confirm that auto-memory is not disabled (`autoMemoryEnabled` is
not `false` in Claude Code settings, and `CLAUDE_CODE_DISABLE_AUTO_MEMORY`
is not set in your environment).

**If either check fails:** stay on the v1.x tag — it remains fully
functional, receives no back-ports, but does not require native memory.

```bash
# Stay on v1 (no upgrade needed):
cd /path/to/hypomnema
git checkout v1.1.2
./install.sh
```

### Upgrade flow

**Step 1 — dry run: review the migration plan**

```bash
memoryctl migrate --dry-run
```

This prints a plan showing which files will be:
- **kept** — converted to native format, routed to project-native or
  global store.
- **pruned** — `ref_count == 0` and `status != pinned` and no outcome
  events and `created` older than 30 days. Pruning is the natural GC
  that a one-shot migration provides.
- **stale-archived** — already stale by type-threshold; moved to backup,
  not converted.

**If the plan lists a `WARNING: global-fallback` for any project:** it
means files tagged with that project slug have no corresponding entry
in `projects.json`, so they would route to the global store instead.
Add those projects to `projects.json` before proceeding to preserve
correct project-scoped routing.

**Step 2 — execute**

```bash
memoryctl migrate --execute
```

This:
1. Creates `~/.claude/memory.v1-backup-<YYYY-MM-DD>/` — a full copy
   of the v1 store. **Nothing is deleted from the original tree** until
   you explicitly remove the backup.
2. Converts and writes native-format files to
   `~/.claude/projects/<slug>/memory/` (project-scoped) and
   `~/.claude/memory-global/` (global).
3. Seeds the SQLite sidecar and WAL from your existing WAL — preserving
   ref_count, effectiveness history, and created dates.
4. Prints a summary: migrated / pruned / routed to global vs project.

For non-interactive environments, pass `--auto` to skip the confirmation
prompt.

**Step 3 — re-run install.sh**

```bash
./install.sh
```

This registers the four v2 shims (`session-start.sh`,
`user-prompt-submit.sh`, `pre-tool-write.sh`, `session-stop.sh`) in
`settings.json` and removes the v1 hook registrations. The edit is
surgical — only hypomnema-owned entries are changed; foreign hooks are
untouched.

**Step 4 — verify**

```bash
memoryctl doctor
```

Doctor confirms sidecar integrity, global store visibility, and that
injection returns results. A parity check (`≥70% recall overlap vs v1
behaviour on representative keywords`) is run automatically and printed
in the report.

### Rollback

If anything looks wrong after migration:

```bash
memoryctl migrate --rollback
```

This restores `~/.claude/memory.v1-backup-<date>/` to
`~/.claude/memory/`, re-registers the v1 hooks in `settings.json`, and
removes the v2 shim registrations. The native-format files written
during migration are left in place (they don't conflict with v1
operation) but the v1 hooks won't read them.

Alternatively, a clean rollback to a known state:

```bash
cd /path/to/hypomnema
git checkout v1.1.2
./install.sh
```

Memory files and WAL are unaffected by a hook-level rollback.

### What changed in v2.0

**Storage:** the v1 `~/.claude/memory/` nine-directory tree is replaced
by native memory dirs (`~/.claude/projects/<slug>/memory/`) for
project-scoped facts and `~/.claude/memory-global/` for global facts
(type: global). The native frontmatter schema is minimal
(name / description / type); the full hypomnema schema lives in the
SQLite sidecar.

**Metadata:** the SQLite sidecar (`~/.claude/memory/.hypomnema.db` in
v1, per-project in v2) is now the authoritative index for ref_count,
effectiveness, decay status, and keyword terms. It is derived and
rebuildable; the WAL remains the event source of truth.

**Hook implementation:** four bash hooks (session-start, user-prompt-submit,
pre-tool-write, session-stop) are now ~10-line exec shims. All logic
lives in the `memoryctl` Go binary. `make build` is a hard requirement;
pure-bash installs are no longer supported.

**Injection pipeline:** the two v1 pipelines (SessionStart composite
score and UserPromptSubmit substring triggers with negation) are merged
into one relevance ranker (`internal/rank`). Substring triggers and
negation windows are removed. Prompt tokens are treated as keyword
signals, not pattern-match gates.

**Dropped features:**
- TF-IDF body scoring (removed; Unicode tokeniser reused in ranker).
- FTS5 shadow retrieval (`index.db`, `memory-fts-shadow.sh`, `memoryctl fts`).
- Cold-start gates (Bayesian dormancy, TF-IDF vocab gate).
- Two-pipeline scoring (SessionStart composite + UserPromptSubmit priority key).
- Substring triggers + negation (`triggers:` / `trigger:` frontmatter fields).
- `decision-review-triggers` metric evaluation (feature deferred to a future release).
- `scripts/parity-check.sh` (no bash reference path to check against).

**Added:**
- Global store (`~/.claude/memory-global/`) for cross-project facts.
- `memoryctl ab` — A/B null-hypothesis harness (ranking beats random
  8–20×, n=49; see `docs/measurements/2026-05-29-v2-ranker-ab.md`).
- Guided migration with dry-run, backup, rollback, and parity
  verification.
- Degraded-ranker mode: sidecar absent or corrupt → inject continues
  with overlap + recency only (ref_count / effectiveness neutral);
  exit 0 always.

**Frontmatter compatibility:** `triggers:` / `trigger:` / `evidence:`
fields in existing memory files are ignored (not an error). The ranker
uses keyword overlap against name/description/body; those frontmatter
fields provide no lift and do not need to be removed.
