#!/bin/bash
# Portable file metadata helpers (BSD stat on macOS vs GNU stat on Linux).
# Source from hook scripts. Pure functions, no side effects.
#
#   _stat_mtime <path>   → epoch seconds (mtime); 0 if file missing
#   _stat_size  <path>   → bytes;                 ; 0 if file missing
#
# Flavour detection is cached at source-time instead of per-call, so each
# hook pays one `stat` probe instead of one per _stat_*() invocation.
# (audit-2026-04-16 P6)

if stat -f %m /dev/null >/dev/null 2>&1; then
  _STAT_MTIME_FMT='-f %m'
  _STAT_SIZE_FMT='-f %z'
else
  _STAT_MTIME_FMT='-c %Y'
  _STAT_SIZE_FMT='-c %s'
fi

_stat_mtime() {
  local f="$1"
  [ -e "$f" ] || { echo 0; return 0; }
  # shellcheck disable=SC2086
  stat $_STAT_MTIME_FMT "$f" 2>/dev/null || echo 0
}

_stat_size() {
  local f="$1"
  [ -e "$f" ] || { echo 0; return 0; }
  # shellcheck disable=SC2086
  stat $_STAT_SIZE_FMT "$f" 2>/dev/null || echo 0
}
