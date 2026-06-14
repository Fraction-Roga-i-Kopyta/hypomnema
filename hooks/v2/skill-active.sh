#!/usr/bin/env sh
# v2 thin shim: PreToolUse(Skill). Records the activated skill for this session
# so capture can tag skill-learnings reliably. Fail-safe: exit 0 if absent.
MCTL="${HYPOMNEMA_MEMORYCTL:-$HOME/.claude/bin/memoryctl}"
command -v "$MCTL" >/dev/null 2>&1 || exit 0
exec "$MCTL" skill-active
