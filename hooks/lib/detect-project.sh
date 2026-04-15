#!/bin/bash
# Detect current project slug from CWD via projects.json longest-prefix match.
# Source from hook scripts. Pure function; outputs slug to stdout.
#
# Usage:
#   PROJECT=$(detect_project "$CWD")
#
# Honors: PROJECTS_JSON env var; falls back to $MEMORY_DIR/projects.json.
# Returns empty string if no match.

detect_project() {
  local cwd="${1:-$PWD}"
  local projects_json="${PROJECTS_JSON:-$MEMORY_DIR/projects.json}"

  if [ ! -f "$projects_json" ] || ! jq empty "$projects_json" 2>/dev/null; then
    return 0
  fi

  while read -r prefix; do
    # Exact match or prefix with / boundary (prevents /fly matching /fly-other)
    case "$cwd" in
      "${prefix}"|"${prefix}"/*)
        jq -r --arg k "$prefix" '.[$k] // empty' "$projects_json" 2>/dev/null
        return 0
        ;;
    esac
  done < <(jq -r 'to_entries | sort_by(.key | length) | reverse | .[].key' "$projects_json" 2>/dev/null)
}
