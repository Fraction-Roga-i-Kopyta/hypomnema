#!/usr/bin/env sh
# v2 thin shim: UserPromptSubmit. Pipes the hook envelope to memoryctl inject.
MCTL="${HYPOMNEMA_MEMORYCTL:-$HOME/.claude/bin/memoryctl}"
command -v "$MCTL" >/dev/null 2>&1 || exit 0
exec "$MCTL" inject --event=UserPromptSubmit
