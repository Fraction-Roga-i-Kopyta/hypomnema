#!/bin/bash
# PreCompact hook — remind Claude to checkpoint unsaved insights before context compression
# Cannot block compaction — observability only
# Output: additionalContext with reminder (stronger if nothing saved this session)
# Graceful: any error → exit 0

set -o pipefail

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"

INPUT=$(cat)
SESSION_ID=$(printf '%s\n' "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)
[ -z "$SESSION_ID" ] && exit 0

# Check if any memory files were created/modified during this session
# Uses session marker file (created by SessionStart) as timestamp reference
SAFE_MARKER_ID="${SESSION_ID//\//_}"
MARKER="/tmp/.claude-session-${SAFE_MARKER_ID}"

SAVED=0
if [ -f "$MARKER" ]; then
  SAVED=$(find "$MEMORY_DIR" -name "*.md" -newer "$MARKER" \
    -not -path "*/archive/*" \
    -not -name "_agent_context.md" \
    -not -path "*/continuity/*" \
    2>/dev/null | wc -l | tr -d ' ')
fi

MSG="⚠ Context compression. If you have unrecorded insights, mistakes, or decisions — save them to memory/ now."
if [ "${SAVED:-0}" -eq 0 ]; then
  MSG="${MSG}
Nothing was saved to memory/ this session. Consider: any errors, decisions, patterns, or strategies worth persisting?
If the task was solved first try — record the approach in strategies/ (trigger, steps, outcome)."
fi

# PreCompact does not support hookSpecificOutput.additionalContext (schema
# allows it only for PreToolUse/UserPromptSubmit/PostToolUse/SessionStart/Stop).
# Use top-level `systemMessage` instead — surfaces the reminder to the user
# before compaction runs.
cat <<EOF
{"systemMessage":$(printf '%s\n' "$MSG" | jq -Rs .)}
EOF

exit 0
