#!/bin/bash
# scripts/run-uninstalled-tests.sh
#
# Runs hooks/test-memory-hooks.sh against the in-repo hooks/ and bin/
# directories without requiring ./install.sh to have run first.
#
# The test suite is integration-style: it invokes hooks by their
# Claude-Code-style filenames (memory-session-start.sh, memory-stop.sh,
# memory-user-prompt-submit.sh) which only exist as symlinks created by
# install.sh's _rename_dest mapping. To avoid duplicating that mapping
# in the test runner itself, this script materialises a throwaway
# symlink directory that mirrors install.sh's layout, then points
# HYPOMNEMA_HOOKS_DIR / HYPOMNEMA_BIN_DIR at it.
#
# Used by `make test-uninstalled` (added 2026-05-08, external review
# finding E5).

set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
SETUP_DIR="$(mktemp -d -t hypomnema-uninstalled-XXXXXX)"
trap 'rm -rf "$SETUP_DIR"' EXIT INT TERM

HOOKS="$SETUP_DIR/hooks"
BIN="$SETUP_DIR/bin"
mkdir -p "$HOOKS" "$BIN"

# Mirror install.sh::_rename_dest for the three renamed hooks.
ln -sf "$REPO/hooks/session-start.sh"      "$HOOKS/memory-session-start.sh"
ln -sf "$REPO/hooks/session-stop.sh"       "$HOOKS/memory-stop.sh"
ln -sf "$REPO/hooks/user-prompt-submit.sh" "$HOOKS/memory-user-prompt-submit.sh"

# Pass-through hooks: same name as in repo.
for src in "$REPO/hooks"/*.sh; do
  base=$(basename "$src")
  case "$base" in
    session-start.sh|session-stop.sh|user-prompt-submit.sh|test-memory-hooks.sh|test-fixture-snapshot.sh|bench-memory.sh)
      continue ;;
  esac
  ln -sf "$src" "$HOOKS/$base"
done

# Shared lib (wal-lock.sh and friends sourced by hooks at runtime).
ln -sfn "$REPO/hooks/lib" "$HOOKS/lib"

# bin/ scripts. No renames currently — install.sh's _rename_dest only
# applies to the four hook names above.
for src in "$REPO/bin"/*.sh "$REPO/bin"/*.py; do
  [ -f "$src" ] || continue
  ln -sf "$src" "$BIN/$(basename "$src")"
done

# Compiled Go binary (memoryctl), if `make build` already produced it.
if [ -x "$REPO/bin/memoryctl" ]; then
  ln -sf "$REPO/bin/memoryctl" "$BIN/memoryctl"
fi

export HYPOMNEMA_HOOKS_DIR="$HOOKS"
export HYPOMNEMA_BIN_DIR="$BIN"
exec bash "$REPO/hooks/test-memory-hooks.sh"
