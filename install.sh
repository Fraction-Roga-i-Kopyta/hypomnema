#!/bin/bash
# Hypomnema — persistent memory system for Claude Code
# Install script: creates directory structure, copies hooks, patches settings.json
set -eo pipefail

# --- Pre-flight: required tools and platform ---
_die() { echo "ERROR: $*" >&2; exit 1; }

command -v jq   >/dev/null 2>&1 || _die "jq is required (brew install jq | apt-get install jq)"
command -v perl >/dev/null 2>&1 || _die "perl is required (preinstalled on macOS/Linux; install via package manager if missing)"
command -v awk  >/dev/null 2>&1 || _die "awk is required"

if [ "${BASH_VERSINFO[0]:-0}" -lt 3 ]; then
  _die "bash 3.2+ required, got ${BASH_VERSION:-unknown}"
fi

if [ ! -d "$HOME/.claude" ]; then
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

Without options: standard install (creates ~/.claude/memory/, symlinks hooks,
patches settings.json). Idempotent — safe to re-run after upgrades.

Options:
  --discover         Interactive wizard: scan ~/Development, ~/code,
                     ~/projects, ~/src for git repos and add selected
                     ones to ~/.claude/memory/projects.json.
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

# S7 (audit-2026-04-16): _run no longer eval's its arguments. The single
# call site needed a redirection, so we pass through the arguments as a
# normal command. If future call sites need redirection, write them
# inline with a DRY_RUN guard instead of reaching for eval.
_run() {
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] $*"
  else
    "$@"
  fi
}
# --- /Flag parsing ---

CLAUDE_DIR="${CLAUDE_DIR:-$HOME/.claude}"
MEMORY_DIR="${CLAUDE_DIR}/memory"
HOOKS_DIR="${CLAUDE_DIR}/hooks"
SETTINGS="${CLAUDE_DIR}/settings.json"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ "$SKIP_BASE" -eq 0 ]; then

echo "=== Hypomnema installer ==="
echo "Claude dir: $CLAUDE_DIR"
echo "Memory dir: $MEMORY_DIR"
[ "$DRY_RUN" -eq 1 ] && echo "(dry-run mode — no changes will be made)"
echo ""

# --- Create directory structure ---
echo "[1/4] Creating memory directories..."
for dir in mistakes feedback knowledge strategies projects notes journal continuity seeds; do
  mkdir -p "$MEMORY_DIR/$dir"
done
echo "  Created: mistakes/ feedback/ knowledge/ strategies/ projects/ notes/ journal/ continuity/ seeds/"

# Populate seeds/ on first install only (empty target = fresh install).
# Skip if user already has seeds to preserve their edits/additions.
if [ -d "$SCRIPT_DIR/seeds" ] && [ -z "$(ls -A "$MEMORY_DIR/seeds" 2>/dev/null)" ]; then
  cp "$SCRIPT_DIR/seeds/"*.md "$MEMORY_DIR/seeds/" 2>/dev/null
  echo "  Seeded seeds/ with starter patterns (dormant hints, activate on prompt triggers)"
fi

# --- Symlink hooks ---
echo "[2/4] Installing hooks (symlinks)..."
mkdir -p "$HOOKS_DIR"
ln -sf "$SCRIPT_DIR/hooks/session-start.sh" "$HOOKS_DIR/memory-session-start.sh"
ln -sf "$SCRIPT_DIR/hooks/session-stop.sh" "$HOOKS_DIR/memory-stop.sh"
ln -sf "$SCRIPT_DIR/hooks/user-prompt-submit.sh" "$HOOKS_DIR/memory-user-prompt-submit.sh"
ln -sf "$SCRIPT_DIR/hooks/memory-index.sh" "$HOOKS_DIR/memory-index.sh"
ln -sf "$SCRIPT_DIR/hooks/memory-outcome.sh" "$HOOKS_DIR/memory-outcome.sh"
ln -sf "$SCRIPT_DIR/hooks/memory-dedup.sh" "$HOOKS_DIR/memory-dedup.sh"
ln -sf "$SCRIPT_DIR/hooks/wal-compact.sh" "$HOOKS_DIR/wal-compact.sh"
ln -sf "$SCRIPT_DIR/hooks/regen-memory-index.sh" "$HOOKS_DIR/regen-memory-index.sh"
ln -sf "$SCRIPT_DIR/hooks/bench-memory.sh" "$HOOKS_DIR/bench-memory.sh"
ln -sf "$SCRIPT_DIR/hooks/memory-analytics.sh" "$HOOKS_DIR/memory-analytics.sh"
ln -sf "$SCRIPT_DIR/hooks/test-memory-hooks.sh" "$HOOKS_DIR/test-memory-hooks.sh"
# Shared library (wal-lock.sh, sourced by hooks at runtime)
ln -sfn "$SCRIPT_DIR/hooks/lib" "$HOOKS_DIR/lib"
echo "  Symlinked: memory-session-start.sh, memory-stop.sh, memory-user-prompt-submit.sh,"
echo "             memory-index.sh, memory-outcome.sh, memory-dedup.sh, wal-compact.sh,"
echo "             bench-memory.sh, memory-analytics.sh, test-memory-hooks.sh, lib/"

# --- Symlink utilities ---
echo "[3/4] Installing utilities (symlinks)..."
mkdir -p "$CLAUDE_DIR/bin"
ln -sf "$SCRIPT_DIR/bin/consolidate.sh" "$CLAUDE_DIR/bin/memory-consolidate.sh"
ln -sf "$SCRIPT_DIR/bin/memory-strategy-score.sh" "$CLAUDE_DIR/bin/memory-strategy-score.sh"
ln -sf "$SCRIPT_DIR/bin/memory-self-profile.sh" "$CLAUDE_DIR/bin/memory-self-profile.sh"
ln -sf "$SCRIPT_DIR/bin/memory-fts-sync.sh" "$CLAUDE_DIR/bin/memory-fts-sync.sh"
ln -sf "$SCRIPT_DIR/bin/memory-fts-query.sh" "$CLAUDE_DIR/bin/memory-fts-query.sh"
ln -sf "$SCRIPT_DIR/bin/memory-fts-shadow.sh" "$CLAUDE_DIR/bin/memory-fts-shadow.sh"
echo "  Symlinked: memory-consolidate.sh, memory-strategy-score.sh, memory-self-profile.sh,"
echo "             memory-fts-sync.sh, memory-fts-query.sh, memory-fts-shadow.sh"

# --- Patch settings.json ---
echo "[4/4] Patching settings.json..."
if [ ! -f "$SETTINGS" ]; then
  echo '{}' > "$SETTINGS"
fi

# Backup
cp "$SETTINGS" "${SETTINGS}.backup-hypomnema"

# Add hooks if not present
HAS_SESSION_START=$(jq '.hooks.SessionStart // empty' "$SETTINGS" 2>/dev/null)
HAS_STOP=$(jq '.hooks.Stop // empty' "$SETTINGS" 2>/dev/null)
HAS_USER_PROMPT_SUBMIT=$(jq '.hooks.UserPromptSubmit // empty' "$SETTINGS" 2>/dev/null)

if [ -z "$HAS_SESSION_START" ]; then
  jq '.hooks.SessionStart = [{"hooks": [{"type": "command", "command": "~/.claude/hooks/memory-session-start.sh", "timeout": 15}]}]' "$SETTINGS" > "${SETTINGS}.tmp" && mv "${SETTINGS}.tmp" "$SETTINGS"
  echo "  Added SessionStart hook"
else
  echo "  SessionStart hook already exists — skipping (add manually if needed)"
fi

if [ -z "$HAS_STOP" ]; then
  jq '.hooks.Stop = [{"hooks": [{"type": "command", "command": "~/.claude/hooks/memory-stop.sh", "timeout": 10}]}]' "$SETTINGS" > "${SETTINGS}.tmp" && mv "${SETTINGS}.tmp" "$SETTINGS"
  echo "  Added Stop hook"
else
  echo "  Stop hook already exists — skipping (add manually if needed)"
fi

if [ -z "$HAS_USER_PROMPT_SUBMIT" ]; then
  jq '.hooks.UserPromptSubmit = [{"hooks": [{"type": "command", "command": "~/.claude/hooks/memory-user-prompt-submit.sh", "timeout": 10}]}]' "$SETTINGS" > "${SETTINGS}.tmp" && mv "${SETTINGS}.tmp" "$SETTINGS"
  echo "  Added UserPromptSubmit hook (v0.5 context triggers)"
else
  echo "  UserPromptSubmit hook already exists — skipping (add manually if needed)"
fi

# --- Create projects.json if missing ---
if [ ! -f "$MEMORY_DIR/projects.json" ]; then
  echo '{}' > "$MEMORY_DIR/projects.json"
  echo "  Created empty projects.json (see templates/projects.json.example)"
fi
if [ ! -f "$MEMORY_DIR/projects-domains.json" ]; then
  echo '{}' > "$MEMORY_DIR/projects-domains.json"
  echo "  Created empty projects-domains.json"
fi
if [ ! -f "$MEMORY_DIR/.stopwords" ] && [ -f "$SCRIPT_DIR/hooks/.stopwords" ]; then
  cp "$SCRIPT_DIR/hooks/.stopwords" "$MEMORY_DIR/.stopwords"
  echo "  Copied default .stopwords"
fi
if [ ! -f "$MEMORY_DIR/.confidence-excludes" ] && [ -f "$SCRIPT_DIR/hooks/.confidence-excludes" ]; then
  cp "$SCRIPT_DIR/hooks/.confidence-excludes" "$MEMORY_DIR/.confidence-excludes"
  echo "  Copied default .confidence-excludes"
fi
# v0.8: copy .config.sh.example to .config.sh on fresh install
if [ ! -f "$MEMORY_DIR/.config.sh" ] && [ -f "$SCRIPT_DIR/templates/.config.sh.example" ]; then
  cp "$SCRIPT_DIR/templates/.config.sh.example" "$MEMORY_DIR/.config.sh"
  echo "  Copied default .config.sh (edit to override caps/limits)"
fi

echo ""
echo "=== Done ==="
echo ""
echo "Next steps:"
echo "  1. Add project mappings to $MEMORY_DIR/projects.json"
echo "     (or run: ./install.sh --discover for an interactive wizard)"
echo "  2. Add memory section to ~/.claude/CLAUDE.md"
echo "     (or run: ./install.sh --patch-claude-md)"
echo "  3. Restart Claude Code"
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

  local projects_json="$MEMORY_DIR/projects.json"
  if [ ! -f "$projects_json" ]; then
    if [ "$DRY_RUN" -eq 1 ]; then
      echo "[dry-run] would seed empty projects.json at $projects_json"
    else
      printf '%s\n' '{}' > "$projects_json"
    fi
  fi
  for entry in "${accepted[@]}"; do
    local path="${entry%:*}"
    local slug="${entry##*:}"
    if [ "$DRY_RUN" -eq 1 ]; then
      echo "[dry-run] would map $path → $slug"
    else
      jq --arg p "$path" --arg s "$slug" '. + {($p): $s}' "$projects_json" > "$projects_json.tmp" \
        && mv "$projects_json.tmp" "$projects_json"
    fi
  done
  echo ""
  echo "Added ${#accepted[@]} project(s) to $projects_json."
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
- Storage: `~/.claude/memory/` — read schema in the hypomnema repo `CLAUDE.md`.
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
