#!/bin/bash
# WAL compaction: keep last 14 days raw, aggregate older entries
MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
WAL_FILE="$MEMORY_DIR/.wal"
[ -f "$WAL_FILE" ] || exit 0

LINE_COUNT=$(wc -l < "$WAL_FILE" 2>/dev/null)
[ "${LINE_COUNT:-0}" -le 1200 ] && exit 0

if [[ "$OSTYPE" == darwin* ]]; then
  CUTOFF=$(date -v-14d +%Y-%m-%d)
else
  CUTOFF=$(date -d "14 days ago" +%Y-%m-%d)
fi

# Single-pass: split into recent (raw) and old (aggregated)
# Old inject entries -> aggregated counts; old non-inject -> preserved as-is
awk -F'|' -v cutoff="$CUTOFF" '
  $1 >= cutoff { recent[++rn] = $0; next }
  $2 == "inject" { agg[$1 "|" $3]++; next }
  $2 == "inject-agg" { agg[$1 "|" $3] += $4; next }
  { old_other[++on] = $0 }
  END {
    # Print aggregated old injects (sorted by date)
    for (key in agg) {
      split(key, k, "|")
      printf "%s|inject-agg|%s|%d\n", k[1], k[2], agg[key]
    }
    # Print preserved old non-inject entries
    for (i = 1; i <= on; i++) print old_other[i]
    # Print recent entries (already in chronological order)
    for (i = 1; i <= rn; i++) print recent[i]
  }
' "$WAL_FILE" | sort -t'|' -k1,1 > "$WAL_FILE.tmp" 2>/dev/null

mv "$WAL_FILE.tmp" "$WAL_FILE"
exit 0
