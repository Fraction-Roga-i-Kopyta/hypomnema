#!/bin/bash
# Hypomnema v2 — uninstaller
# Removes v2 shims/symlinks, strips our entries from settings.json.
# User memory in ~/.claude/memory-global/ is preserved by default — pass
# --purge-memory --yes to delete it.
set -eo pipefail

_die() { echo "ERROR: $*" >&2; exit 1; }

command -v jq >/dev/null 2>&1 || _die "jq is required"

# --- Flags ---
DRY_RUN=0
PURGE_MEMORY=0
ASSUME_YES=0

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run)       DRY_RUN=1; shift ;;
    --purge-memory)  PURGE_MEMORY=1; shift ;;
    --yes|-y)        ASSUME_YES=1; shift ;;
    --help|-h)
      cat <<EOF
Usage: ./uninstall.sh [OPTIONS]

Removes v2 shims and memoryctl symlink installed by install.sh, and strips
hypomnema entries from ~/.claude/settings.json.

By default, ~/.claude/memory-global/ (your memory files) is preserved.

Options:
  --dry-run       Print what would happen; make no changes.
  --purge-memory  Also delete ~/.claude/memory-global/ (requires --yes).
  --yes, -y       Skip confirmation prompts.
  --help          Show this message.

Examples:
  ./uninstall.sh                          # remove shims, keep memory
  ./uninstall.sh --dry-run                # preview
  ./uninstall.sh --purge-memory --yes     # wipe everything, no prompts
EOF
      exit 0
      ;;
    *) _die "unknown flag: $1 (try --help)" ;;
  esac
done

CLAUDE_DIR="${CLAUDE_DIR:-$HOME/.claude}"
MEMORY_GLOBAL_DIR="$CLAUDE_DIR/memory-global"
HOOKS_DIR="$CLAUDE_DIR/hooks"
V2_HOOKS_DIR="$HOOKS_DIR/v2"
BIN_DIR="$CLAUDE_DIR/bin"
SETTINGS="$CLAUDE_DIR/settings.json"

_run() {
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] $*"
  else
    "$@"
  fi
}

echo "=== Hypomnema v2 uninstaller ==="
echo "Claude dir: $CLAUDE_DIR"
[ "$DRY_RUN" -eq 1 ] && echo "(dry-run mode — no changes will be made)"
echo ""

# --- Refuse if ~/.claude does not exist ---
[ -d "$CLAUDE_DIR" ] || { echo "Nothing to uninstall: $CLAUDE_DIR does not exist."; exit 0; }

SCRIPT_DIR="$(cd "$(dirname "$0")" 2>/dev/null && pwd)"

# --- [1/3] Remove v2 shim files from ~/.claude/hooks/v2/ ---
echo "[1/3] Removing v2 hook shims from $V2_HOOKS_DIR..."
removed_shims=0
for shim in session-start.sh user-prompt-submit.sh pre-tool-write.sh session-stop.sh; do
  target="$V2_HOOKS_DIR/$shim"
  if [ -f "$target" ] || [ -L "$target" ]; then
    _run rm "$target"
    removed_shims=$((removed_shims + 1))
  fi
done
# Remove v2 dir if now empty
if [ "$DRY_RUN" -eq 0 ] && [ -d "$V2_HOOKS_DIR" ] && [ -z "$(ls -A "$V2_HOOKS_DIR" 2>/dev/null)" ]; then
  rmdir "$V2_HOOKS_DIR" 2>/dev/null || true
fi
echo "  Removed $removed_shims shim(s)."

# --- [2/3] Remove memoryctl symlink from ~/.claude/bin/ ---
echo "[2/3] Removing memoryctl from $BIN_DIR..."
_remove_our_symlinks() {
  local dir="$1" removed=0 path target
  [ -d "$dir" ] || { echo 0; return; }
  while IFS= read -r path; do
    [ -L "$path" ] || continue
    target=$(readlink "$path" 2>/dev/null) || continue
    case "$target" in
      "$SCRIPT_DIR"/*)
        _run rm "$path"
        removed=$((removed + 1))
        ;;
    esac
  done < <(find "$dir" -mindepth 1 -maxdepth 1 2>/dev/null)
  echo "$removed"
}
removed_bin=$(_remove_our_symlinks "$BIN_DIR")
echo "  Removed $removed_bin symlink(s) from $BIN_DIR."

# --- [3/3] Strip v2 hypomnema hook entries from settings.json ---
# Matches the 4 v2 shim paths registered by install.sh.
echo "[3/3] Stripping hypomnema v2 entries from $SETTINGS..."
if [ -f "$SETTINGS" ]; then
  if [ "$DRY_RUN" -eq 1 ]; then
    affected=$(jq '[.hooks // {} | to_entries[] | .value[]?.hooks[]?
                    | select((.command // "") | test("hooks/v2/.*\\.sh"))] | length' \
                  "$SETTINGS" 2>/dev/null || echo 0)
    echo "[dry-run] would strip $affected hypomnema v2 entry(ies) across all hook events"
  else
    tmp="$SETTINGS.tmp.$$"
    jq '
      if (.hooks // {}) == {} then .
      else
        .hooks |= (
          with_entries(
            .value |= (
              map(
                .hooks = ((.hooks // []) | map(select(
                  (.command // "") | test("hooks/v2/.*\\.sh") | not
                )))
              )
              | map(select(((.hooks // []) | length) > 0))
            )
          )
          | with_entries(select((.value | length) > 0))
        )
        | if (.hooks // {}) == {} then del(.hooks) else . end
      end
    ' "$SETTINGS" > "$tmp" && mv "$tmp" "$SETTINGS"
    echo "  settings.json updated (all hooks/v2/*.sh entries removed)."
  fi
else
  echo "  $SETTINGS not found — skipping."
fi

# --- Memory directory (user data) ---
echo ""
if [ "$PURGE_MEMORY" -eq 1 ]; then
  if [ -d "$MEMORY_GLOBAL_DIR" ]; then
    if [ "$ASSUME_YES" -ne 1 ]; then
      printf "Delete memory directory '%s' and all its contents? [y/N]: " "$MEMORY_GLOBAL_DIR"
      read -r ans </dev/tty || ans="n"
      case "$ans" in
        y|Y|yes) ;;
        *) echo "Skipped memory purge."; MEMORY_GLOBAL_DIR=""; ;;
      esac
    fi
    if [ -n "$MEMORY_GLOBAL_DIR" ] && [ -d "$MEMORY_GLOBAL_DIR" ]; then
      _run rm -rf "$MEMORY_GLOBAL_DIR"
      echo "Memory directory removed."
    fi
  else
    echo "Memory directory not found — nothing to purge."
  fi
else
  [ -d "$MEMORY_GLOBAL_DIR" ] && echo "Memory directory preserved: $MEMORY_GLOBAL_DIR"
  [ -d "$MEMORY_GLOBAL_DIR" ] && echo "  (pass --purge-memory --yes to delete it)"
fi

echo ""
echo "=== Done ==="
echo ""
if [ -f "$SETTINGS.backup-hypomnema" ]; then
  echo "Note: original settings backup still at $SETTINGS.backup-hypomnema"
  echo "      (not auto-restored; delete or keep as you prefer)"
fi
