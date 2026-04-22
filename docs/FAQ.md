# FAQ

## Can I sync memory across machines?

Yes. `~/.claude/memory/` is just markdown + JSON. Initialize it as a git repo and push to a private remote:

```bash
cd ~/.claude/memory
git init
cat > .gitignore <<EOF
.wal
.wal.lockd
.tfidf-index
.runtime/
_agent_context.md
EOF
git add . && git commit -m "memory baseline"
git remote add origin git@github.com:you/private-memory.git
git push -u origin main
```

On the second machine, clone instead of running install for the memory dir:

```bash
git clone git@github.com:you/private-memory.git ~/.claude/memory
```

Then run `./install.sh` from the cloned hypomnema repo to set up the hook symlinks (memory dir already exists, will be left alone).

## How do I fix a wrongly-recorded mistake?

Edit the file in `~/.claude/memory/mistakes/<slug>.md` directly. Increment `recurrence` if you keep hitting it; flip `status` to `archived` if it stopped being relevant.

If the wrong file should not exist at all:

```bash
rm ~/.claude/memory/mistakes/<wrong-slug>.md
```

The next Stop hook regenerates `MEMORY.md` and the TF-IDF index automatically.

## How does upgrading work?

```bash
cd /path/to/hypomnema
git pull
./install.sh   # idempotent, re-symlinks hooks, leaves memory alone
```

If a release introduces a frontmatter field, `docs/MIGRATION.md` lists what to update. Existing files without the new field continue to work; the field's default applies.

## Is my memory shared with Anthropic?

No. Hypomnema reads files locally and prepends them to the prompt your Claude Code session sends. The model sees the injected context but the files never leave your disk via hypomnema. (The model itself sees the prompt, of course — that's how injection works.)

## Can I exclude a project from memory?

Two ways:

1. Don't add it to `projects.json`. Hypomnema will still inject `project: global` mistakes/strategies but no project-specific content.
2. Add the project root path to `~/.claude/memory/.confidence-excludes`. SessionStart skips injection entirely when CWD matches.

## What's the difference between `injected` and `referenced` in frontmatter?

- `injected: YYYY-MM-DD` — last date this file was placed in a session's `# Memory Context` block.
- `referenced: YYYY-MM-DD` — last date a hook (any hook) read or wrote to the file.

`referenced` updates more often. The two together let lifecycle rotation distinguish "actively used" from "just sitting there."

## How do I see what got injected last session?

```bash
tail -50 ~/.claude/memory/.wal | grep inject
```

Each line shows the date, event type, slug, and session ID.

## I'm running multiple Claude Code clients concurrently

Safe. WAL writes use `mkdir`-based locking; concurrent reads (v0.8+) also lock. The dedup list is per-session (filename includes session ID).

## What does `scope: narrow` mean?

A mistake or strategy with `scope: narrow` is only injected when the current prompt contains one of its `keywords`. `scope: universal` is always injected (subject to caps). `scope: domain` is injected when the current project's domain matches `domains:` in the file.

Use `narrow` for very specific, easily-mismatched advice. Use `universal` for cross-cutting rules.

## Why is hypomnema bash and not Python / Node?

Zero install friction. Bash + jq + awk + perl are on every macOS and Linux out of the box. The hot paths (SessionStart injection, UserPromptSubmit trigger match, Stop hook outcome detection) are pure shell, so install.sh is three seconds of `ln -sf`. Fuzzy dedup and the FTS5 shadow pass are optional opt-ins via a Go binary (`make build`); without it, those features disable silently and the rest of the system works unchanged.

## How do I write memory in Cyrillic / Chinese / Greek?

Just write it. As of v0.8, body tokenization uses perl `\w{4,}` with the `/u` flag — Unicode-aware for any script. No need to add explicit `evidence:` for non-ASCII memories.

## How do I measure what hypomnema actually saved me?

```bash
bash ~/.claude/bin/memory-self-profile.sh
```

Generates `~/.claude/memory/self-profile.md` with effectiveness scores, calibration metrics, and weak spots based on WAL events.
