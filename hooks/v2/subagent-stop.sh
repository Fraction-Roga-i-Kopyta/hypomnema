#!/usr/bin/env sh
# v2 thin shim: SubagentStop. Classifies the subagent's memory citations.
# Exit 2 here would tell the harness to block the subagent's own stop and
# show it stderr, looping the subagent — an older memoryctl's exit-2 refusal
# on --subagent (it predates subagent support) is swallowed here instead: a
# hook for a feature the binary lacks must be a no-op, never a
# harness-visible error.
MCTL="${HYPOMNEMA_MEMORYCTL:-$HOME/.claude/bin/memoryctl}"
command -v "$MCTL" >/dev/null 2>&1 || exit 0
"$MCTL" close --subagent 2>/dev/null
exit 0
