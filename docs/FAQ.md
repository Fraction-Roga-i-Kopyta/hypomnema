# FAQ

## Can I sync memory across machines?

Yes. Your memory content is plain markdown. The cleanest thing to version is
the global store, which is nothing but `.md` files:

```bash
cd ~/.claude/memory-global
git init
git add . && git commit -m "memory baseline"
git remote add origin git@github.com:you/private-memory.git
git push -u origin main
```

On the second machine, clone into the same location:

```bash
git clone git@github.com:you/private-memory.git ~/.claude/memory-global
```

Do **not** version the runtime metadata under `~/.claude/memory/` (`.wal`,
`.sidecar.db`, `.runtime/`, `self-profile.md`) — it is per-machine and the
sidecar is rebuildable with `memoryctl sidecar rebuild`. Per-project facts
live under `~/.claude/projects/<slug>/memory/` and can be versioned the same
way if you want them portable.

Run `./install.sh` on each machine to wire up the hooks and `memoryctl`; it
leaves existing memory directories alone.

## How do I fix a wrongly-recorded mistake?

Edit the file directly. Stores are flat, so the file is right there in the
store — `~/.claude/memory-global/<slug>.md` for a global fact, or
`~/.claude/projects/<slug>/memory/<slug>.md` for a project one. There is no
`mistakes/` subdirectory.

Statuses you can hand-set are `active` and `pinned` (there is no `archived`
status — `stale` is sidecar-managed, not written by hand). To retire a fact,
either delete the file or just let it go stale from disuse:

```bash
rm ~/.claude/memory-global/<wrong-slug>.md
```

The next Stop hook regenerates `MEMORY.md` (or run `memoryctl reindex`). There
is no TF-IDF index to rebuild.

## How does upgrading work?

```bash
cd /path/to/hypomnema
git pull
make build      # rebuild the binary if Go code changed
./install.sh    # idempotent — re-installs shims, leaves memory alone
```

If a release introduces a frontmatter field, `docs/MIGRATION.md` lists what to
update. Existing files without the new field keep working; the default applies.

## Is my memory shared with Anthropic?

No. Hypomnema reads files locally and injects them into the context your
Claude Code session sends. The model sees the injected context (that is how
injection works), but the files never leave your disk via hypomnema.

## Can I exclude a project from memory?

There is no project-exclusion switch in v2 — no `projects.json`, no
`.confidence-excludes`. Project scope is derived automatically from the working
directory. In practice:

- A project with no native memory files simply has nothing project-local to
  inject. Global facts (`~/.claude/memory-global/`) still apply everywhere.
- To silence memory in a directory entirely, disable the hypomnema hooks for
  that machine/profile (uninstall, or remove the entries from
  `settings.json`) — injection is all-or-nothing per install.

## Is there an `injected:` / `referenced:` frontmatter field?

No. Injection recency is tracked by the **sidecar**, not in frontmatter. The
sidecar records `last_injected` per fact (from WAL `inject` events) and the
ranker uses it for the recency term and for decay (age counts from last
injection, falling back to `created`). You never hand-edit these — they are
sidecar-managed like `ref_count` and `effectiveness`.

## How do I see what got injected last session?

```bash
tail -50 ~/.claude/memory/.wal | grep inject
```

Each line is `date|event|slug|session`.

## I'm running multiple Claude Code clients concurrently

Safe. WAL writes and feedback-loop reads are locked; per-session dedup state is
keyed by session id.

## What does `scope: narrow` mean?

In v2 `scope` (`universal` | `domain` | `narrow`) is **informational
metadata** — it documents how broadly a lesson applies. It does **not** gate
injection. The v1 substring-trigger mechanism is gone: the single ranker
treats all tokens (keywords, name, description, body) as relevance signal, and
every `active`/`pinned` fact in scope is a ranking candidate regardless of its
`scope` value. Write good `keywords`/`domains` and let the ranker sort it out.

## Why is hypomnema written in Go, not pure bash?

It is Go-primary by design. The `memoryctl` binary is the mandatory engine —
it does all the real work: ranking and injection, WAL accounting, Bayesian
effectiveness, decay, the secrets gate, dedup, self-profile, `doctor`. The six
files in `~/.claude/hooks/v2/` are ~5-line bash shims that read the hook
envelope on stdin and pipe it to `memoryctl`; they contain no logic. This is
the reverse of v1, where bash carried the hot paths and Go was an optional
add-on. `install.sh` refuses to proceed without a built binary, so `make build`
is a required install step, not an opt-in.

## How do I write memory in Cyrillic / Chinese / Greek?

Just write it. The ranker's tokenizer is Unicode-aware, so non-ASCII memories
rank normally with no special handling.

## How do I measure what hypomnema actually saved me?

```bash
memoryctl self-profile
```

Generates `~/.claude/memory/self-profile.md` with effectiveness scores,
calibration metrics, and weak spots from the WAL. The Stop hook also
regenerates it automatically each session.
