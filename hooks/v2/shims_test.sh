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
echo "PASS"
