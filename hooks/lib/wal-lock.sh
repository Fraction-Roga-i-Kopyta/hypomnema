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

# Run command under lock; if lock unavailable, runs anyway (better than dropping writes).
wal_run_locked() {
  if wal_lock_acquire; then
    "$@"
    local rc=$?
    wal_lock_release
    return "$rc"
  fi
  "$@"
}
