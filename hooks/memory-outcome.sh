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

FILENAME=$(basename "$FILE_PATH" .md)
TODAY=$(date +%Y-%m-%d)

# Check WAL: was this specific mistake injected in current session?
if [ -f "$WAL_FILE" ] && grep -q "${SESSION_ID}" "$WAL_FILE" 2>/dev/null; then
  if grep "$SESSION_ID" "$WAL_FILE" | grep -q "inject.*${FILENAME}"; then
    # Negative outcome: mistake was injected but Claude is writing/updating it anyway
    printf '%s|outcome-negative|%s|%s\n' "$TODAY" "$FILENAME" "$SESSION_ID" >> "$WAL_FILE" 2>/dev/null
  else
    # New mistake, not a repeat of injected one
    printf '%s|outcome-new|%s|%s\n' "$TODAY" "$FILENAME" "$SESSION_ID" >> "$WAL_FILE" 2>/dev/null
  fi
else
  # Session had no injections (or no WAL) — new mistake
  printf '%s|outcome-new|%s|%s\n' "$TODAY" "$FILENAME" "$SESSION_ID" >> "$WAL_FILE" 2>/dev/null
fi

exit 0
