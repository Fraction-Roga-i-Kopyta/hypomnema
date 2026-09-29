#!/usr/bin/env sh
# v2 thin shim: SubagentStart. Pipes the hook envelope to memoryctl inject.
# --subagent is unknown to a memoryctl older than subagent support, so such a
# binary refuses (exit 2) before reading stdin instead of treating the event
# as the parent's SessionStart; that refusal is swallowed here — a hook for a
# feature the binary lacks must be a no-op, never a harness-visible error.
MCTL="${HYPOMNEMA_MEMORYCTL:-$HOME/.claude/bin/memoryctl}"
command -v "$MCTL" >/dev/null 2>&1 || exit 0
"$MCTL" inject --event=SubagentStart --subagent 2>/dev/null
exit 0
