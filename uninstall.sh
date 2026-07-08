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
  --purge-memory  Also delete ~/.claude/memory-global/ AND the runtime state
                  (.wal, .sidecar.db, .runtime/, self-profile.md). Requires --yes.
  --yes, -y       Skip confirmation prompts.
  --help          Show this message.

Always removes the six v2 shims, the memoryctl symlink, hypomnema's
settings.json hook entries, and the CLAUDE.md section (if --patch-claude-md
added one).

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
# Keep in lockstep with install.sh's shim list and doctor's
# requiredHookCommands (internal/doctor/doctor.go).
for shim in session-start.sh user-prompt-submit.sh pre-tool-write.sh \
            skill-learnings-inject.sh skill-active.sh session-stop.sh; do
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
        # stdout of this function is its return value (a count); a dry-run
        # notice on stdout corrupts the caller's capture (review R4).
        if [ "$DRY_RUN" -eq 1 ]; then
          echo "[dry-run] rm $path" >&2
        else
          rm "$path"
        fi
        removed=$((removed + 1))
        ;;
    esac
  done < <(find "$dir" -mindepth 1 -maxdepth 1 2>/dev/null)
  echo "$removed"
}
removed_bin=$(_remove_our_symlinks "$BIN_DIR")
echo "  Removed $removed_bin symlink(s) from $BIN_DIR."

# --- [3/3] Strip v2 hypomnema hook entries from settings.json ---
# Matches only the 6 known v2 shim names install.sh registers (not arbitrary
# hooks/v2/*.sh, which could be a user's own hook — review O5).
echo "[3/3] Stripping hypomnema v2 entries from $SETTINGS..."
if [ -f "$SETTINGS" ]; then
  if [ "$DRY_RUN" -eq 1 ]; then
    affected=$(jq '[.hooks // {} | to_entries[] | .value[]?.hooks[]?
                    | select((.command // "") | test("hooks/v2/(session-start|user-prompt-submit|pre-tool-write|skill-learnings-inject|skill-active|session-stop)\\.sh"))] | length' \
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
                  (.command // "") | test("hooks/v2/(session-start|user-prompt-submit|pre-tool-write|skill-learnings-inject|skill-active|session-stop)\\.sh") | not
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
    echo "  settings.json updated (hypomnema v2 shim entries removed)."
  fi
else
  echo "  $SETTINGS not found — skipping."
fi

# --- Memory + runtime (user data) ---
echo ""
RUNTIME_DIR="$CLAUDE_DIR/memory"
if [ "$PURGE_MEMORY" -eq 1 ]; then
  _confirmed=1
  if [ "$ASSUME_YES" -ne 1 ]; then
    printf "Delete memory (%s) AND runtime state (%s/.wal,.sidecar.db,.runtime,self-profile.md)? [y/N]: " \
      "$MEMORY_GLOBAL_DIR" "$RUNTIME_DIR"
    read -r ans </dev/tty || ans="n"
    case "$ans" in
      y|Y|yes) ;;
      *) echo "Skipped memory purge."; _confirmed=0 ;;
    esac
  fi
  if [ "$_confirmed" -eq 1 ]; then
    [ -d "$MEMORY_GLOBAL_DIR" ] && { _run rm -rf "$MEMORY_GLOBAL_DIR"; echo "Memory directory removed."; }
    # Remove hypomnema's runtime artifacts by NAME (the v1 store may still live
    # under $CLAUDE_DIR/memory — do not blow the whole dir away). "wipe
    # everything" previously left .wal/.sidecar.db/self-profile behind (O6).
    for art in .wal .wal.lockd .sidecar.db .sidecar.db-wal .sidecar.db-shm \
               self-profile.md .self-profile.lockd .runtime; do
      [ -e "$RUNTIME_DIR/$art" ] && _run rm -rf "$RUNTIME_DIR/$art"
    done
    echo "Runtime state removed."
  fi
else
  [ -d "$MEMORY_GLOBAL_DIR" ] && echo "Memory directory preserved: $MEMORY_GLOBAL_DIR"
  [ -d "$MEMORY_GLOBAL_DIR" ] && echo "  (pass --purge-memory --yes to delete it and the runtime state)"
fi

# --- Remove the CLAUDE.md section install --patch-claude-md may have added ---
# Otherwise every future session keeps getting instructions for a memory system
# that is no longer installed (review O3).
CLAUDE_MD="$CLAUDE_DIR/CLAUDE.md"
if [ -f "$CLAUDE_MD" ] && grep -q "<!-- hypomnema-section -->" "$CLAUDE_MD"; then
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] would remove the hypomnema section from $CLAUDE_MD"
  else
    tmp="$CLAUDE_MD.tmp.$$"
    awk 'BEGIN{skip=0}
         /<!-- hypomnema-section -->/{skip=1; next}
         /<!-- \/hypomnema-section -->/{skip=0; next}
         skip==0{print}' "$CLAUDE_MD" > "$tmp" && mv "$tmp" "$CLAUDE_MD"
    echo "Removed hypomnema section from $CLAUDE_MD"
  fi
fi

echo ""
echo "=== Done ==="
echo ""
if [ -f "$SETTINGS.backup-hypomnema" ]; then
  echo "Note: original settings backup still at $SETTINGS.backup-hypomnema"
  echo "      (not auto-restored; delete or keep as you prefer)"
fi
