#!/bin/bash
# Hypomnema — persistent memory system for Claude Code
# Install script: creates directory structure, copies hooks, patches settings.json
set -e

CLAUDE_DIR="${CLAUDE_DIR:-$HOME/.claude}"
MEMORY_DIR="${CLAUDE_DIR}/memory"
HOOKS_DIR="${CLAUDE_DIR}/hooks"
SETTINGS="${CLAUDE_DIR}/settings.json"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "=== Hypomnema installer ==="
echo "Claude dir: $CLAUDE_DIR"
echo "Memory dir: $MEMORY_DIR"
echo ""

# --- Create directory structure ---
echo "[1/4] Creating memory directories..."
for dir in mistakes feedback knowledge strategies projects notes journal continuity; do
  mkdir -p "$MEMORY_DIR/$dir"
done
echo "  Created: mistakes/ feedback/ knowledge/ strategies/ projects/ notes/ journal/ continuity/"

# --- Symlink hooks ---
echo "[2/4] Installing hooks (symlinks)..."
mkdir -p "$HOOKS_DIR"
ln -sf "$SCRIPT_DIR/hooks/session-start.sh" "$HOOKS_DIR/memory-session-start.sh"
ln -sf "$SCRIPT_DIR/hooks/session-stop.sh" "$HOOKS_DIR/memory-stop.sh"
ln -sf "$SCRIPT_DIR/hooks/memory-index.sh" "$HOOKS_DIR/memory-index.sh"
ln -sf "$SCRIPT_DIR/hooks/memory-outcome.sh" "$HOOKS_DIR/memory-outcome.sh"
ln -sf "$SCRIPT_DIR/hooks/memory-dedup.sh" "$HOOKS_DIR/memory-dedup.sh"
ln -sf "$SCRIPT_DIR/hooks/wal-compact.sh" "$HOOKS_DIR/wal-compact.sh"
ln -sf "$SCRIPT_DIR/hooks/bench-memory.sh" "$HOOKS_DIR/bench-memory.sh"
ln -sf "$SCRIPT_DIR/hooks/test-memory-hooks.sh" "$HOOKS_DIR/test-memory-hooks.sh"
echo "  Symlinked: memory-session-start.sh, memory-stop.sh, memory-index.sh,"
echo "             memory-outcome.sh, memory-dedup.sh, wal-compact.sh,"
echo "             bench-memory.sh, test-memory-hooks.sh"

# --- Symlink utilities ---
echo "[3/4] Installing utilities (symlinks)..."
mkdir -p "$CLAUDE_DIR/bin"
ln -sf "$SCRIPT_DIR/bin/consolidate.sh" "$CLAUDE_DIR/bin/memory-consolidate.sh"
echo "  Symlinked: memory-consolidate.sh"

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

if [ -z "$HAS_SESSION_START" ]; then
  jq '.hooks.SessionStart = [{"hooks": [{"type": "command", "command": "~/.claude/hooks/memory-session-start.sh", "timeout": 5}]}]' "$SETTINGS" > "${SETTINGS}.tmp" && mv "${SETTINGS}.tmp" "$SETTINGS"
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

# --- Create projects.json if missing ---
if [ ! -f "$MEMORY_DIR/projects.json" ]; then
  echo '{}' > "$MEMORY_DIR/projects.json"
  echo "  Created empty projects.json"
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

echo ""
echo "=== Done ==="
echo ""
echo "Next steps:"
echo "  1. Add project mappings to $MEMORY_DIR/projects.json:"
echo '     {"~/Development/myproject": "myproject"}'
echo "  2. Add to your CLAUDE.md:"
echo "     - Mistakes → ~/.claude/memory/mistakes/"
echo "     - Strategies → ~/.claude/memory/strategies/"
echo "     - Continuity → ~/.claude/memory/continuity/{project}.md"
echo "  3. Restart Claude Code"
echo ""
echo "Backup saved: ${SETTINGS}.backup-hypomnema"
