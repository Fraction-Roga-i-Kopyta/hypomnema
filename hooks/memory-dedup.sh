#!/bin/bash
# PreToolUse hook — fuzzy dedup for mistakes (blocks duplicate creation)
# Matcher: Write
# Exit 2 = block write (high similarity duplicate found)
# Requires: memoryctl on PATH (graceful: exit 0 if unavailable)

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

# Dedup backend: memoryctl. Try $PATH first; fall back to the canonical
# install location because `~/.claude/bin` is usually not on a fresh-install
# user's PATH by default (external review 2026-04-22 — user hit 255/258
# until they manually added it to their shell rc). No memoryctl → dedup
# disabled silently.
MEMORYCTL=$(command -v memoryctl 2>/dev/null || echo "$HOME/.claude/bin/memoryctl")
[ -x "$MEMORYCTL" ] || exit 0

CONTENT=$(printf '%s\n' "$INPUT" | jq -r '.tool_input.content // empty' 2>/dev/null)
[ -z "$CONTENT" ] && exit 0

SESSION_ID=$(printf '%s\n' "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)
# R17: session_id propagates into the WAL via memoryctl. If it contains
# `|` or a newline, downstream awk -F'|' splitters see extra fields or
# spurious records. Mirror the sanitization used in session-start.sh /
# memory-outcome.sh. (audit-2026-04-16 R17)
SAFE_SID="${SESSION_ID//|/_}"
SAFE_SID="${SAFE_SID//$'\n'/_}"
SAFE_SID="${SAFE_SID//$'\r'/_}"
export HYPOMNEMA_SESSION_ID="${SAFE_SID:-unknown}"
export CLAUDE_MEMORY_DIR="$MEMORY_DIR"

OUTPUT=$(printf '%s\n' "$CONTENT" | "$MEMORYCTL" dedup check "$FILE_PATH" 2>&1)
EXIT_CODE=$?

if [ "$EXIT_CODE" -eq 2 ]; then
  echo "$OUTPUT" >&2
  exit 2
fi

[ -n "$OUTPUT" ] && echo "$OUTPUT"
exit 0
