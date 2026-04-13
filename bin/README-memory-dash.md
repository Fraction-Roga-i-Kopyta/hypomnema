# memory-dash

Console TUI for `~/.claude/memory/` observation and operations.

## Usage

    memory-dash         # short alias (after install.sh)
    memory-dashboard.sh # full name

Tabs: `[Tab]` switch / `[r]` refresh / `[1-8]` run op (Ops tab) / `[q]` quit.

## Environment overrides

| Variable | Default |
|----------|---------|
| `MEMORY_DIR` | `~/.claude/memory` |
| `BIN_DIR` | `~/.claude/bin` |
| `HOOKS_DIR` | `~/.claude/hooks` |
| `MEMORY_MD_PATH` | `~/.claude/projects/-Users-akamash/memory/MEMORY.md` |

## Tests

    bash bin/tests/test-memory-dashboard.sh

## Spec and plan

See `docs/specs/2026-04-13-memory-dashboard-design.md` and `docs/plans/2026-04-13-memory-dashboard.md`.
