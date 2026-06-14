#!/usr/bin/env bash
set -u
MCTL="${1:?usage: shims_test.sh <memoryctl-path>}"
DIR="$(cd "$(dirname "$0")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/.claude/memory" "$TMP/.claude/projects/-tmp-proj/memory"
printf '%s' '---
name: Docker cache
type: mistake
---
docker cache layer
' > "$TMP/.claude/projects/-tmp-proj/memory/docker.md"
: > "$TMP/.claude/memory/.wal"

out=$(printf '%s' '{"session_id":"s1","cwd":"/tmp/proj","prompt":"docker"}' \
  | HYPOMNEMA_MEMORYCTL="$MCTL" CLAUDE_HOME="$TMP/.claude" CLAUDE_MEMORY_DIR="$TMP/.claude/memory" \
    bash "$DIR/session-start.sh")
echo "$out" | grep -q 'hookSpecificOutput' || { echo "FAIL: session-start.sh no envelope: $out"; exit 1; }

printf '%s' '{"session_id":"s1","cwd":"/tmp"}' \
  | HYPOMNEMA_MEMORYCTL=/nonexistent/memoryctl bash "$DIR/session-start.sh"
[ $? -eq 0 ] || { echo "FAIL: missing binary must exit 0"; exit 1; }

# skill-learnings-inject: with binary present, emits envelope; missing binary exits 0
mkdir -p "$TMP/.claude/memory-global" "$TMP/.claude/memory"
: > "$TMP/.claude/memory/.wal"
cat > "$TMP/.claude/memory-global/commit-learning.md" <<'EOF'
---
type: skill-learning
name: commit-learning
description: learning for commit
skill: commit
status: active
created: 2026-06-14
---

commit body
EOF
out=$(printf '%s' '{"session_id":"s1","tool_input":{"skill":"commit"}}' \
  | HYPOMNEMA_MEMORYCTL="$MCTL" CLAUDE_HOME="$TMP/.claude" CLAUDE_MEMORY_DIR="$TMP/.claude/memory" \
    bash "$DIR/skill-learnings-inject.sh")
echo "$out" | grep -q 'hookSpecificOutput' || { echo "FAIL: skill-learnings-inject no envelope: $out"; exit 1; }

printf '%s' '{"session_id":"s1","tool_input":{"skill":"commit"}}' \
  | HYPOMNEMA_MEMORYCTL=/nonexistent/memoryctl bash "$DIR/skill-learnings-inject.sh"
[ $? -eq 0 ] || { echo "FAIL: skill-learnings-inject missing binary must exit 0"; exit 1; }

# skill-active: with binary present, writes marker; missing binary exits 0
printf '%s' '{"session_id":"s1","tool_input":{"skill":"commit"}}' \
  | HYPOMNEMA_MEMORYCTL="$MCTL" CLAUDE_HOME="$TMP/.claude" CLAUDE_MEMORY_DIR="$TMP/.claude/memory" \
    bash "$DIR/skill-active.sh"
[ $? -eq 0 ] || { echo "FAIL: skill-active.sh must exit 0"; exit 1; }
[ -f "$TMP/.claude/memory/.runtime/active-skill-s1" ] \
  || { echo "FAIL: skill-active.sh marker not written"; exit 1; }

printf '%s' '{"session_id":"s1","tool_input":{"skill":"commit"}}' \
  | HYPOMNEMA_MEMORYCTL=/nonexistent/memoryctl bash "$DIR/skill-active.sh"
[ $? -eq 0 ] || { echo "FAIL: skill-active.sh missing binary must exit 0"; exit 1; }

echo "PASS"
