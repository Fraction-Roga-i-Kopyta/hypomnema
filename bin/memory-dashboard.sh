#!/bin/bash
# memory-dashboard.sh — TUI for ~/.claude/memory observation and ops
# Spec: docs/specs/2026-04-13-memory-dashboard-design.md

set -u

MEMORY_DIR="${MEMORY_DIR:-$HOME/.claude/memory}"
BIN_DIR="${BIN_DIR:-$HOME/.claude/bin}"
HOOKS_DIR="${HOOKS_DIR:-$HOME/.claude/hooks}"

# parse_frontmatter_field FILE FIELD
# Extracts a top-level YAML field from frontmatter (between --- markers).
# Prints empty string if not found. Strips surrounding quotes from value.
parse_frontmatter_field() {
    local file=$1 field=$2
    awk -v f="$field" '
        BEGIN { in_fm = 0; depth = 0 }
        /^---[[:space:]]*$/ { depth++; in_fm = (depth == 1); next }
        depth >= 2 { exit }
        in_fm {
            if (match($0, "^" f ":[[:space:]]*")) {
                v = substr($0, RLENGTH+1)
                gsub(/^["'\''"]|["'\'']$/, "", v)
                gsub(/[[:space:]]+$/, "", v)
                print v
                exit
            }
        }
    ' "$file" 2>/dev/null
}

main() {
    echo "memory-dashboard: not implemented yet"
}

[[ "${SOURCE_ONLY:-0}" == "1" ]] && return 0
main "$@"
