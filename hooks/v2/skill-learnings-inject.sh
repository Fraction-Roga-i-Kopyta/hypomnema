#!/usr/bin/env sh
# v2 thin shim: PostToolUse(Skill). Injects accumulated skill-learning facts
# for the activated skill. Fail-safe: exit 0 if memoryctl is absent.
MCTL="${HYPOMNEMA_MEMORYCTL:-$HOME/.claude/bin/memoryctl}"
command -v "$MCTL" >/dev/null 2>&1 || exit 0
exec "$MCTL" skill-inject
