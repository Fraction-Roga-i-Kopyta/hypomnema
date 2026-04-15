#!/bin/bash
# WAL compaction: keep last 14 days raw, aggregate older entries
set -o pipefail
MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
WAL_FILE="$MEMORY_DIR/.wal"
[ -f "$WAL_FILE" ] || exit 0

# shellcheck source=lib/wal-lock.sh
. "$(dirname "$0")/lib/wal-lock.sh" 2>/dev/null || true

LINE_COUNT=$(wc -l < "$WAL_FILE" 2>/dev/null)
[ "${LINE_COUNT:-0}" -le 1200 ] && exit 0

# Acquire exclusive lock — compaction is read-modify-write, cannot race with writers.
# If lock unavailable within timeout, bail out (next SessionStart will retry).
wal_lock_acquire 2>/dev/null || exit 0
trap 'wal_lock_release 2>/dev/null' EXIT INT TERM

if [[ "$OSTYPE" == darwin* ]]; then
  CUTOFF=$(date -v-14d +%Y-%m-%d)
else
  CUTOFF=$(date -d "14 days ago" +%Y-%m-%d)
fi

# Single-pass: split into recent (raw) and old (aggregated)
# Old inject entries -> aggregated counts; old session-metrics -> monthly summary
# All other old events (outcome-*, clean-session, strategy-*, etc.) -> preserved as-is
awk -F'|' -v cutoff="$CUTOFF" '
  $1 >= cutoff { recent[++rn] = $0; next }
  $2 == "inject" { agg[$1 "|" $3]++; next }
  $2 == "inject-agg" { agg[$1 "|" $3] += $4; next }
  $2 == "session-metrics" {
    month = substr($1, 1, 7)
    metrics_count[month]++
    ec = $4; sub(/.*error_count:/, "", ec); sub(/,.*/, "", ec)
    metrics_errors[month] += ec + 0
    tc = $4; sub(/.*tool_calls:/, "", tc); sub(/,.*/, "", tc)
    metrics_tools[month] += tc + 0
    dur = $4; sub(/.*duration:/, "", dur); sub(/s.*/, "", dur)
    metrics_duration[month] += dur + 0
    next
  }
  { old_other[++on] = $0 }
  END {
    for (key in agg) {
      split(key, k, "|")
      printf "%s|inject-agg|%s|%d\n", k[1], k[2], agg[key]
    }
    for (month in metrics_count) {
      n = metrics_count[month]
      avg_err = metrics_errors[month] / n
      avg_tools = metrics_tools[month] / n
      total_dur = metrics_duration[month]
      printf "%s-01|metrics-agg|sessions:%d,avg_errors:%.1f,avg_tools:%.1f,total_duration:%ds\n", \
        month, n, avg_err, avg_tools, total_dur
    }
    for (i = 1; i <= on; i++) print old_other[i]
    for (i = 1; i <= rn; i++) print recent[i]
  }
' "$WAL_FILE" | sort -t'|' -k1,1 > "$WAL_FILE.tmp" 2>/dev/null

mv "$WAL_FILE.tmp" "$WAL_FILE"
exit 0
