#!/usr/bin/env sh
# v2 thin shim: PreToolUse (Write/Edit). Delegates to memoryctl guard.
# guard verb lands in Phase 5; preserves its exit code so the hook can block.
MCTL="${HYPOMNEMA_MEMORYCTL:-$HOME/.claude/bin/memoryctl}"
command -v "$MCTL" >/dev/null 2>&1 || exit 0
exec "$MCTL" guard
