# Troubleshooting

## Start here: `memoryctl doctor`

`memoryctl doctor` is the primary v2 diagnostic. It is read-only, runs in a
second, and covers almost every failure below in one shot:

```bash
memoryctl doctor          # human-readable report
memoryctl doctor --json   # machine-readable, for scripts/CI
```

It checks:

- `claude_dir` / `memory_dir` exist
- every hypomnema hook is registered in `settings.json` **under the correct
  event** (not just present somewhere)
- all six v2 shims exist and are executable in `~/.claude/hooks/v2/`
- no broken symlinks in `~/.claude/hooks/` or `~/.claude/bin/`
- `memoryctl` is installed and on `$PATH`
- native corpus counts by type (empty corpus is flagged)
- WAL error events in the last 7 days
- open behavioural quanta (injects that never got a closing signal)
- sidecar freshness vs the WAL
- frontmatter quality (e.g. an empty `status:` line that silently drops a file)

Exit code is `0` unless a check is **FAIL** (`1`). `WARN` does not bump the
code. Read the report first — most sections below are just "what to do when
doctor points at a specific check."

## SessionStart injects nothing

1. **Run `memoryctl doctor`.** `settings_hooks_registered` and
   `shim_files_present` FAIL tell you the hooks aren't wired — re-run
   `./install.sh` and restart Claude Code.
2. **Check there is anything to inject.** v2 stores are **flat** (the kind is
   the `type:` frontmatter field, not a subdirectory):

   ```bash
   ls ~/.claude/memory-global/                                   # global facts
   ls ~/.claude/projects/"$(echo "$PWD" | tr / -)"/memory/       # this project's facts
   ```

   `corpus_counts` in doctor reports the same thing. An empty corpus has
   nothing to rank — write a fact or `memoryctl recall` to seed usage.
3. **Run the shim manually** to see its output:

   ```bash
   echo '{"session_id":"debug","cwd":"'"$PWD"'"}' | bash ~/.claude/hooks/v2/session-start.sh
   ```

   Look for `additionalContext` in the JSON. If absent, check stderr. Note the
   path is `~/.claude/hooks/v2/session-start.sh` — there is no
   `memory-session-start.sh` in v2.

## Hooks not firing after install

Restart Claude Code — hook registration is read from `settings.json` only at
launch. Then run `memoryctl doctor`; `no_broken_symlinks_*` catches the common
cause: the cloned repo was moved or deleted after install. Re-clone and re-run
`./install.sh`.

## `memoryctl: command not found` / doctor says "binary not built"

The Go binary is mandatory in v2 (the bash shims are thin dispatchers that
pipe the hook envelope to `memoryctl`). Build and install it:

```bash
cd /path/to/hypomnema
make build          # produces bin/memoryctl
./install.sh        # symlinks it into ~/.claude/bin/memoryctl
```

If `memoryctl_available` is WARN ("present but not on $PATH"), add
`~/.claude/bin` to your `PATH` for interactive shell use.

## "jq: command not found" / "perl is required" during install

`install.sh` requires `jq`, `perl`, and `awk`:

- macOS: `brew install jq` (perl/awk are preinstalled)
- Debian/Ubuntu: `sudo apt-get install jq perl`
- Fedora/RHEL: `sudo dnf install jq perl`

Re-run `./install.sh`.

## Sidecar is stale or missing

The sidecar (`~/.claude/memory/.sidecar.db`) is a rebuildable projection of
the WAL + native frontmatter. If doctor's `sidecar` check is WARN
("stale vs WAL" or "missing but .wal exists"):

```bash
memoryctl sidecar rebuild
```

Run this after bulk imports or a migration too.

## `MEMORY.md` index looks wrong or out of date

The Stop hook regenerates each project's `MEMORY.md` every session. To force
it on demand:

```bash
memoryctl reindex     # regenerates <project>/memory/MEMORY.md
```

There is no separate TF-IDF or FTS index in v2 — `reindex` only rebuilds the
`MEMORY.md` table of contents.

## The WAL (`~/.claude/memory/.wal`) grew very large

This is expected. **v2 has no WAL compaction** — it is an append-only log and
grows without bound. The file is the source of truth for effectiveness
history, so it stays complete by design.

It is plain text and cheap to read; size is rarely a real problem. If it ever
gets genuinely unwieldy you can archive it manually, but be aware that
truncating discards the outcome history that feeds Bayesian effectiveness:

```bash
cp ~/.claude/memory/.wal ~/.claude/memory/.wal.archive-$(date +%Y%m%d)
# then, only if you accept losing effectiveness history:
# : > ~/.claude/memory/.wal && memoryctl sidecar rebuild
```

## A file has an empty `status:` and never injects

doctor's `corpus_frontmatter_quality` WARN lists these. An empty `status:`
line drops the file from injection. Set it to `active` (or delete the line):

```yaml
status: active
```

## My rule shows under "ambient" in `self-profile.md`, never as trigger-useful/silent

By design when the file has `precision_class: ambient`. Ambient rules shape
behaviour continuously (tone, language preference, security baseline) without
producing citation events, so they are excluded from the precision denominator
on purpose. If the rule *should* produce visible citations, remove
`precision_class: ambient` and add `evidence:` phrases.

## Two Claude Code sessions running at once

Safe. WAL writes and the feedback-loop reads are locked. The per-session
dedup state is keyed by session id.

## Want to start over

Content lives in `~/.claude/memory-global/` and per-project
`~/.claude/projects/<slug>/memory/`; runtime state lives in
`~/.claude/memory/`. To reset the runtime while keeping your facts:

```bash
rm ~/.claude/memory/.wal ~/.claude/memory/.sidecar.db*
memoryctl sidecar rebuild
```

To wipe **everything** hypomnema owns, use the uninstaller:

```bash
./uninstall.sh --purge-memory --yes
```

## Tests fail after pulling a new version

```bash
cd /path/to/hypomnema
git pull
make test                    # Go suite (go test -race ./...)
bash hooks/v2/shims_test.sh  # bash shim contract tests
```

If a test is genuinely broken (not stale fixture state), check
`docs/MIGRATION.md` for incompatible changes between versions.
