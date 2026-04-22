#!/bin/bash
# scripts/replay-runner.sh — batch-replay a corpus of prompts through
# UserPromptSubmit, produce per-prompt and aggregate metrics for dogfood.
#
# Usage:
#   scripts/replay-runner.sh [CORPUS] [--memory DIR] [--tsv OUT]
#
# CORPUS defaults to scripts/synthetic-corpus.txt.
# --memory DIR uses an existing memory tree (defaults to a throwaway
#   fixture seeded from seeds/). Pass ~/.claude/memory to replay against
#   your real corpus.
# --tsv OUT writes per-prompt details as TSV (prompt, triggered_count,
#   shadow_miss_count). Summary always prints to stdout.
#
# Exit 0 on success even if zero prompts match anything — the point is
# measurement, not pass/fail.
set -eo pipefail

REPO=$(cd "$(dirname "$0")/.." && pwd)
CORPUS="$REPO/scripts/synthetic-corpus.txt"
MEM_DIR=""
TSV_OUT=""

while [ $# -gt 0 ]; do
  case "$1" in
    --memory) MEM_DIR="$2"; shift 2 ;;
    --tsv)    TSV_OUT="$2"; shift 2 ;;
    --*)      echo "unknown flag: $1" >&2; exit 2 ;;
    *)        CORPUS="$1"; shift ;;
  esac
done

[ -f "$CORPUS" ] || { echo "corpus not found: $CORPUS" >&2; exit 1; }

# --- Seed a throwaway memory tree if one wasn't provided ---
OWN_MEM=""
if [ -z "$MEM_DIR" ]; then
  OWN_MEM=$(mktemp -d)
  MEM_DIR="$OWN_MEM/memory"
  mkdir -p "$MEM_DIR"/{mistakes,feedback,strategies,knowledge,notes,seeds,.runtime}
  echo '{}' > "$MEM_DIR/projects.json"
  echo '{}' > "$MEM_DIR/projects-domains.json"
  # Copy seeds into mistakes/ so they become real records for the trigger
  # pipeline. Without this, the seed files live under seeds/ which is not
  # in the scored scan path by default.
  for s in "$REPO"/seeds/*.md; do
    [ "$(basename "$s")" = "README.md" ] && continue
    cp "$s" "$MEM_DIR/mistakes/"
  done
fi

HOOK="$REPO/hooks/user-prompt-submit.sh"
[ -f "$HOOK" ] || { echo "hook not found: $HOOK" >&2; exit 1; }

# Warm up the FTS5 index once before replay begins. Without this the
# first few prompts race against an empty index.db — the shadow pass
# runs detached and may not have indexed all markdown yet when the
# next prompt fires.
if command -v memoryctl >/dev/null 2>&1; then
  CLAUDE_MEMORY_DIR="$MEM_DIR" memoryctl fts sync >/dev/null 2>&1 || true
fi

# --- Per-prompt replay ---
session=$(date +%Y%m%d%H%M%S)-replay
WAL="$MEM_DIR/.wal"
: > "$WAL"

total=0
triggered_total=0
shadow_miss_total=0
prompts_with_trigger=0
prompts_with_shadow_miss=0

if [ -n "$TSV_OUT" ]; then
  printf 'prompt\ttriggered\tshadow_miss\n' > "$TSV_OUT"
fi

while IFS= read -r line; do
  case "$line" in
    ''|'#'*) continue ;;
  esac
  total=$((total + 1))

  wal_before=$(wc -l < "$WAL" 2>/dev/null | tr -d ' ')
  wal_before=${wal_before:-0}

  payload=$(jq -n --arg sid "$session-$total" --arg p "$line" \
    '{session_id: $sid, prompt: $p, cwd: "/tmp/replay"}')
  echo "$payload" | CLAUDE_MEMORY_DIR="$MEM_DIR" bash "$HOOK" >/dev/null 2>&1 || true

  # The UserPromptSubmit hook backgrounds the FTS5 shadow pass (detached
  # via `disown`). For measurement determinism we invoke the shadow pass
  # synchronously ourselves after the hook returns — same inputs, same
  # fixture, just no `&`. Without this the background process races
  # against our WAL read and the shadow-miss count is under-reported.
  injected_list=""
  runtime_list="$MEM_DIR/.runtime/injected-$session-$total.list"
  [ -f "$runtime_list" ] && injected_list=$(cat "$runtime_list")
  if command -v memoryctl >/dev/null 2>&1; then
    CLAUDE_MEMORY_DIR="$MEM_DIR" memoryctl fts shadow "$line" "$session-$total" "$injected_list" >/dev/null 2>&1 || true
  fi

  wal_after=$(wc -l < "$WAL" 2>/dev/null | tr -d ' ')
  wal_after=${wal_after:-0}

  # Only events from this prompt's session-id.
  new=$(tail -n $((wal_after - wal_before)) "$WAL" 2>/dev/null | grep -F "$session-$total" || true)
  t_count=$(printf '%s\n' "$new" | grep -c '|trigger-match|' || true)
  s_count=$(printf '%s\n' "$new" | grep -c '|shadow-miss|' || true)

  triggered_total=$((triggered_total + t_count))
  shadow_miss_total=$((shadow_miss_total + s_count))
  [ "$t_count" -gt 0 ] && prompts_with_trigger=$((prompts_with_trigger + 1))
  [ "$s_count" -gt 0 ] && prompts_with_shadow_miss=$((prompts_with_shadow_miss + 1))

  if [ -n "$TSV_OUT" ]; then
    printf '%s\t%d\t%d\n' "$line" "$t_count" "$s_count" >> "$TSV_OUT"
  fi
done < "$CORPUS"

# --- Summary ---
echo "=== replay summary ==="
echo "corpus             : $CORPUS"
echo "memory dir         : $MEM_DIR"
echo "prompts processed  : $total"
echo "trigger-match hits : $triggered_total (across $prompts_with_trigger / $total prompts)"
echo "shadow-miss events : $shadow_miss_total (across $prompts_with_shadow_miss / $total prompts)"

if [ "$total" -gt 0 ]; then
  trigger_ratio=$(awk -v a="$prompts_with_trigger" -v b="$total" 'BEGIN{printf "%.1f%%", 100*a/b}')
  shadow_ratio=$(awk -v a="$prompts_with_shadow_miss" -v b="$total" 'BEGIN{printf "%.1f%%", 100*a/b}')
  echo "prompts matching triggers : $trigger_ratio"
  echo "prompts surfacing via FTS  : $shadow_ratio"
fi

if [ -n "$TSV_OUT" ]; then
  echo "per-prompt TSV written to $TSV_OUT"
fi

if [ -n "$OWN_MEM" ]; then
  rm -rf "$OWN_MEM"
fi
