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
