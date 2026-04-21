#!/bin/bash
# PostToolUse hook — detect negative outcomes + cascade signals (v0.4)
# Triggered on Write|Edit to memory/**/*.md
# Graceful: any error → exit 0

set -o pipefail

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
WAL_FILE="$MEMORY_DIR/.wal"

# shellcheck source=lib/wal-lock.sh
. "$(dirname "$0")/lib/wal-lock.sh" 2>/dev/null || true

INPUT=$(cat)
FILE_PATH=$(printf '%s\n' "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)
[ -z "$FILE_PATH" ] && exit 0

# Only process writes to memory/**/*.md
case "$FILE_PATH" in
  */memory/*.md|*/memory/*/*.md) ;;
  *) exit 0 ;;
esac

SESSION_ID=$(printf '%s\n' "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)
[ -z "$SESSION_ID" ] && exit 0
SAFE_SESSION_ID="${SESSION_ID//|/_}"
SAFE_SESSION_ID="${SAFE_SESSION_ID//\//_}"

FILENAME=$(basename "$FILE_PATH" .md)
TODAY="${HYPOMNEMA_TODAY:-$(date +%Y-%m-%d)}"

# --- Negative outcome detection (mistakes only) ---
case "$FILE_PATH" in
  */mistakes/*.md)
    INJECTED_IN_SESSION=""
    if [ -f "$WAL_FILE" ]; then
      INJECTED_IN_SESSION=$(awk -F'|' -v sid="$SAFE_SESSION_ID" -v fname="$FILENAME" '
        $4 == sid && $2 == "inject" && $3 == fname { found=1; exit }
        END { print found+0 }
      ' "$WAL_FILE")
    fi

    if [ "$INJECTED_IN_SESSION" = "1" ]; then
      wal_append "$TODAY|outcome-negative|$FILENAME|$SAFE_SESSION_ID" "outcome-negative|$FILENAME|$SAFE_SESSION_ID"
    else
      wal_append "$TODAY|outcome-new|$FILENAME|$SAFE_SESSION_ID" "outcome-new|$FILENAME|$SAFE_SESSION_ID"
    fi
    ;;
esac

# --- Cascade detection (v0.4) ---
# When a file is updated, check if other files have instance_of pointing to it
for dir in mistakes feedback strategies knowledge notes decisions; do
  [ -d "$MEMORY_DIR/$dir" ] || continue
  find "$MEMORY_DIR/$dir" -name "*.md" -type f 2>/dev/null | while IFS= read -r candidate; do
    [ "$candidate" = "$FILE_PATH" ] && continue
    # Quick grep: skip files without instance_of
    grep -q "instance_of" "$candidate" 2>/dev/null || continue
    # Parse related field for instance_of links to this file
    awk -v target="$FILENAME" '
      /^---$/ { fm++; if (fm >= 2) exit; next }
      fm == 1 && /^related:/ { in_rel = 1; next }
      fm == 1 && in_rel && /^  - / {
        line = $0; sub(/^  - /, "", line)
        # Parse "target-name: instance_of"
        split(line, parts, ": ")
        if (parts[1] == target && parts[2] == "instance_of") { found = 1; exit }
        next
      }
      fm == 1 && in_rel && !/^  - / { in_rel = 0 }
      END { exit !found }
    ' "$candidate" 2>/dev/null && {
      child_slug=$(basename "$candidate" .md)
      wal_append "$TODAY|cascade-review|$child_slug|parent:$FILENAME" "cascade-review|$child_slug|parent:$FILENAME"
    }
  done
done

exit 0
