#!/bin/bash
# Stop hook — remind Claude to record if work happened but nothing was saved
# Dependencies: jq, find, stat
# Graceful degradation: any error → exit 0

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
MIN_SESSION_SECONDS=120  # 2 minutes

INPUT=$(cat)
SESSION_ID=$(printf '%s\n' "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)

[ -z "$SESSION_ID" ] && exit 0

MARKER="/tmp/.claude-session-${SESSION_ID}"
[ -f "$MARKER" ] || exit 0

# Check session duration
if [[ "$OSTYPE" == darwin* ]]; then
  MARKER_TIME=$(stat -f %m "$MARKER" 2>/dev/null || echo 0)
else
  MARKER_TIME=$(stat -c %Y "$MARKER" 2>/dev/null || echo 0)
fi
NOW=$(date +%s)
ELAPSED=$((NOW - MARKER_TIME))

# Short session — skip
if [ "$ELAPSED" -lt "$MIN_SESSION_SECONDS" ]; then
  rm -f "$MARKER" 2>/dev/null
  exit 0
fi

# --- Lifecycle rotation ---
lifecycle_rotate() {
  local ARCHIVE_DIR="$MEMORY_DIR/archive"
  local STALE_DAYS=30
  local ARCHIVE_DAYS=90
  local NOW_SEC=$(date +%s)

  for f in "$MEMORY_DIR"/mistakes/*.md "$MEMORY_DIR"/feedback/*.md \
           "$MEMORY_DIR"/knowledge/*.md "$MEMORY_DIR"/notes/*.md \
           "$MEMORY_DIR"/projects/*.md "$MEMORY_DIR"/strategies/*.md \
           "$MEMORY_DIR"/journal/*.md; do
    [ -f "$f" ] || continue

    local file_status=$(awk '/^---$/{n++} n==1 && /^status:/{sub(/^status: */,""); print; exit}' "$f")
    [ "$file_status" = "pinned" ] && continue
    [ "$file_status" = "archived" ] && continue

    local ref_date=$(awk '/^---$/{n++} n==1 && /^referenced:/{sub(/^referenced: */,""); print; exit}' "$f")
    [ -z "$ref_date" ] && ref_date=$(awk '/^---$/{n++} n==1 && /^created:/{sub(/^created: */,""); print; exit}' "$f")
    [ -z "$ref_date" ] && continue

    local ref_sec
    if [[ "$OSTYPE" == darwin* ]]; then
      ref_sec=$(date -j -f "%Y-%m-%d" "$ref_date" +%s 2>/dev/null) || continue
    else
      ref_sec=$(date -d "$ref_date" +%s 2>/dev/null) || continue
    fi
    local age=$(( (NOW_SEC - ref_sec) / 86400 ))

    if [ "$file_status" = "stale" ] && [ "$age" -gt "$ARCHIVE_DAYS" ]; then
      local rel="${f#$MEMORY_DIR/}"
      mkdir -p "$ARCHIVE_DIR/$(dirname "$rel")"
      mv "$f" "$ARCHIVE_DIR/$rel"
      continue
    fi

    if [ "$age" -gt "$STALE_DAYS" ] && [ "$file_status" = "active" ]; then
      sed -i.bak "s/^status: active/status: stale/" "$f" 2>/dev/null && rm -f "$f.bak" 2>/dev/null
    fi
  done
}

lifecycle_rotate 2>/dev/null || true

# Check if any memory files were modified during this session
MODIFIED=$(find "$MEMORY_DIR" -name "*.md" -newer "$MARKER" -not -name "MEMORY.md" -not -name "_agent_context.md" 2>/dev/null | head -1)

if [ -z "$MODIFIED" ]; then
  # Work happened but nothing was recorded — remind
  REMINDER="[MEMORY CHECKPOINT] Session lasted >2 min but nothing was recorded.
If there's anything to save — mistakes, decisions, journal — do it now."

  cat <<EOF
{"hookSpecificOutput":{"hookEventName":"Stop","additionalContext":$(printf '%s\n' "$REMINDER" | jq -Rs .)}}
EOF
fi

# Clean up marker
rm -f "$MARKER" 2>/dev/null

exit 0
