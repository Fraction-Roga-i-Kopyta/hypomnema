#!/usr/bin/env sh
# v2 thin shim: PreToolUse (Bash). Delegates to memoryctl guard-bash, which
# blocks the model's own Bash call to the /memboard verbs pin, unpin and mark
# (only "mark <slug> done" passes). Not exec: a memoryctl older than guard-bash
# refuses the unknown verb with exit 2, and a panic exits 2 too — so this shim
# blocks (exit 2) only when stderr starts with the BLOCKED (hypomnema) marker,
# re-emitting it for the model; any other outcome passes (exit 0).
MCTL="${HYPOMNEMA_MEMORYCTL:-$HOME/.claude/bin/memoryctl}"
command -v "$MCTL" >/dev/null 2>&1 || exit 0
err=$("$MCTL" guard-bash 2>&1 >/dev/null)
case "$err" in
  "BLOCKED (hypomnema)"*)
    printf '%s\n' "$err" >&2
    exit 2
    ;;
esac
exit 0
