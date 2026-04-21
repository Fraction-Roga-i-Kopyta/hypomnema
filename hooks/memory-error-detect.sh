#!/bin/bash
# PostToolUse hook — detect error patterns in Bash output (exit 0 only)
# Catches: warnings, deprecation notices, stderr on successful commands
# Matcher: Bash
# Output: hint to Claude context if known pattern detected
# Graceful: any error → exit 0

set -o pipefail

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
PATTERNS_FILE="$MEMORY_DIR/.error-patterns"
WAL_FILE="$MEMORY_DIR/.wal"

# shellcheck source=lib/wal-lock.sh
. "$(dirname "$0")/lib/wal-lock.sh" 2>/dev/null || true

INPUT=$(cat)

# Only process Bash tool calls
TOOL_NAME=$(printf '%s\n' "$INPUT" | jq -r '.tool_name // empty' 2>/dev/null)
[ "$TOOL_NAME" = "Bash" ] || exit 0

# Extract stdout + stderr from tool_response
STDOUT=$(printf '%s\n' "$INPUT" | jq -r '.tool_response.stdout // empty' 2>/dev/null)
STDERR=$(printf '%s\n' "$INPUT" | jq -r '.tool_response.stderr // empty' 2>/dev/null)
RESPONSE="${STDOUT}
${STDERR}"
[ -z "$STDOUT" ] && [ -z "$STDERR" ] && exit 0

# Truncate large outputs to 50 KB — protects awk heredoc from megabyte blasts.
if [ "${#RESPONSE}" -gt 50000 ]; then
  RESPONSE="${RESPONSE:0:50000}"
fi

# Need patterns file
[ -f "$PATTERNS_FILE" ] || exit 0

# Session dedup: one hint per category per session
SESSION_ID=$(printf '%s\n' "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)
SAFE_SESSION_ID="${SESSION_ID//|/_}"
SAFE_SESSION_ID="${SAFE_SESSION_ID//\//_}"
TODAY="${HYPOMNEMA_TODAY:-$(date +%Y-%m-%d)}"

# Single-pass awk: match patterns against response text
# Returns first match only (lowest noise)
MATCH=$(awk -F'|' '
  NR == FNR {
    # First file: load response text (stdin via -)
    resp = resp $0 "\n"
    next
  }
  /^#/ || /^[[:space:]]*$/ { next }
  {
    pat = $1; cat = $2; hint = $3
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", pat)
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", cat)
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", hint)
    if (pat != "" && index(resp, pat) > 0) {
      printf "%s\t%s\t%s\n", pat, cat, hint
      exit
    }
  }
' - "$PATTERNS_FILE" <<< "$RESPONSE" 2>/dev/null)

[ -z "$MATCH" ] && exit 0

PAT=$(printf '%s\n' "$MATCH" | cut -f1)
CAT=$(printf '%s\n' "$MATCH" | cut -f2)
HINT=$(printf '%s\n' "$MATCH" | cut -f3)

# Session dedup: skip if this category already detected in this session
if [ -f "$WAL_FILE" ] && [ -n "$SAFE_SESSION_ID" ]; then
  ALREADY=$(awk -F'|' -v sid="$SAFE_SESSION_ID" -v cat="$CAT" '
    $4 == sid && $2 == "error-detect" && $3 == cat { found=1; exit }
    END { print found+0 }
  ' "$WAL_FILE" 2>/dev/null)
  [ "$ALREADY" = "1" ] && exit 0
fi

# Log detection to WAL
wal_append "$TODAY|error-detect|$CAT|$SAFE_SESSION_ID" "error-detect|$CAT|$SAFE_SESSION_ID"

# Cross-reference: check if similar mistake already exists
SIMILAR=""
if [ -d "$MEMORY_DIR/mistakes" ]; then
  SIMILAR_FILE=$(grep -rli "$CAT" "$MEMORY_DIR/mistakes/"*.md 2>/dev/null | head -1)
  if [ -z "$SIMILAR_FILE" ]; then
    SIMILAR_FILE=$(grep -rli "$PAT" "$MEMORY_DIR/mistakes/"*.md 2>/dev/null | head -1)
  fi
  [ -n "$SIMILAR_FILE" ] && SIMILAR=" → см. mistakes/$(basename "$SIMILAR_FILE" .md)"
fi

# Output hint to Claude context
printf '⚠ Паттерн: %s — %s%s\n' "$PAT" "$HINT" "$SIMILAR"

exit 0
