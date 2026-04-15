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

MSG="⚠ Context compression. Если есть незаписанные инсайты, ошибки или решения — запиши в memory/ сейчас."
if [ "${SAVED:-0}" -eq 0 ]; then
  MSG="${MSG}
В этой сессии ничего не записано в memory/. Проверь: были ли ошибки, решения, паттерны или стратегии, которые стоит сохранить?
Если задача решена с первого подхода — запиши подход в strategies/ (trigger, steps, outcome)."
fi

cat <<EOF
{"hookSpecificOutput":{"hookEventName":"PreCompact","additionalContext":$(printf '%s\n' "$MSG" | jq -Rs .)}}
EOF

exit 0
