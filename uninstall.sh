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

# --- Remove hook symlinks ---
# Everything install.sh creates under $HOOKS_DIR. Delete only if the path
# is a symlink (our creation); leave regular files alone — a user may
# have replaced one of our symlinks with their own script, and nuking it
# would be destructive.
HOOK_LINKS=(
  memory-session-start.sh
  memory-stop.sh
  memory-user-prompt-submit.sh
  memory-index.sh
  memory-outcome.sh
  memory-dedup.sh
  memory-analytics.sh
  wal-compact.sh
  regen-memory-index.sh
  bench-memory.sh
  test-memory-hooks.sh
  lib
)

echo "[1/3] Removing hook symlinks from $HOOKS_DIR..."
removed_hooks=0
for name in "${HOOK_LINKS[@]}"; do
  path="$HOOKS_DIR/$name"
  if [ -L "$path" ]; then
    _run rm "$path"
    removed_hooks=$((removed_hooks + 1))
  fi
done
echo "  Removed $removed_hooks hook symlink(s)."

# --- Remove utility symlinks from ~/.claude/bin ---
BIN_LINKS=(
  memory-consolidate.sh
  memory-strategy-score.sh
  memory-self-profile.sh
  memory-fts-sync.sh
  memory-fts-query.sh
  memory-fts-shadow.sh
)

echo "[2/3] Removing utility symlinks from $BIN_DIR..."
removed_bin=0
for name in "${BIN_LINKS[@]}"; do
  path="$BIN_DIR/$name"
  if [ -L "$path" ]; then
    _run rm "$path"
    removed_bin=$((removed_bin + 1))
  fi
done
echo "  Removed $removed_bin utility symlink(s)."

# --- Strip our hooks from settings.json ---
# Do NOT blindly restore $SETTINGS.backup-hypomnema: a user may have
# added unrelated hooks after install, and the backup would wipe them.
# Instead, surgically remove entries whose command references our
# scripts, then drop empty hook arrays.
echo "[3/3] Stripping hypomnema entries from $SETTINGS..."
if [ -f "$SETTINGS" ]; then
  for pair in "SessionStart:memory-session-start.sh" \
              "Stop:memory-stop.sh" \
              "UserPromptSubmit:memory-user-prompt-submit.sh"; do
    hook_name="${pair%%:*}"
    cmd_frag="${pair##*:}"
    tmp="$SETTINGS.tmp.$$"
    if [ "$DRY_RUN" -eq 1 ]; then
      echo "[dry-run] would strip entries referencing '$cmd_frag' from .hooks.$hook_name"
      continue
    fi
    jq --arg name "$hook_name" --arg frag "$cmd_frag" '
      if (.hooks // {})[$name] then
        .hooks[$name] |= (
          map(
            .hooks = ((.hooks // []) | map(select((.command // "") | contains($frag) | not)))
          )
          | map(select(((.hooks // []) | length) > 0))
        )
        | if ((.hooks[$name] // []) | length) == 0 then del(.hooks[$name]) else . end
      else . end
    ' "$SETTINGS" > "$tmp" && mv "$tmp" "$SETTINGS"
  done
  # If .hooks is now empty object, remove it too
  if [ "$DRY_RUN" -eq 0 ]; then
    tmp="$SETTINGS.tmp.$$"
    jq 'if (.hooks // {}) == {} then del(.hooks) else . end' "$SETTINGS" > "$tmp" && mv "$tmp" "$SETTINGS"
  fi
  echo "  settings.json updated."
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
