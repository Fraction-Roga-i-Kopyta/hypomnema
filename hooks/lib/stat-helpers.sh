#!/bin/bash
# Portable file metadata helpers (BSD stat on macOS vs GNU stat on Linux).
# Source from hook scripts. Pure functions, no side effects.
#
#   _stat_mtime <path>   → epoch seconds (mtime); 0 if file missing
#   _stat_size  <path>   → bytes;            ; 0 if file missing

_stat_mtime() {
  local f="$1"
  [ -e "$f" ] || { echo 0; return 0; }
  if stat -f %m "$f" >/dev/null 2>&1; then
    stat -f %m "$f"
  else
    stat -c %Y "$f" 2>/dev/null || echo 0
  fi
}

_stat_size() {
  local f="$1"
  [ -e "$f" ] || { echo 0; return 0; }
  if stat -f %z "$f" >/dev/null 2>&1; then
    stat -f %z "$f"
  else
    stat -c %s "$f" 2>/dev/null || echo 0
  fi
}
