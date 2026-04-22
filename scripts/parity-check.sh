#!/bin/bash
# scripts/parity-check.sh — runs bash and Go shadow on identical fixtures,
# diffs the resulting WAL event(s). Prints PASS/FAIL with actionable diff
# on mismatch. Invoked by `make parity` and by the CI job.
#
# Contract: both implementations must produce the same shadow-miss events
# (same slugs, same session id) for the same (fixture + prompt + injected)
# tuple. BM25 tie-breaking may differ between implementations; the fixture
# is small enough that scoring is deterministic.
set -euo pipefail

REPO=$(cd "$(dirname "$0")/.." && pwd)
BASH_SHADOW="$REPO/bin/memory-fts-shadow.sh"
GO_SHADOW="$REPO/bin/memoryctl"

[ -x "$BASH_SHADOW" ] || { echo "missing $BASH_SHADOW"; exit 1; }
[ -x "$GO_SHADOW"   ] || { echo "missing $GO_SHADOW (run: make build)"; exit 1; }

# Bash wal-lock.sh must be reachable at $HOME/.claude/hooks/lib/ — that's
# how the shadow script sources it. Install.sh makes that symlink; here
# we tolerate missing installs by seeding a throwaway HOME.
if [ ! -f "$HOME/.claude/hooks/lib/wal-lock.sh" ]; then
  echo "note: $HOME/.claude/hooks/lib/wal-lock.sh missing — run ./install.sh first for an honest parity check." >&2
  exit 2
fi

_run_case() {
  local name="$1" prompt="$2"; shift 2
  local injected="${1:-}"

  local TMP
  TMP=$(mktemp -d)
  local MEM="$TMP/memory"
  mkdir -p "$MEM"/{mistakes,feedback,strategies,knowledge,notes,seeds,.runtime}

  cat > "$MEM/mistakes/sed-bsd-gnu.md" <<EOF
---
type: mistake
status: active
severity: major
---
BSD sed behaves differently from GNU sed on 1,/pat/ range addresses.
EOF
  cat > "$MEM/feedback/portability-rule.md" <<EOF
---
type: feedback
status: active
---
Always test shell scripts under both BSD and GNU coreutils before release.
EOF

  # Bash run — capture stderr so diagnostic output is available on mismatch.
  local BASH_ERR="$TMP/bash.err"
  CLAUDE_MEMORY_DIR="$MEM" bash "$BASH_SHADOW" "$prompt" "parity-$name" "$injected" >/dev/null 2>"$BASH_ERR"
  local BASH_WAL="$TMP/bash.wal"
  sort "$MEM/.wal" > "$BASH_WAL" 2>/dev/null || true
  rm -f "$MEM/.wal" "$MEM/index.db"

  # Go run on the same fixture tree.
  local GO_ERR="$TMP/go.err"
  CLAUDE_MEMORY_DIR="$MEM" "$GO_SHADOW" fts shadow "$prompt" "parity-$name" "$injected" >/dev/null 2>"$GO_ERR"
  local GO_WAL="$TMP/go.wal"
  sort "$MEM/.wal" > "$GO_WAL" 2>/dev/null || true

  if diff -q "$BASH_WAL" "$GO_WAL" >/dev/null; then
    echo "  ✓ $name"
  else
    echo "  ✗ $name"
    echo "    -- bash wal --";  sed 's/^/    /' "$BASH_WAL"
    echo "    -- go wal --";    sed 's/^/    /' "$GO_WAL"
    if [ -s "$BASH_ERR" ]; then
      echo "    -- bash stderr --"; sed 's/^/    /' "$BASH_ERR"
    fi
    if [ -s "$GO_ERR" ]; then
      echo "    -- go stderr --"; sed 's/^/    /' "$GO_ERR"
    fi
    # Dump fixture + env state to help diagnose env-specific failures.
    echo "    -- env --"
    echo "    MEM=$MEM"
    echo "    sqlite3=$(command -v sqlite3) $(sqlite3 -version 2>/dev/null | head -1)"
    echo "    index.db after runs:"
    [ -f "$MEM/index.db" ] && sqlite3 "$MEM/index.db" "SELECT COUNT(*) FROM mem;" 2>&1 | sed 's/^/      rows=/' || echo "      (no index.db)"
    rm -rf "$TMP"
    return 1
  fi
  rm -rf "$TMP"
}

echo "=== parity: bash vs Go shadow ==="
_run_case "portability-query"       "help me with GNU sed portability"  ""
_run_case "cyrillic-query"          "помоги с портативностью sed"       ""
_run_case "dedup-injected-skipped"  "portability test"                  "sed-bsd-gnu"
_run_case "empty-prompt"            ""                                  ""

echo "=== all parity cases passed ==="
