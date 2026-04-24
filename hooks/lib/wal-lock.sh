# shellcheck shell=bash
# Portable WAL lock via mkdir atomicity (POSIX, no external deps).
# Source from hook scripts AFTER $MEMORY_DIR is set:
#   source "$(dirname "$0")/lib/wal-lock.sh"
#   wal_run_locked bash -c '...'    # run command under exclusive WAL lock
#   wal_lock_acquire && ... && wal_lock_release

WAL_LOCK_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}/.wal.lockd"
WAL_LOCK_MAX_WAIT_MS="${WAL_LOCK_MAX_WAIT_MS:-1000}"
WAL_LOCK_STALE_SEC="${WAL_LOCK_STALE_SEC:-10}"

_wal_lock_mtime() {
  if [[ "$OSTYPE" == darwin* ]]; then
    stat -f %m "$1" 2>/dev/null
  else
    stat -c %Y "$1" 2>/dev/null
  fi
}

wal_lock_acquire() {
  local i=0 max=$((WAL_LOCK_MAX_WAIT_MS / 50)) now mtime age
  while ! mkdir "$WAL_LOCK_DIR" 2>/dev/null; do
    if [ -d "$WAL_LOCK_DIR" ]; then
      now=$(date +%s)
      mtime=$(_wal_lock_mtime "$WAL_LOCK_DIR")
      if [ -n "$mtime" ]; then
        age=$(( now - mtime ))
        if [ "$age" -gt "$WAL_LOCK_STALE_SEC" ]; then
          rmdir "$WAL_LOCK_DIR" 2>/dev/null
          continue
        fi
      fi
    fi
    i=$((i+1))
    [ "$i" -ge "$max" ] && return 1
    sleep 0.05
  done
  return 0
}

wal_lock_release() {
  rmdir "$WAL_LOCK_DIR" 2>/dev/null
  return 0
}

# Run command under lock. If lock acquisition fails, policy is
# controlled by WAL_LOCK_STRICT:
#   0 (default) — legacy fail-open: log to stderr and run anyway.
#                 Preserves pre-v0.15 behaviour so existing callers
#                 that tolerate a race window still work.
#   1           — fail-closed: return 75 (EX_TEMPFAIL) without
#                 running. Use for critical writes the caller would
#                 rather skip than emit twice (outcome-positive,
#                 dedupe-sensitive events).
# Either way the lock-acquisition failure is now surfaced on stderr
# so session-stop's journal and CI logs can see real contention —
# previously the fail-open path was entirely silent (P2.8).
wal_run_locked() {
  if wal_lock_acquire; then
    "$@"
    local rc=$?
    wal_lock_release
    return "$rc"
  fi
  if [ "${WAL_LOCK_STRICT:-0}" = "1" ]; then
    echo "wal-lock: acquire failed (strict), refusing to run unlocked" >&2
    return 75
  fi
  echo "wal-lock: acquire failed, running without lock (race window open)" >&2
  "$@"
}

# wal_append <line> [dedup_key]
# Append a pre-formatted line to $WAL_FILE (or $MEMORY_DIR/.wal) under the WAL lock.
# If dedup_key is non-empty and a line containing that exact key already exists
# in the WAL for today, the write is skipped. dedup_key should be a unique
# substring of the line (typically "event|slug|session").
wal_append() {
  local line="$1" dedup_key="${2:-}"
  local wal="${WAL_FILE:-${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}/.wal}"
  [ -z "$line" ] && return 0
  wal_run_locked bash -c '
    line=$1; key=$2; wal=$3
    if [ -n "$key" ] && [ -f "$wal" ] && grep -qF "$key" "$wal" 2>/dev/null; then
      exit 0
    fi
    printf "%s\n" "$line" >> "$wal"
  ' _ "$line" "$dedup_key" "$wal" 2>/dev/null || true
}
