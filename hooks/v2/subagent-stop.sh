#!/usr/bin/env sh
# v2 thin shim: SubagentStop. Classifies the subagent's memory citations.
MCTL="${HYPOMNEMA_MEMORYCTL:-$HOME/.claude/bin/memoryctl}"
command -v "$MCTL" >/dev/null 2>&1 || exit 0
exec "$MCTL" close --subagent
