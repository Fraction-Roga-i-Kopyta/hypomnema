#!/bin/bash
# Finds consolidation candidates in mistakes/
set -o pipefail
MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"

echo "=== Consolidation Candidates ==="
echo ""

for f in "$MEMORY_DIR"/mistakes/*.md; do
  [ -f "$f" ] || continue
  prevention=$(awk '/^---$/{n++} n==1 && /^prevention:/{sub(/^prevention: */,""); print; exit}' "$f")
  printf '%s\t%s\n' "$(basename "$f" .md)" "$prevention"
done | sort -t$'\t' -k2 | awk -F$'\t' '
  prev != "" && prev == $2 { printf "  DUPLICATE: %s + %s\n  Prevention: %s\n\n", prev_name, $1, $2 }
  { prev = $2; prev_name = $1 }
'

echo "=== Stale Candidates (recurrence:0, >60 days) ==="
echo ""
NOW=$(date +%s)
for f in "$MEMORY_DIR"/mistakes/*.md; do
  [ -f "$f" ] || continue
  rec=$(awk '/^---$/{n++} n==1 && /^recurrence:/{sub(/^recurrence: */,""); print; exit}' "$f")
  created=$(awk '/^---$/{n++} n==1 && /^created:/{sub(/^created: */,""); print; exit}' "$f")
  [ "$rec" != "0" ] && continue
  [ -z "$created" ] && continue
  if [[ "$OSTYPE" == darwin* ]]; then
    created_sec=$(date -j -f "%Y-%m-%d" "$created" +%s 2>/dev/null) || continue
  else
    created_sec=$(date -d "$created" +%s 2>/dev/null) || continue
  fi
  age=$(( (NOW - created_sec) / 86400 ))
  [ "$age" -gt 60 ] && echo "  $(basename "$f" .md) — recurrence:0, age: ${age}d"
done

echo ""
echo "Run: claude 'консолидируй mistakes' for manual consolidation"
