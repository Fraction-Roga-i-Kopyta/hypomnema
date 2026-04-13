#!/bin/bash
# Update strategies/*.md success_count from WAL strategy-used events.
# Idempotent: recounts from scratch each run, writes only on change.
# Invoked from memory-stop.sh (background).

set -o pipefail

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
WAL_FILE="$MEMORY_DIR/.wal"
STRAT_DIR="$MEMORY_DIR/strategies"

[ -f "$WAL_FILE" ] || exit 0
[ -d "$STRAT_DIR" ] || exit 0

# slug -> count
COUNTS=$(awk -F'|' '$2 == "strategy-used" && $3 != "" { print $3 }' "$WAL_FILE" 2>/dev/null | sort | uniq -c)

[ -z "$COUNTS" ] && exit 0

updated=0
while read -r count slug; do
  [ -z "$slug" ] && continue
  file="$STRAT_DIR/${slug}.md"
  [ -f "$file" ] || continue

  current=$(awk '/^---$/{fm++; if(fm==2) exit} fm==1 && /^success_count:/{sub(/^success_count: */,""); print; exit}' "$file" 2>/dev/null)
  current="${current:-0}"

  [ "$current" = "$count" ] && continue

  if grep -q "^success_count:" "$file"; then
    perl -pi -e '
      $in_fm = 1 if /^---$/ && !$seen_fm++;
      $in_fm = 0 if /^---$/ && $seen_fm > 1;
      s/^success_count:.*/success_count: '"$count"'/ if $in_fm;
      if (eof) { $seen_fm = 0; $in_fm = 0; }
    ' "$file" 2>/dev/null
  else
    awk -v c="$count" '
      /^---$/ { fm++; if (fm==2) { print "success_count: " c; print; next } }
      { print }
    ' "$file" > "$file.tmp" && mv "$file.tmp" "$file"
  fi
  updated=$((updated + 1))
done <<< "$COUNTS"

if [ "$updated" -gt 0 ] && [ -f "$(dirname "$0")/../hooks/lib/wal-lock.sh" ]; then
  . "$(dirname "$0")/../hooks/lib/wal-lock.sh" 2>/dev/null || true
  wal_append "$(date +%Y-%m-%d)|strategy-rescored|updated_${updated}|" 2>/dev/null || true
fi

exit 0
