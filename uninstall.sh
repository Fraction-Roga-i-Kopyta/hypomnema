#!/bin/bash
# Hypomnema — uninstaller
# Removes hooks/utility symlinks, strips our entries from settings.json.
# User memory in ~/.claude/memory/ is preserved by default — pass
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

Removes symlinks installed by install.sh and strips hypomnema entries
from ~/.claude/settings.json.

By default, ~/.claude/memory/ (your memory files) is preserved.

Options:
  --dry-run       Print what would happen; make no changes.
  --purge-memory  Also delete ~/.claude/memory/ (requires --yes).
  --yes, -y       Skip confirmation prompts.
  --help          Show this message.

Examples:
  ./uninstall.sh                          # remove symlinks, keep memory
  ./uninstall.sh --dry-run                # preview
  ./uninstall.sh --purge-memory --yes     # wipe everything, no prompts
EOF
      exit 0
      ;;
    *) _die "unknown flag: $1 (try --help)" ;;
  esac
done

CLAUDE_DIR="${CLAUDE_DIR:-$HOME/.claude}"
MEMORY_DIR="$CLAUDE_DIR/memory"
HOOKS_DIR="$CLAUDE_DIR/hooks"
BIN_DIR="$CLAUDE_DIR/bin"
SETTINGS="$CLAUDE_DIR/settings.json"

_run() {
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] $*"
  else
    "$@"
  fi
}

echo "=== Hypomnema uninstaller ==="
echo "Claude dir: $CLAUDE_DIR"
[ "$DRY_RUN" -eq 1 ] && echo "(dry-run mode — no changes will be made)"
echo ""

# --- Refuse if ~/.claude does not exist ---
[ -d "$CLAUDE_DIR" ] || { echo "Nothing to uninstall: $CLAUDE_DIR does not exist."; exit 0; }

# SCRIPT_DIR is this uninstaller's directory — the hypomnema repo root. Used
# to identify symlinks that point into our tree (vs. a user's own scripts
# that happen to live in the same install dirs).
SCRIPT_DIR="$(cd "$(dirname "$0")" 2>/dev/null && pwd)"

# --- Directory-driven symlink removal ---
# Enumerate everything under the target directory and remove any symlink
# whose target points into $SCRIPT_DIR (our repo). Leaves regular files and
# third-party symlinks untouched. Replaces the hand-rolled HOOK_LINKS /
# BIN_LINKS arrays that fell out of sync with install.sh's own directory-
# driven enumeration — v0.10.1 had three symlinks uninstall didn't know
# about (memory-error-detect.sh, memory-precompact.sh, memoryctl).
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

echo "[1/3] Removing hook symlinks from $HOOKS_DIR..."
removed_hooks=$(_remove_our_symlinks "$HOOKS_DIR")
echo "  Removed $removed_hooks hook symlink(s)."

echo "[2/3] Removing utility symlinks from $BIN_DIR..."
removed_bin=$(_remove_our_symlinks "$BIN_DIR")
echo "  Removed $removed_bin utility symlink(s)."

# --- Strip our hooks from settings.json ---
# Do NOT blindly restore $SETTINGS.backup-hypomnema: a user may have
# added unrelated hooks after install, and the backup would wipe them.
# Instead, walk every hook event and drop entries whose `.command`
# references a `memory-*.sh` script — matches what install.sh installs
# regardless of the number of hooks. The previous hard-coded pair list
# missed v0.11-v0.13 additions (memory-dedup, memory-outcome,
# memory-error-detect, memory-precompact, memory-secrets-detect) and
# left them as ghost entries after uninstall.
echo "[3/3] Stripping hypomnema entries from $SETTINGS..."
if [ -f "$SETTINGS" ]; then
  if [ "$DRY_RUN" -eq 1 ]; then
    affected=$(jq '[.hooks // {} | to_entries[] | .value[]?.hooks[]?
                    | select((.command // "") | test("memory-[a-z0-9-]+\\.sh"))] | length' \
                  "$SETTINGS" 2>/dev/null || echo 0)
    echo "[dry-run] would strip $affected hypomnema entry(ies) across all hook events"
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
                  (.command // "") | test("memory-[a-z0-9-]+\\.sh") | not
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
    echo "  settings.json updated (all memory-*.sh entries removed)."
  fi
else
  echo "  $SETTINGS not found — skipping."
fi

# --- Memory directory (user data) ---
echo ""
if [ "$PURGE_MEMORY" -eq 1 ]; then
  if [ -d "$MEMORY_DIR" ]; then
    if [ "$ASSUME_YES" -ne 1 ]; then
      printf "Delete memory directory '%s' and all its contents? [y/N]: " "$MEMORY_DIR"
      read -r ans </dev/tty || ans="n"
      case "$ans" in
        y|Y|yes) ;;
        *) echo "Skipped memory purge."; MEMORY_DIR=""; ;;
      esac
    fi
    if [ -n "$MEMORY_DIR" ] && [ -d "$MEMORY_DIR" ]; then
      _run rm -rf "$MEMORY_DIR"
      echo "Memory directory removed."
    fi
  else
    echo "Memory directory not found — nothing to purge."
  fi
else
  [ -d "$MEMORY_DIR" ] && echo "Memory directory preserved: $MEMORY_DIR"
  [ -d "$MEMORY_DIR" ] && echo "  (pass --purge-memory --yes to delete it)"
fi

echo ""
echo "=== Done ==="
echo ""
if [ -f "$SETTINGS.backup-hypomnema" ]; then
  echo "Note: original settings backup still at $SETTINGS.backup-hypomnema"
  echo "      (not auto-restored; delete or keep as you prefer)"
fi
