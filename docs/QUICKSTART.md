# Hypomnema Quickstart

Five minutes from clone to your first injected context.

## 1. Install (30 seconds)

```bash
git clone https://github.com/<your-fork>/hypomnema.git
cd hypomnema
./install.sh
```

The installer checks for `jq`, `perl`, `awk`, and `bash 3.2+`. If anything is missing, it tells you what to install.

### Optional: fuzzy dedup for mistakes

The PreToolUse dedup hook blocks creation of a `mistakes/*.md` file whose root-cause is >80% similar to an existing one. It's implemented in the Go binary `memoryctl`. To enable it:

```bash
# macOS: Go already installed via Homebrew, or
brew install go

# Linux
sudo apt-get install golang   # or: asdf install golang ...
```

Then build and reinstall:

```bash
make build       # produces ./bin/memoryctl (static, CGO_ENABLED=0)
./install.sh     # symlinks it into ~/.claude/bin/memoryctl
```

Without `memoryctl` the hook silently exits 0 — dedup is off, everything else still works. If you're seeing many near-duplicate mistakes accumulate, build `memoryctl` and they'll start being blocked at write time.

## 2. Run the wizard (90 seconds)

```bash
./install.sh --discover
```

The wizard scans `~/Development/`, `~/code/`, `~/projects/`, and `~/src/` for git repos and asks which ones you want hypomnema to track. It updates `~/.claude/memory/projects.json` with your selections.

## 3. Add the agent protocol to your global CLAUDE.md (15 seconds)

```bash
./install.sh --patch-claude-md
```

Adds a four-line section to `~/.claude/CLAUDE.md` pointing future Claude Code sessions at the memory schema.

## 4. Restart Claude Code (10 seconds)

Quit the Claude Code app/CLI and start it again. Hooks register on the next session.

## 5. Open a session in a tracked project (1 minute)

```bash
cd ~/Development/<one-of-the-tracked-projects>
claude
```

In your first message, ask: "What's in memory?" — Claude should mention the seeded mistakes from `seeds/` and any continuity from prior sessions (none yet).

## 6. Record your first real mistake (90 seconds)

When you hit a bug or take a wrong approach during a real task, ask Claude:

> "Record this as a mistake — root cause was X, prevention is Y."

Claude writes a file in `~/.claude/memory/mistakes/`. Next session, it'll be injected automatically.

## What's next

- `docs/TROUBLESHOOTING.md` — when context isn't injecting
- `docs/FAQ.md` — sync across machines, upgrade path, fixing wrongly-recorded entries
- `README.md` — full architecture and scoring details
- `CLAUDE.md` (repo root) — full agent protocol with examples
