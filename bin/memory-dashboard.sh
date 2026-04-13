#!/bin/bash
# memory-dashboard.sh — TUI for ~/.claude/memory observation and ops
# Spec: docs/specs/2026-04-13-memory-dashboard-design.md

set -u

MEMORY_DIR="${MEMORY_DIR:-$HOME/.claude/memory}"
BIN_DIR="${BIN_DIR:-$HOME/.claude/bin}"
HOOKS_DIR="${HOOKS_DIR:-$HOME/.claude/hooks}"

main() {
    echo "memory-dashboard: not implemented yet"
}

[[ "${SOURCE_ONLY:-0}" == "1" ]] && return 0
main "$@"
