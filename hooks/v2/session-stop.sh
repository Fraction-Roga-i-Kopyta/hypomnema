#!/usr/bin/env sh
# v2 thin shim: Stop. Delegates to memoryctl close.
# close verb lands in Phase 4; preserves its exit code.
MCTL="${HYPOMNEMA_MEMORYCTL:-memoryctl}"
command -v "$MCTL" >/dev/null 2>&1 || exit 0
exec "$MCTL" close
