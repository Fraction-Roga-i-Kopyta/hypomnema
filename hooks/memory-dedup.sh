#!/bin/bash
# PostToolUse hook — fuzzy dedup for mistakes
# Requires: python3 + rapidfuzz (opt-in). Graceful: exit 0 if unavailable.

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"

INPUT=$(cat)
FILE_PATH=$(printf '%s\n' "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)
[ -z "$FILE_PATH" ] && exit 0

case "$FILE_PATH" in
  */mistakes/*.md) ;;
  *) exit 0 ;;
esac

[ -f "$FILE_PATH" ] || exit 0
command -v python3 >/dev/null 2>&1 || exit 0
python3 -c "import rapidfuzz" 2>/dev/null || exit 0

SESSION_ID=$(printf '%s\n' "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)
export HYPOMNEMA_SESSION_ID="${SESSION_ID:-unknown}"
export CLAUDE_MEMORY_DIR="$MEMORY_DIR"

DEDUP_SCRIPT="$HOME/.claude/bin/memory-dedup.py"
[ -f "$DEDUP_SCRIPT" ] || exit 0

OUTPUT=$(python3 "$DEDUP_SCRIPT" "$FILE_PATH" 2>/dev/null)
[ -n "$OUTPUT" ] && echo "$OUTPUT"

exit 0
