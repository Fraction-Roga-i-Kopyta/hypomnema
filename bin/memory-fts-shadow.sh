#!/bin/bash
# memory-fts-shadow.sh — FTS5 recall shadow for UserPromptSubmit (v0.8 signal).
#
# The primary UserPromptSubmit hook matches substring triggers on frontmatter.
# This shadow pass asks FTS5/BM25 over full body content what it would have
# surfaced for the same prompt, then logs every high-rank file that the
# primary pipeline did NOT inject. Those `shadow-miss` events feed roadmap
# v0.8 trigger-tuning: a file that shadow-hits repeatedly without ever
# being triggered directly is a trigger-miss candidate.
#
# Designed to run detached from user-prompt-submit.sh:
#   ( memory-fts-shadow.sh "$CLEAN_PROMPT" "$SAFE_SESSION_ID" "$NEW_INJECTED" ) \
#     >/dev/null 2>&1 &
#
# Args:
#   $1 = prompt text (already fence/blockquote/inline-code stripped)
#   $2 = safe session id (| and / already substituted)
#   $3 = newline-separated slugs the primary hook injected this prompt
#
# Fail-safe: any failure → silent exit 0. Running in background, nobody reads stderr.

set -uo pipefail

PROMPT="${1:-}"
SESSION_ID="${2:-}"
NEW_INJECTED="${3:-}"

[ -z "$PROMPT" ] && exit 0
[ -z "$SESSION_ID" ] && exit 0

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
BIN_DIR="$(cd "$(dirname "$0")" && pwd)"
TODAY=$(date +%Y-%m-%d)

# Load WAL lock lib (symlinked by install.sh into ~/.claude/hooks/lib/).
# shellcheck source=../hooks/lib/wal-lock.sh
. "$HOME/.claude/hooks/lib/wal-lock.sh" 2>/dev/null || exit 0

WAL_FILE="$MEMORY_DIR/.wal"
MAX_SHADOW=${MEMORY_FTS_SHADOW_MAX:-4}
LOOKUP_LIMIT=$((MAX_SHADOW + 12))  # headroom for dedup filtering

# --- Build "already-injected" set: SessionStart + UserPromptSubmit combined.
INJECTED_SET=" "
DEDUP_FILE="$MEMORY_DIR/.runtime/injected-${SESSION_ID}.list"
if [ -f "$DEDUP_FILE" ]; then
  while IFS= read -r _slug; do
    [ -n "$_slug" ] && INJECTED_SET="${INJECTED_SET}${_slug} "
  done < "$DEDUP_FILE"
fi
while IFS= read -r _slug; do
  [ -n "$_slug" ] && INJECTED_SET="${INJECTED_SET}${_slug} "
done <<< "$NEW_INJECTED"

# --- FTS5 shadow query + diff.
# Only consider the same dirs the primary hook scans — auto-generated files
# (self-profile.md, _agent_context.md, continuity/, projects/) are not
# trigger-match candidates and would be noise in the shadow signal.
COUNT=0
while IFS=$'\t' read -r _path _score; do
  [ -z "$_path" ] && continue
  case "$_path" in
    */mistakes/*|*/feedback/*|*/strategies/*|*/knowledge/*|*/notes/*|*/seeds/*) ;;
    *) continue ;;
  esac
  _slug="${_path##*/}"; _slug="${_slug%.md}"

  # Skip records already in the injected set.
  case "$INJECTED_SET" in *" ${_slug} "*) continue ;; esac

  # Emit shadow-miss (wal_append dedupes by the same key within the WAL).
  wal_append \
    "${TODAY}|shadow-miss|${_slug}|${SESSION_ID}" \
    "shadow-miss|${_slug}|${SESSION_ID}"

  COUNT=$((COUNT + 1))
  [ "$COUNT" -ge "$MAX_SHADOW" ] && break
done < <("$BIN_DIR/memory-fts-query.sh" "$PROMPT" "$LOOKUP_LIMIT" 2>/dev/null)

exit 0
