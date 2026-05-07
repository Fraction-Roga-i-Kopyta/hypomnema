#!/bin/bash
# wal-retro-silent.sh — retroactively classify orphan `inject` events
# as `trigger-silent-retro`. Closes the measurement gap from sessions
# that crash, run under MIN_SESSION_SECONDS, or arrive without a
# transcript — `memoryctl doctor`'s open_quanta saw 74% pre-retro.
set -o pipefail
export LC_ALL=C

# Walks the WAL once per Stop hook, identifies sessions with raw
# `inject` events older than 24 h and no closing event of any kind,
# and emits one `trigger-silent-retro|<slug>|<session>` per inject.
# Idempotent — we check for an existing retro before emitting.
# Runs under the same lock as `wal-compact.sh`; bails silently on
# lock timeout (next Stop hook retries).

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
WAL_FILE="$MEMORY_DIR/.wal"
[ -f "$WAL_FILE" ] || exit 0

# shellcheck source=lib/wal-lock.sh
. "$(dirname "$0")/lib/wal-lock.sh" 2>/dev/null || true

# CUTOFF — the age beyond which an orphan inject is fair game for
# retroactive classification. 24 h matches MIN_SESSION_SECONDS's
# time-scale (a session that didn't close within a day is
# overwhelmingly likely to never close).
if [[ "$OSTYPE" == darwin* ]]; then
  CUTOFF=$(date -v-1d +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
else
  CUTOFF=$(date -d "1 day ago" +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
fi

TODAY="${HYPOMNEMA_TODAY:-$(date +%Y-%m-%d)}"

wal_lock_acquire 2>/dev/null || exit 0
trap 'wal_lock_release 2>/dev/null' EXIT INT TERM

# Two-pass awk:
#   pass 1 — populate the `closed[session]` set (any posterior event)
#            and the `retro[session|slug]` set (already emitted retros).
#   pass 2 — for each raw `inject` event older than cutoff, if its
#            session has no closing event AND we haven't already
#            emitted a retro for this (session, slug), print the new
#            `trigger-silent-retro` line.
#
# The emission date is TODAY (when we retroactively classified), not
# the original inject's date. Preserves the four-column invariant
# and keeps retros separable from natural trigger-silent events in
# downstream analysis.
awk -F'|' -v cutoff="$CUTOFF" -v today="$TODAY" '
  # Closing event set (v0.14 soft-close semantics — docs/FORMAT.md §
  # Closing events). A session is considered closed for retro purposes
  # if ANY of these events appear against it, not just clean-session.
  #
  # The narrow pre-v0.14 set only counted terminal signals
  # (clean-session and the feedback-loop verdicts), so any session
  # that produced session-metrics (Stop hook completed, maybe with
  # errors) or outcome-new (fresh mistake captured) was re-flagged as
  # silent after 24 h even though Stop demonstrably ran. The new
  # session-close event is the canonical Stop-hook-completed signal
  # (see session-stop.sh). Format v2 of session-metrics carries
  # session_id in $4 too, but mixing v1 and v2 formats here would
  # require per-line detection — session-close is the cleaner
  # universal signal for both v1 and v2 WALs.
  #
  # Guard NF == 4: the WAL is canonical 4-column; anything else came
  # from a crashed writer or a user-pasted artefact and must not
  # poison the closing-set.
  FNR == NR {
    if (NF != 4) next
    if ($2 == "trigger-useful" || $2 == "trigger-silent" ||
        $2 == "outcome-positive" || $2 == "outcome-negative" ||
        $2 == "clean-session" || $2 == "trigger-silent-retro" ||
        $2 == "session-close" || $2 == "outcome-new") {
      closed[$4] = 1
    }
    if ($2 == "trigger-silent-retro") retro[$4 "|" $3] = 1
    next
  }
  NF != 4 { next }
  $2 == "inject" && $1 < cutoff {
    if ($4 in closed) next
    key = $4 "|" $3
    if (key in retro) next
    retro[key] = 1
    printf "%s|trigger-silent-retro|%s|%s\n", today, $3, $4
  }
' "$WAL_FILE" "$WAL_FILE" >> "$WAL_FILE"

exit 0
