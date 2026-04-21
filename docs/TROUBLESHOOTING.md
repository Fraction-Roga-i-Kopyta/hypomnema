# Troubleshooting

## SessionStart injects nothing

**Check 1: hook is registered.** Open `~/.claude/settings.json` and confirm there is a `hooks.SessionStart` entry pointing at `~/.claude/hooks/memory-session-start.sh`.

**Check 2: project is recognized.** From your project root, run:

```bash
jq --arg pwd "$PWD" '.[$pwd]' ~/.claude/memory/projects.json
```

If the result is `null`, add the project: edit `projects.json` or re-run `./install.sh --discover`.

**Check 3: there is anything to inject.** Run:

```bash
ls ~/.claude/memory/mistakes ~/.claude/memory/feedback ~/.claude/memory/strategies
```

If empty (and you didn't seed), there's nothing to rank. The seeds in `seeds/` are copied on first install.

**Check 4: hook is not silently failing.** Run the hook manually:

```bash
echo '{"session_id":"debug","cwd":"'"$PWD"'"}' | bash ~/.claude/hooks/memory-session-start.sh
```

Look for `additionalContext` in the JSON output. If absent, look at stderr for parse errors.

## "jq: command not found" during install

Install jq:

- macOS: `brew install jq`
- Debian/Ubuntu: `sudo apt-get install jq`
- Fedora/RHEL: `sudo dnf install jq`
- Other: https://stedolan.github.io/jq/download/

Re-run `./install.sh`.

## "perl is required"

Perl is preinstalled on macOS and almost every Linux distro. If missing:

- Debian/Ubuntu: `sudo apt-get install perl`
- Alpine: `apk add perl`

## Hooks not firing after install

Restart Claude Code. The hook block is registered in `settings.json` only when the app/CLI starts.

If still not firing after restart:

```bash
ls -la ~/.claude/hooks/
```

You should see symlinks to the cloned hypomnema repo. If the symlinks are broken (`ls -la` shows red / `??`), the cloned repo was moved or deleted — re-clone and re-run install.

## WAL grew very large

Normal: hypomnema compacts the WAL when it crosses ~1200 lines. Manual compaction:

```bash
bash ~/.claude/hooks/wal-compact.sh
```

## My rule shows under "ambient activations" in `self-profile.md` and never in `trigger-useful` / `silent-applied`

That's by design when the file has `precision_class: ambient` in frontmatter. Ambient rules shape behaviour continuously (tone, language preference, security baseline) without producing citation events, so they're excluded from the precision denominator on purpose — counting them as "silent-noise" would drag the measurable-precision ratio down as false noise.

If the rule *is* supposed to produce visible citations and you want it in the measurable pool, remove `precision_class: ambient` from the frontmatter and add `evidence:` phrases. See README § Precision metric.

## Memory injection feels noisy

Edit `~/.claude/memory/.config.sh` (created on first install of v0.8+) to lower caps:

```bash
MAX_FILES=15           # default 22
MAX_SCORED_TOTAL=8     # default 12
MAX_GLOBAL_MISTAKES=2  # default 3
```

If `.config.sh` does not exist yet, copy from `templates/.config.sh.example`.

## Cyrillic / non-ASCII text in memory bodies

Fully supported as of v0.8 — `evidence_from_body` uses perl `\w` with the `/u` flag. If you see broken tokens in `~/.claude/memory/.tfidf-index`, regenerate:

```bash
bash ~/.claude/hooks/regen-memory-index.sh
```

## Two Claude Code sessions running at once

Safe: WAL writes are locked via `lib/wal-lock.sh`. Feedback-loop reads (Stop hook) are also locked as of v0.8.

## Want to start over

```bash
mv ~/.claude/memory ~/.claude/memory.backup-$(date +%Y%m%d)
./install.sh   # re-creates fresh memory tree, re-seeds
```

## Tests fail after pulling new version

```bash
cd /path/to/hypomnema
git pull
bash hooks/test-memory-hooks.sh
```

If a test is genuinely broken (not just stale fixture state), check `docs/MIGRATION.md` for incompatible changes between versions.
