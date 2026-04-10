#!/bin/bash
# PostToolUse hook — detect negative outcomes (mistake repeated despite warning)
# Triggered on Write|Edit to memory/mistakes/*.md
# Graceful: any error → exit 0

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
WAL_FILE="$MEMORY_DIR/.wal"

INPUT=$(cat)
FILE_PATH=$(printf '%s\n' "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)
[ -z "$FILE_PATH" ] && exit 0

# Only process writes to mistakes/*.md
case "$FILE_PATH" in
  */mistakes/*.md) ;;
  *) exit 0 ;;
esac

SESSION_ID=$(printf '%s\n' "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)
[ -z "$SESSION_ID" ] && exit 0
SAFE_SESSION_ID="${SESSION_ID//|/_}"
SAFE_SESSION_ID="${SAFE_SESSION_ID//\//_}"

FILENAME=$(basename "$FILE_PATH" .md)
TODAY=$(date +%Y-%m-%d)

# Check WAL: was this specific mistake injected in current session?
INJECTED_IN_SESSION=""
if [ -f "$WAL_FILE" ]; then
  INJECTED_IN_SESSION=$(awk -F'|' -v sid="$SAFE_SESSION_ID" -v fname="$FILENAME" '
    $4 == sid && $2 == "inject" && $3 == fname { found=1; exit }
    END { print found+0 }
  ' "$WAL_FILE")
fi

if [ "$INJECTED_IN_SESSION" = "1" ]; then
  # Negative outcome: mistake was injected but Claude is writing/updating it anyway
  printf '%s|outcome-negative|%s|%s\n' "$TODAY" "$FILENAME" "$SAFE_SESSION_ID" >> "$WAL_FILE" 2>/dev/null
else
  # New mistake, not a repeat of injected one
  printf '%s|outcome-new|%s|%s\n' "$TODAY" "$FILENAME" "$SAFE_SESSION_ID" >> "$WAL_FILE" 2>/dev/null
fi

exit 0
