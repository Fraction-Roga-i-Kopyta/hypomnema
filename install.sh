#!/bin/bash
# Hypomnema v2 — persistent memory system for Claude Code
# Install script: creates directory structure, installs v2 shims, patches settings.json
set -eo pipefail

# --- Pre-flight: required tools and platform ---
_die() { echo "ERROR: $*" >&2; exit 1; }

command -v jq   >/dev/null 2>&1 || _die "jq is required (brew install jq | apt-get install jq)"
command -v perl >/dev/null 2>&1 || _die "perl is required (preinstalled on macOS/Linux; install via package manager if missing)"
command -v awk  >/dev/null 2>&1 || _die "awk is required"

if [ "${BASH_VERSINFO[0]:-0}" -lt 3 ]; then
  _die "bash 3.2+ required, got ${BASH_VERSION:-unknown}"
fi

if [ ! -d "${CLAUDE_DIR:-$HOME/.claude}" ]; then
  _die "~/.claude not found — install Claude Code first (https://docs.anthropic.com/en/docs/claude-code)"
fi
# --- /Pre-flight ---

# --- Flag parsing ---
DRY_RUN=0
PATCH_CLAUDE_MD=0
SKIP_BASE=0

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run)         DRY_RUN=1; shift ;;
    --discover)
      _die "--discover was removed in v2.5.0: v2 tracks projects automatically (per-project native stores are derived from the working directory) — no registration step exists or is needed"
      ;;
    --patch-claude-md) PATCH_CLAUDE_MD=1; shift ;;
    --skip-base)       SKIP_BASE=1; shift ;;
    --help|-h)
      cat <<EOF
Usage: ./install.sh [OPTIONS]

Without options: standard install (creates ~/.claude/memory-global/, installs
v2 shims into ~/.claude/hooks/v2/, registers them in settings.json, symlinks
memoryctl into ~/.claude/bin/). Idempotent — safe to re-run after upgrades.

Options:
  --patch-claude-md  Append a four-line memory section to ~/.claude/CLAUDE.md
                     (idempotent — checks for existing marker).
  --dry-run          Print what would happen; make no changes.
  --skip-base        Skip the base install and run only flagged actions.
  --help             Show this message.

Examples:
  ./install.sh                                  # base install
  ./install.sh --patch-claude-md                # base + claude-md patch
  ./install.sh --skip-base --patch-claude-md    # only patch (already installed)
  ./install.sh --dry-run                        # preview, no writes
EOF
      exit 0
      ;;
    *) _die "unknown flag: $1 (try --help)" ;;
  esac
done

_run() {
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] $*"
  else
    "$@"
  fi
}
# --- /Flag parsing ---

CLAUDE_DIR="${CLAUDE_DIR:-$HOME/.claude}"
MEMORY_GLOBAL_DIR="${CLAUDE_DIR}/memory-global"
HOOKS_DIR="${CLAUDE_DIR}/hooks"
BIN_DIR="${CLAUDE_DIR}/bin"
SETTINGS="${CLAUDE_DIR}/settings.json"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# --- Pre-flight gates: everything that can refuse the install must fire
# --- BEFORE any file is created, copied, or patched. ---

# settings.json must be valid JSON before we back it up or patch it — a
# corrupt file lets every jq registration below fail while the installer
# still prints "Done" (2026-07-08 review P1 #7, reproduced in sandbox). Only
# the base install touches settings.json, so --skip-base skips this gate
# (a flagged-actions-only run must not abort on an unrelated corrupt file).
if [ "$SKIP_BASE" -eq 0 ] && [ -f "$SETTINGS" ] && ! jq empty "$SETTINGS" >/dev/null 2>&1; then
  _die "settings.json is not valid JSON: $SETTINGS — fix it (or restore a ${SETTINGS}.backup-hypomnema-* copy), then re-run"
fi

# Claude Code version gate (best-effort): v2 needs native memory (>= 2.1.59).
# Runs pre-flight so an unsupported install refuses before copying anything.
if command -v claude >/dev/null 2>&1; then
  _cc_ver=$(claude --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
  if [ -n "$_cc_ver" ]; then
    _cc_major=$(echo "$_cc_ver" | cut -d. -f1)
    _cc_minor=$(echo "$_cc_ver" | cut -d. -f2)
    _cc_patch=$(echo "$_cc_ver" | cut -d. -f3)
    # Require >= 2.1.59
    _needs_upgrade=0
    if [ "$_cc_major" -lt 2 ]; then _needs_upgrade=1
    elif [ "$_cc_major" -eq 2 ] && [ "$_cc_minor" -lt 1 ]; then _needs_upgrade=1
    elif [ "$_cc_major" -eq 2 ] && [ "$_cc_minor" -eq 1 ] && [ "$_cc_patch" -lt 59 ]; then _needs_upgrade=1
    fi
    if [ "$_needs_upgrade" -eq 1 ]; then
      echo "" >&2
      echo "ERROR: hypomnema v2 requires Claude Code v2.1.59+ (native memory)." >&2
      echo "  You have $_cc_ver. Upgrade Claude Code, or use hypomnema v1.x (git checkout v1.1.2)." >&2
      exit 1
    fi
    echo "Claude Code version: $_cc_ver (>= 2.1.59 OK)"
  fi
else
  echo "WARNING: could not verify Claude Code version; v2 needs v2.1.59+"
fi
# --- /Pre-flight gates ---

if [ "$SKIP_BASE" -eq 0 ]; then

echo "=== Hypomnema v2 installer ==="
echo "Claude dir: $CLAUDE_DIR"
echo "Memory dir: $MEMORY_GLOBAL_DIR"
[ "$DRY_RUN" -eq 1 ] && echo "(dry-run mode — no changes will be made)"
echo ""

# --- [1/4] Create native global store directory ---
echo "[1/4] Creating global memory store..."
_run mkdir -p "$MEMORY_GLOBAL_DIR"
_run mkdir -p "$BIN_DIR"
echo "  Created: $MEMORY_GLOBAL_DIR"

# --- [2/4] Install memoryctl binary ---
echo "[2/4] Installing memoryctl..."
if [ -x "$SCRIPT_DIR/bin/memoryctl" ]; then
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] would symlink $SCRIPT_DIR/bin/memoryctl -> $BIN_DIR/memoryctl"
  else
    ln -sf "$SCRIPT_DIR/bin/memoryctl" "$BIN_DIR/memoryctl"
    echo "  Linked memoryctl -> $BIN_DIR/memoryctl"
  fi
else
  echo ""
  echo "ERROR: bin/memoryctl not found." >&2
  echo "  Run 'make build' first, then re-run install.sh." >&2
  exit 1
fi

# --- [3/4] Install v2 shims ---
echo "[3/4] Installing v2 hook shims..."
V2_HOOKS_DIR="$HOOKS_DIR/v2"
if [ "$DRY_RUN" -eq 1 ]; then
  echo "[dry-run] would create $V2_HOOKS_DIR"
  echo "[dry-run] would copy 6 shims from $SCRIPT_DIR/hooks/v2/ to $V2_HOOKS_DIR"
else
  mkdir -p "$V2_HOOKS_DIR"
  for shim in session-start.sh user-prompt-submit.sh pre-tool-write.sh skill-learnings-inject.sh skill-active.sh session-stop.sh; do
    src="$SCRIPT_DIR/hooks/v2/$shim"
    if [ -f "$src" ]; then
      # rm -f first: a leftover DANGLING symlink at the dest makes `cp` follow
      # it and fail ("No such file or directory"), which under `set -e` aborts
      # the installer mid-way — after memoryctl is linked but before hooks are
      # wired (review R5). Removing the dest makes cp write a fresh regular file.
      rm -f "$V2_HOOKS_DIR/$shim"
      cp "$src" "$V2_HOOKS_DIR/$shim"
      chmod +x "$V2_HOOKS_DIR/$shim"
    else
      echo "WARNING: shim not found: $src" >&2
    fi
  done
  echo "  Installed 6 shims into $V2_HOOKS_DIR"
fi

# --- Sweep stale symlinks left by earlier versions ---
_sweep_stale_links() {
  local dir="$1" swept=0 path target
  [ -d "$dir" ] || { echo 0; return; }
  while IFS= read -r path; do
    [ -L "$path" ] || continue
    target=$(readlink "$path" 2>/dev/null) || continue
    case "$target" in
      "$SCRIPT_DIR"/*) ;;
      *) continue ;;
    esac
    [ -e "$target" ] && continue
    # stdout of this function IS its return value (a count) — the dry-run
    # notice must go to stderr or it corrupts the caller's arithmetic.
    if [ "$DRY_RUN" -eq 1 ]; then
      echo "[dry-run] rm $path" >&2
    else
      rm "$path"
    fi
    swept=$((swept + 1))
  done < <(find "$dir" -mindepth 1 -maxdepth 1 2>/dev/null)
  echo "$swept"
}
stale_hooks=$(_sweep_stale_links "$HOOKS_DIR")
stale_bin=$(_sweep_stale_links "$BIN_DIR")
stale_total=$((stale_hooks + stale_bin))
if [ "$stale_total" -gt 0 ]; then
  echo "  Swept $stale_total stale symlink(s) left by previous versions"
fi

# --- [4/4] settings.json ---
echo "[4/4] Patching settings.json..."

if [ ! -f "$SETTINGS" ]; then
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] would create $SETTINGS with empty object"
  else
    echo '{}' > "$SETTINGS"
  fi
fi

# Backup settings.json
if [ -f "$SETTINGS" ]; then
  BACKUP_TS=$(date +%Y%m%d-%H%M%S)
  _run cp "$SETTINGS" "${SETTINGS}.backup-hypomnema-${BACKUP_TS}"
  if [ ! -e "${SETTINGS}.backup-hypomnema" ] && [ "$DRY_RUN" -eq 0 ]; then
    ln -s "$(basename "${SETTINGS}.backup-hypomnema-${BACKUP_TS}")" \
          "${SETTINGS}.backup-hypomnema" 2>/dev/null || \
          cp "${SETTINGS}" "${SETTINGS}.backup-hypomnema"
  fi
fi

# Register one hypomnema hook entry. Idempotent by command string.
# Args: event matcher command timeout label
register_hook() {
  local event="$1" matcher="$2" command="$3" timeout="$4" label="$5"
  if [ ! -f "$SETTINGS" ]; then
    [ "$DRY_RUN" -eq 1 ] && echo "[dry-run] would register $label" && return 0
    return 0
  fi
  if jq -e --arg ev "$event" --arg cmd "$command" \
      '(.hooks[$ev] // []) | any(.hooks[]? | .command == $cmd)' \
      "$SETTINGS" >/dev/null 2>&1; then
    echo "  $label already registered — skipping"
    return 0
  fi
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] would register $label under .hooks.$event"
    return 0
  fi
  local entry
  if [ -n "$matcher" ]; then
    entry=$(jq -n --arg m "$matcher" --arg cmd "$command" --argjson to "$timeout" \
      '{matcher: $m, hooks: [{type: "command", command: $cmd, timeout: $to}]}')
  else
    entry=$(jq -n --arg cmd "$command" --argjson to "$timeout" \
      '{hooks: [{type: "command", command: $cmd, timeout: $to}]}')
  fi
  jq --arg ev "$event" --argjson entry "$entry" \
     '.hooks[$ev] = ((.hooks[$ev] // []) + [$entry])' \
     "$SETTINGS" > "${SETTINGS}.tmp" && mv "${SETTINGS}.tmp" "$SETTINGS" \
     || _die "failed to register $label in $SETTINGS"
  echo "  Added $label"
}

register_hook SessionStart     ""      "$CLAUDE_DIR/hooks/v2/session-start.sh"           15 "SessionStart (hypomnema v2)"
register_hook UserPromptSubmit ""      "$CLAUDE_DIR/hooks/v2/user-prompt-submit.sh"      10 "UserPromptSubmit (hypomnema v2)"
register_hook PreToolUse       "Write|Edit" "$CLAUDE_DIR/hooks/v2/pre-tool-write.sh"      10 "PreToolUse secrets gate (hypomnema v2)"
register_hook PostToolUse      "Skill" "$CLAUDE_DIR/hooks/v2/skill-learnings-inject.sh"  10 "PostToolUse(Skill) skill learnings (hypomnema v2)"
register_hook PreToolUse       "Skill" "$CLAUDE_DIR/hooks/v2/skill-active.sh"             10 "PreToolUse(Skill) active-skill marker (hypomnema v2)"
register_hook Stop             ""      "$CLAUDE_DIR/hooks/v2/session-stop.sh"            10 "Stop (hypomnema v2)"

# Upgrade hint: existing v1 store. Detect by v1 SUBDIRECTORIES — in v2 the
# $CLAUDE_DIR/memory dir always exists (it holds .wal/.sidecar.db runtime), so
# testing the dir alone nagged every v2 user to "migrate" on each run.
_has_v1_store=0
for d in mistakes strategies feedback knowledge decisions notes; do
  [ -d "$CLAUDE_DIR/memory/$d" ] && _has_v1_store=1
done
if [ "$_has_v1_store" -eq 1 ]; then
  echo ""
  echo "  NOTE: Existing v1 store detected at $CLAUDE_DIR/memory/."
  echo "  Run 'memoryctl migrate --dry-run' to preview the v1->v2 migration"
  echo "  (see docs/MIGRATION.md for details)."
fi

echo ""
echo "=== Done ==="
echo ""
echo "Next steps:"
echo "  1. Restart Claude Code (hooks take effect on next launch)"
echo "  2. Run: memoryctl doctor   (verifies the install end-to-end)"
[ "$_has_v1_store" -eq 1 ] && echo "  3. When ready: memoryctl migrate --execute  (to move v1 -> v2 store)"
echo ""
echo "Backup saved: ${SETTINGS}.backup-hypomnema"

fi  # /SKIP_BASE


# ---------- Optional: --patch-claude-md ----------
patch_claude_md() {
  echo ""
  echo "=== Patch ~/.claude/CLAUDE.md ==="
  local f="$CLAUDE_DIR/CLAUDE.md"
  local marker="<!-- hypomnema-section -->"
  if [ -f "$f" ] && grep -q "$marker" "$f"; then
    echo "$f already contains hypomnema section — skipping."
    return 0
  fi
  local block
  block=$(cat <<'BLOCK'

<!-- hypomnema-section -->
## Memory (hypomnema)
- Global store: `~/.claude/memory-global/`; per-project: `~/.claude/projects/<slug>/memory/`. Stores are FLAT — the kind is the `type:` frontmatter field, not a subdirectory.
- SessionStart + UserPromptSubmit hooks auto-inject the most relevant facts; `memoryctl recall "<query>"` pulls on demand.
- When you learn something durable, write a native memory file with `name:`, `description:`, `type:` (mistake | strategy | feedback | knowledge | decision | note) — see the hypomnema repo `CLAUDE.md` for the schema.
<!-- /hypomnema-section -->
BLOCK
)
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] would append memory section to $f"
    printf '%s\n' "$block"
  else
    [ -f "$f" ] || touch "$f"
    printf '%s\n' "$block" >> "$f"
    echo "Appended memory section to $f"
  fi
}

if [ "$PATCH_CLAUDE_MD" -eq 1 ]; then
  patch_claude_md
fi
