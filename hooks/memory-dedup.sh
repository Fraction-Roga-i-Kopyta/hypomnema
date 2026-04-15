#!/bin/bash
# PreToolUse hook — fuzzy dedup for mistakes (blocks duplicate creation)
# Matcher: Write
# Exit 2 = block write (high similarity duplicate found)
# Requires: uv + python3 (graceful: exit 0 if unavailable)

set -o pipefail

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"

INPUT=$(cat)
FILE_PATH=$(printf '%s\n' "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)
[ -z "$FILE_PATH" ] && exit 0

# Only check writes to mistakes/*.md
case "$FILE_PATH" in
  */mistakes/*.md) ;;
  *) exit 0 ;;
esac

# Skip if file already exists (it's an overwrite/update, not a new duplicate)
[ -f "$FILE_PATH" ] && exit 0

# Check dependencies (uv handles rapidfuzz automatically)
command -v uv >/dev/null 2>&1 || exit 0

# Extract content from tool_input
CONTENT=$(printf '%s\n' "$INPUT" | jq -r '.tool_input.content // empty' 2>/dev/null)
[ -z "$CONTENT" ] && exit 0

SESSION_ID=$(printf '%s\n' "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)
export HYPOMNEMA_SESSION_ID="${SESSION_ID:-unknown}"
export CLAUDE_MEMORY_DIR="$MEMORY_DIR"

DEDUP_SCRIPT="$HOME/.claude/bin/memory-dedup.py"
[ -f "$DEDUP_SCRIPT" ] || exit 0

# Pass content via stdin, file_path as argument
OUTPUT=$(printf '%s\n' "$CONTENT" | uv run --with rapidfuzz python3 "$DEDUP_SCRIPT" "$FILE_PATH" 2>&1)
EXIT_CODE=$?

if [ $EXIT_CODE -eq 2 ]; then
  # Block: high-similarity duplicate — stderr message propagates to Claude
  echo "$OUTPUT" >&2
  exit 2
fi

# Warn: medium similarity — stdout hint to Claude context
[ -n "$OUTPUT" ] && echo "$OUTPUT"
exit 0
