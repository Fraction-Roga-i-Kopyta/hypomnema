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
- all eight v2 shims exist and are executable in `~/.claude/hooks/v2/`
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

## A subagent got no memory

1. **Check whether the agent type is meant to be skipped.** By default
   `fork`, `Explore`, `claude-code-guide`, and `statusline-setup` get no
   memory — `HYPOMNEMA_SUBAGENT_SKIP` fully replaces that list when set (a
   set-but-empty value means skip nothing, and a single `*` entry means skip
   every agent type — the full opt-out):

   ```bash
   echo "$HYPOMNEMA_SUBAGENT_SKIP"
   ```

   To opt every subagent out permanently, set `HYPOMNEMA_SUBAGENT_SKIP=*` in
   `settings.json`'s top-level `env` block so both the `SubagentStart` and
   `SubagentStop` hooks inherit it — deleting the hook entries themselves
   instead is undone by the next `./install.sh`.
2. **A resumed agent getting nothing is by design**, not a bug: the harness
   never adds a second `SubagentStart` context to a transcript that already
   carries one, and `memoryctl` mirrors that — a key whose
   `rendered-<key>.list` already exists is a no-op on every later start for
   that key.
3. **Run `memoryctl doctor`.** A missing or misrouted `SubagentStart`/
   `SubagentStop` registration shows up in `settings_hooks_registered` with
   the same wording as any other missing hook, e.g.:

   ```
   settings_hooks_registered FAIL: subagent-start.sh missing (want SubagentStart); subagent-stop.sh missing (want SubagentStop) — re-run ./install.sh
   ```

   A `memoryctl_available` line whose detail contains `predates subagent
   support` means the resolved binary itself is too old. With the current
   shims that is a no-op, not a harness-visible error — `subagent-start.sh`
   / `subagent-stop.sh` swallow the binary's exit-2 refusal on `--subagent`
   and exit 0 either way, so a subagent just runs without memory instead of
   looping or corrupting the parent session (§1, §8, §9 of
   hooks-contract.md). `doctor` still warns because the subagent gets no
   memory either way: `make build` then re-run `./install.sh` (it refuses to
   proceed against a `bin/memoryctl` that doesn't understand `close
   --subagent`).
4. **Claude Code older than the verified version (2.1.284) may not deliver
   `additionalContext` on `SubagentStart`**, even though the hook fires and
   `memoryctl` still writes its `inject` WAL row — the subagent's context
   never actually receives the text, but `ref_count` keeps climbing as if it
   had. Update Claude Code, or, until that's an option, set
   `HYPOMNEMA_SUBAGENT_SKIP=*` in `settings.json`'s top-level `env` block
   (item 1 above) rather than deleting the `SubagentStart` registration —
   deleting it is undone by the next `./install.sh`.
5. **Nested agents (a subagent launching its own subagent) and a launching
   call this hook cannot find at all** both fall back to ranking against the
   parent's 3 most recent prompts instead of a specific task — expected, not
   a bug; the subagent still gets memory, just against a less specific
   query. This fallback fires when the agent has no launch acknowledgement
   and no unanswered, unclaimed call of its type is left in the transcript
   tail — every such call already answered or taken by a sibling, or none
   there at all (a nested agent's launching call lives in its *parent
   subagent's* transcript, not the top-level one this hook reads) —
   not merely because the acknowledgement hasn't landed yet; see the next
   section for that case.

## A subagent got memory for a sibling's task

Only an **acknowledged** launch is matched exactly: the `tool_result` naming
`agentId: <id>` identifies that agent's own call even among several parallel
calls of the same type. A synchronous launch is never acknowledged, and a
background launch's acknowledgement can still be unwritten when
`SubagentStart` fires. Either way, the subagent instead claims the **oldest
still-unclaimed** call of its type (`.runtime/agentcall-<id>.claim`, an
atomic create) — so N parallel launches of one type, none yet acknowledged,
get N *distinct* calls, assigned in the order their `SubagentStart` hooks
actually fire, not in launch order. A mismatch (a subagent ranked against a
sibling's task) now needs the hooks themselves to fire out of launch order —
still possible under enough concurrency, but no longer the default outcome
of parallel launches the way "newest pending call wins" was. The effect,
when it does happen, is a less relevant fact set for that one subagent's
launch, not data corruption or a wrong classification later — `SubagentStop`
still classifies citations under the correct agent's own key regardless of
what `SubagentStart` ranked against.

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

## doctor warns `store_resolution: legacy store(s) no longer read`

v2.13 hardened store resolution: the project slug is now derived from the
sanitized **canonical git root** (with a linked worktree mapped to its main
checkout), matching exactly how Claude Code itself resolves
`autoMemoryDirectory`. Before v2.13 the slug came straight from `cwd` with a
naive `/` → `-` substitution — a path containing dots, underscores, spaces,
or non-ASCII characters, a linked worktree, or a repo subdirectory produced a
*different* slug than the harness (and current hypomnema) would compute.

**Cause:** facts were written into that old, differently-keyed store
directory, and store resolution no longer points there, so they silently
stop being read.

**Fix:** move the stray `.md` files into the store doctor names as current,
then rebuild the sidecar. `memoryctl doctor` prints both `…/memory` paths in
the `store_resolution` line — the resolved (current) store first, then each
legacy store it found with facts still in it — copy them into these two
variables and run:

```bash
LEGACY_STORE="<the .../memory path doctor lists after 'legacy store(s) no longer read', before its '(N facts)'>"
NEW_STORE="<the .../memory path doctor lists at the start of the store_resolution line, and again after 'move their facts (not MEMORY.md) into'>"
for f in "$LEGACY_STORE"/*.md; do
  [ "$(basename "$f")" = "MEMORY.md" ] && continue
  mv -n "$f" "$NEW_STORE"/
done
memoryctl sidecar rebuild
```

`mv -n` never overwrites — a same-named file already present in `$NEW_STORE`
(or `MEMORY.md`, skipped outright since it is regenerated per store, not
moved) is left behind in `$LEGACY_STORE`. Check what remains there and merge
those by hand. Moved facts start a fresh ranking history in the new store —
their old WAL rows stay attributed to the legacy project tag.

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

## doctor warns `citation_signal: facts were injected … but never cited`

Facts got injected (`# Memory Context` fired) but none of the sessions
`close` classified in the last 7 days carried a `cite-useful` row — WARN
only fires once at least 3 distinct classified sessions injected something
and every one of them cited zero delivered facts. A session counts only if
it carries a `cite-*` row, so sessions closed before the v2.14 upgrade (the
model was never told to cite) and sessions whose transcript `close` could
not read are left out.
This means the honest-usefulness signal itself is off: `cite-useful` /
`cite-silent` come solely from an explicit `<cc-memory filenames="…">`
citation in the assistant's own transcript text (CLAUDE.md "Citing memory"),
never from an evidence-phrase or name match, so a session can go quiet for a
few different reasons. Check, in order:

1. **Is the instruction line actually reaching the model?** The injected
   `# Memory Context` block should start with "When a fact below changes
   what you say or do, wrap that sentence in `<cc-memory filenames="FILE">…
   </cc-memory>`". If the block is missing entirely, see "SessionStart
   injects nothing" above. If it's present but the model's context doesn't
   actually show it, check whether `additionalContext` is being diverted to
   a file by an oversized payload — see the 8 KB budget in "Memory layout"
   (`CLAUDE.md`).
2. **Are citations landing but not counting?** Every session this check
   counts had its transcript read (each carries a `cite-none` row). Grep the
   WAL for `|cite-undelivered|` in the same window: present means the model
   does cite, but names facts that were not delivered in that session — for
   example a store reached through a symlinked path (see `docs/EVENTS.md`).
   An unreadable transcript is a different failure: `close` writes no
   `cite-*` row at all for it, so it never shows up here.
3. **Is the model citing at all?** If the instruction line is present and
   transcripts are readable, the model isn't wrapping used facts in the tag.
   This is a prompting/model-behaviour question, not a hypomnema bug — the
   tag is deliberately hidden from the user, so there's nothing to visually
   confirm in the transcript besides grepping raw JSONL for `<cc-memory`.

`citation_signal` reports OK with "no citation data yet" before any `cite-*`
row has ever been written (a fresh install, or one that hasn't seen a Stop
hook since the v2.14 cutover) — that is expected, not a problem.

**Subagent keys count as their own sessions here.** A WAL row whose session
column (`$4`) contains `:` (`<session_id>:<agent_id>`) came from a
subagent's own `SubagentStop`, and both `citation_signal` and
`open_quanta_last_30d` treat it as a session in its own right — it is not
folded back into its parent. If the WARN is puzzling given how quiet the
orchestrating sessions actually are, split the rows and check the two
populations separately:

```bash
awk -F'|' '$4 ~ /:/'  ~/.claude/memory/.wal | tail -50   # subagent keys
awk -F'|' '$4 !~ /:/' ~/.claude/memory/.wal | tail -50   # parent sessions
```

A subagent whose own transcript was unreadable (a bad or missing
`agent_transcript_path`) still gets a `session-close` row for its key, but no
`cite-*` row at all — the same "transcript unreadable" case as a normal
session, just keyed to the subagent.

## My rule shows under "ambient" in `self-profile.md`, never as cite-useful/silent

By design when the file has `precision_class: ambient`. Ambient rules shape
behaviour continuously (tone, language preference, security baseline) without
producing citation events, so they are excluded from the precision denominator
on purpose. If the rule *should* produce visible citations, remove
`precision_class: ambient` — citing still requires the model to actually wrap
the sentence in `<cc-memory filenames="…">`; `evidence:` phrases no longer
produce `cite-useful`/`cite-silent` (they only drive the `ablate` holdout
comparison).

### An injected fact ends with `…(truncated — N B total; full text: <path>)`

Bodies are capped at 2 500 B per record so the whole payload stays under the
8 KB inline limit. The marker names the total size and the file's absolute
path — read it for the rest (`memoryctl recall` applies the same cap to its
top hit). `memoryctl doctor` lists such facts under `oversized_facts` — split
them into focused facts, or retire the journal-like ones and keep a short
summary.

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
