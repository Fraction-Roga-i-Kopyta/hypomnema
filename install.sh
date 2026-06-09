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
DISCOVER=0
PATCH_CLAUDE_MD=0
SKIP_BASE=0

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run)         DRY_RUN=1; shift ;;
    --discover)        DISCOVER=1; shift ;;
    --patch-claude-md) PATCH_CLAUDE_MD=1; shift ;;
    --skip-base)       SKIP_BASE=1; shift ;;
    --help|-h)
      cat <<EOF
Usage: ./install.sh [OPTIONS]

Without options: standard install (creates ~/.claude/memory-global/, installs
v2 shims into ~/.claude/hooks/v2/, registers them in settings.json, symlinks
memoryctl into ~/.claude/bin/). Idempotent — safe to re-run after upgrades.

Options:
  --discover         Interactive wizard: scan ~/Development, ~/code,
                     ~/projects, ~/src for git repos and register them.
  --patch-claude-md  Append a four-line memory section to ~/.claude/CLAUDE.md
                     (idempotent — checks for existing marker).
  --dry-run          Print what would happen; make no changes.
  --skip-base        Skip the base install and run only flagged actions.
  --help             Show this message.

Examples:
  ./install.sh                                # base install
  ./install.sh --discover --patch-claude-md   # base + wizard + claude-md patch
  ./install.sh --skip-base --discover         # only run wizard (already installed)
  ./install.sh --discover --dry-run           # preview discover, no writes
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
  echo "[dry-run] would copy 4 shims from $SCRIPT_DIR/hooks/v2/ to $V2_HOOKS_DIR"
else
  mkdir -p "$V2_HOOKS_DIR"
  for shim in session-start.sh user-prompt-submit.sh pre-tool-write.sh session-stop.sh; do
    src="$SCRIPT_DIR/hooks/v2/$shim"
    if [ -f "$src" ]; then
      cp "$src" "$V2_HOOKS_DIR/$shim"
      chmod +x "$V2_HOOKS_DIR/$shim"
    else
      echo "WARNING: shim not found: $src" >&2
    fi
  done
  echo "  Installed 4 shims into $V2_HOOKS_DIR"
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
    _run rm "$path"
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

# --- [4/4] Claude Code version gate + settings.json ---
echo "[4/4] Patching settings.json..."

# CC-version gate (best-effort)
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
    echo "  Claude Code version: $_cc_ver (>= 2.1.59 OK)"
  fi
else
  echo "  WARNING: could not verify Claude Code version; v2 needs v2.1.59+"
fi

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
     "$SETTINGS" > "${SETTINGS}.tmp" && mv "${SETTINGS}.tmp" "$SETTINGS"
  echo "  Added $label"
}

register_hook SessionStart     ""      "$HOME/.claude/hooks/v2/session-start.sh"      15 "SessionStart (hypomnema v2)"
register_hook UserPromptSubmit ""      "$HOME/.claude/hooks/v2/user-prompt-submit.sh" 10 "UserPromptSubmit (hypomnema v2)"
register_hook PreToolUse       "Write|Edit" "$HOME/.claude/hooks/v2/pre-tool-write.sh"     10 "PreToolUse secrets gate (hypomnema v2)"
register_hook Stop             ""      "$HOME/.claude/hooks/v2/session-stop.sh"       10 "Stop (hypomnema v2)"

# Upgrade hint: existing v1 store
if [ -d "$HOME/.claude/memory" ]; then
  echo ""
  echo "  NOTE: Existing v1 store detected at ~/.claude/memory/."
  echo "  Run 'memoryctl migrate --dry-run' to preview the v1->v2 migration"
  echo "  (see docs/MIGRATION.md for details)."
fi

echo ""
echo "=== Done ==="
echo ""
echo "Next steps:"
echo "  1. Restart Claude Code (hooks take effect on next launch)"
echo "  2. Run: memoryctl status"
[ -d "$HOME/.claude/memory" ] && echo "  3. When ready: memoryctl migrate  (to move v1 -> v2 store)"
echo ""
echo "Backup saved: ${SETTINGS}.backup-hypomnema"

fi  # /SKIP_BASE

# ---------- Optional: --discover wizard ----------
discover_projects() {
  echo ""
  echo "=== Discover wizard ==="
  local roots=("$HOME/Development" "$HOME/code" "$HOME/projects" "$HOME/src")
  local found=()
  for root in "${roots[@]}"; do
    [ -d "$root" ] || continue
    while IFS= read -r repo; do
      found+=("$repo")
    done < <(find "$root" -maxdepth 3 -type d -name .git 2>/dev/null | sed 's|/.git$||')
  done

  if [ ${#found[@]} -eq 0 ]; then
    echo "No git repos found in standard dev folders (${roots[*]})."
    return 0
  fi

  echo "Found ${#found[@]} git repos. For each: [y]es to track, [n]o to skip, [q]uit."
  echo ""
  local accepted=()
  for repo in "${found[@]}"; do
    local slug
    slug=$(basename "$repo")
    printf "  %s  (slug: %s) [y/n/q]: " "$repo" "$slug"
    if [ "$DRY_RUN" -eq 1 ]; then
      echo "[dry-run]"
      accepted+=("$repo:$slug")
      continue
    fi
    read -r ans </dev/tty || ans="n"
    case "$ans" in
      y|Y) accepted+=("$repo:$slug") ;;
      q|Q) break ;;
      *) ;;
    esac
  done

  if [ ${#accepted[@]} -eq 0 ]; then
    echo ""
    echo "Nothing selected."
    return 0
  fi

  # Register with memoryctl if available, otherwise print instructions
  if command -v memoryctl >/dev/null 2>&1 || [ -x "$BIN_DIR/memoryctl" ]; then
    local mc="${HYPOMNEMA_MEMORYCTL:-$BIN_DIR/memoryctl}"
    for entry in "${accepted[@]}"; do
      local path="${entry%:*}"
      local slug="${entry##*:}"
      if [ "$DRY_RUN" -eq 1 ]; then
        echo "[dry-run] would register project: $path (slug: $slug)"
      else
        "$mc" project add --path "$path" --slug "$slug" 2>/dev/null || \
          echo "  (could not register $slug via memoryctl — add manually)"
      fi
    done
  else
    echo "memoryctl not found — add these projects manually:"
    for entry in "${accepted[@]}"; do
      echo "  memoryctl project add --path '${entry%:*}' --slug '${entry##*:}'"
    done
  fi
  echo ""
  echo "Registered ${#accepted[@]} project(s)."
}

# ---------- Optional: --patch-claude-md ----------
patch_claude_md() {
  echo ""
  echo "=== Patch ~/.claude/CLAUDE.md ==="
  local f="$HOME/.claude/CLAUDE.md"
  local marker="<!-- hypomnema-section -->"
  if [ -f "$f" ] && grep -q "$marker" "$f"; then
    echo "$f already contains hypomnema section — skipping."
    return 0
  fi
  local block
  block=$(cat <<'BLOCK'

<!-- hypomnema-section -->
## Memory (hypomnema)
- Storage: `~/.claude/memory-global/` — read schema in the hypomnema repo `CLAUDE.md`.
- SessionStart hook auto-injects relevant mistakes, strategies, feedback.
- When you learn something durable: write to `mistakes/`, `strategies/`, or `feedback/` per the protocol.
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

if [ "$DISCOVER" -eq 1 ]; then
  discover_projects
fi
if [ "$PATCH_CLAUDE_MD" -eq 1 ]; then
  patch_claude_md
fi
