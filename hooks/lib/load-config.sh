#!/bin/bash
# hooks/lib/load-config.sh — safe parser for ~/.claude/memory/.config.sh
#
# SECURITY: .config.sh is writable by any process with access to the memory
# directory, including Claude itself via its Write tool. We must never `source`
# this file — that would give any memory-writer arbitrary shell execution on
# the next hook invocation (audit-2026-04-16 S1, CWE-78).
#
# Instead, read line-by-line and only honour assignments whose KEY is on a
# small whitelist and whose VALUE is a non-negative integer (≤ 6 digits).
# Anything else is silently ignored.
#
# Usage:
#   . "$(dirname "$0")/lib/load-config.sh"
#   load_memory_config "$MEMORY_DIR/.config.sh"

load_memory_config() {
  local cfg="$1"
  [ -f "$cfg" ] || return 0

  local line key val
  while IFS= read -r line || [ -n "$line" ]; do
    # Strip trailing comment (anything from a `#` on)
    line="${line%%#*}"
    # Trim leading/trailing whitespace (POSIX-compatible)
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    [ -z "$line" ] && continue

    # Must look like KEY=VALUE
    case "$line" in
      *=*) : ;;
      *)   continue ;;
    esac
    key="${line%%=*}"
    val="${line#*=}"

    # Strip one layer of matching quotes from value
    case "$val" in
      \"*\") val="${val#\"}"; val="${val%\"}" ;;
      \'*\') val="${val#\'}"; val="${val%\'}" ;;
    esac

    # Value must be a non-negative integer, 1–6 digits
    case "$val" in
      ''|*[!0-9]*) continue ;;
    esac
    [ ${#val} -gt 6 ] && continue

    # Whitelist: only known tuning knobs. Explicit assignment avoids eval.
    case "$key" in
      MAX_FILES)             MAX_FILES=$val ;;
      MAX_GLOBAL_MISTAKES)   MAX_GLOBAL_MISTAKES=$val ;;
      MAX_PROJECT_MISTAKES)  MAX_PROJECT_MISTAKES=$val ;;
      MAX_SCORED_TOTAL)      MAX_SCORED_TOTAL=$val ;;
      MAX_CLUSTER)           MAX_CLUSTER=$val ;;
      MAX_TRIGGERED)         MAX_TRIGGERED=$val ;;
      CAP_FEEDBACK)          CAP_FEEDBACK=$val ;;
      CAP_KNOWLEDGE)         CAP_KNOWLEDGE=$val ;;
      CAP_STRATEGIES)        CAP_STRATEGIES=$val ;;
      CAP_NOTES)             CAP_NOTES=$val ;;
      CAP_DECISIONS)         CAP_DECISIONS=$val ;;
      MIN_SESSION_SECONDS)   MIN_SESSION_SECONDS=$val ;;
      *) : ;;
    esac
  done < "$cfg"
  return 0
}
