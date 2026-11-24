# Hypomnema Quickstart

Five minutes from clone to your first injected context.

## Prerequisites

- **Claude Code v2.1.59+** (native file memory — hypomnema builds on it).
- **Go 1.25+** and `make` — hypomnema's core is the Go binary `memoryctl`; the
  hooks are thin shims that call it. The binary is **not** checked in (it's
  built locally), so building it is a required step, not optional.
- `jq` (install.sh uses it to edit `settings.json`).

## 1. Build and install (1 minute)

```bash
git clone https://github.com/Fraction-Roga-i-Kopyta/hypomnema.git
cd hypomnema
make build       # compiles ./bin/memoryctl (static, CGO_ENABLED=0)
./install.sh     # symlinks memoryctl into ~/.claude/bin/, installs the 6
                 # hook shims into ~/.claude/hooks/v2/, registers them in
                 # settings.json, creates ~/.claude/memory-global/
```

`./install.sh` refuses to run if `bin/memoryctl` is missing — run `make build`
first. It also checks that Claude Code is ≥ 2.1.59 before touching anything.

Verify the install end-to-end:

```bash
~/.claude/bin/memoryctl doctor   # expect: 0 FAIL; an empty corpus is a WARN on day one
```

`memoryctl` is installed into `~/.claude/bin/`; add
`export PATH="$HOME/.claude/bin:$PATH"` to your shell profile to call it as
plain `memoryctl`.

## 2. Projects need no registration

v2 derives each project's memory store from the working directory
(`~/.claude/projects/<slug>/memory/`) — there is nothing to register, no
`projects.json`, no wizard. Global facts (useful in every project) live in
`~/.claude/memory-global/`.

## 3. (Optional) point your global CLAUDE.md at the protocol (15 s)

```bash
./install.sh --patch-claude-md
```

Appends a short section to `~/.claude/CLAUDE.md` telling future Claude Code
sessions how the memory schema works. `./uninstall.sh` removes it again.

## 4. Restart Claude Code (10 seconds)

Quit the Claude Code app/CLI and start it again — the hooks register on the
next launch.

## 5. Open a session and check memory (1 minute)

```bash
cd ~/Development/<any-project>
claude
```

On SessionStart the `session-start` hook injects the project's fixed core —
its freshest `continuity`, pinned facts and project feedback rules (not a
ranking); there won't be any yet on a fresh install. Other facts are read
from the `MEMORY.md` index when their description fits, or pulled on demand
from inside a session: `memoryctl recall "<query words>"`. Recommended:
pull-only mode — `HYPOMNEMA_PROMPT_INJECT=off` in `settings.json`'s `env`
block plus `@~/.claude/memory-global/MEMORY.md` in your user CLAUDE.md (by
default each prompt also gets a gated ranked push).

## 6. Record your first real mistake (90 seconds)

When you hit a bug or take a wrong approach during a real task, ask Claude to
remember it. Claude writes a **flat** native memory file — the kind is the
`type:` frontmatter field (`mistake`), not a subdirectory — into
`~/.claude/projects/<slug>/memory/` (or `~/.claude/memory-global/` for a
cross-project lesson). Later sessions find it through the `MEMORY.md` index
(or the per-prompt push, if left on) when a task names it.

## What's next

- `docs/TROUBLESHOOTING.md` — when context isn't injecting (`memoryctl doctor` first)
- `docs/FAQ.md` — sync across machines, upgrade path, fixing wrongly-recorded entries
- `README.md` — architecture and scoring details
- `CLAUDE.md` (repo root) — the full agent protocol with worked examples
