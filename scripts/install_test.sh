#!/bin/bash
# Sandbox integration test for install.sh / uninstall.sh.
# Run from anywhere: scripts/install_test.sh. Needs bin/memoryctl (make build)
# and jq. Uses an isolated CLAUDE_DIR per case — never touches ~/.claude.
set -u

REPO="$(cd "$(dirname "$0")/.." && pwd)"
FAILS=0
TMPROOT="$(mktemp -d)"
trap 'rm -rf "$TMPROOT"' EXIT

_fail() { echo "FAIL: $*" >&2; FAILS=$((FAILS + 1)); }
_ok()   { echo "  ok: $*"; }

[ -x "$REPO/bin/memoryctl" ] || { echo "install_test: bin/memoryctl missing — run 'make build' first" >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "install_test: jq required" >&2; exit 1; }

_sandbox() {
  local d="$TMPROOT/$1"
  mkdir -p "$d"
  echo "$d"
}

# --- 1. Corrupt settings.json: abort loudly, BEFORE any files are installed ---
CD="$(_sandbox corrupt)"
echo '{broken' > "$CD/settings.json"
out=$(CLAUDE_DIR="$CD" "$REPO/install.sh" 2>&1)
rc=$?
if [ "$rc" -eq 0 ]; then
  _fail "corrupt settings: install exited 0 (output: $out)"
else
  _ok "corrupt settings aborts (rc=$rc)"
fi
echo "$out" | grep -qi "not valid JSON" || _fail "corrupt settings: no clear error message"
[ -d "$CD/hooks/v2" ] && _fail "corrupt settings: shims were installed before the abort"

# --- 2. Fresh install: 6 shims, 6 registrations, Next steps → doctor ---
CD="$(_sandbox fresh)"
echo '{}' > "$CD/settings.json"
out=$(CLAUDE_DIR="$CD" "$REPO/install.sh" 2>&1) || _fail "fresh install exited non-zero: $out"
n=$(find "$CD/hooks/v2" -name '*.sh' 2>/dev/null | wc -l | tr -d ' ')
[ "$n" -eq 6 ] || _fail "fresh install: expected 6 shims, got $n"
reg=$(jq '[.hooks // {} | to_entries[] | .value[]?.hooks[]?
           | select((.command // "") | test("hooks/v2/.*\\.sh"))] | length' "$CD/settings.json")
[ "$reg" -eq 6 ] || _fail "fresh install: expected 6 registered hooks, got $reg"
echo "$out" | grep -q "memoryctl doctor" || _fail "next steps must recommend 'memoryctl doctor'"
echo "$out" | grep -q "memoryctl status" && _fail "next steps still recommends nonexistent 'memoryctl status'"
_ok "fresh install"

# --- 3. --dry-run with a stale symlink must not crash (arithmetic bug) ---
# bash 3.2 survives the $((...)) syntax error with exit 0 but spews it;
# bash 4+/5 aborts. Guard against both manifestations.
CD="$(_sandbox dryrun)"
echo '{}' > "$CD/settings.json"
mkdir -p "$CD/hooks"
ln -s "$REPO/hooks/v2/ghost-does-not-exist.sh" "$CD/hooks/stale-shim.sh"
out=$(CLAUDE_DIR="$CD" "$REPO/install.sh" --dry-run 2>&1)
rc=$?
if [ "$rc" -ne 0 ]; then
  _fail "dry-run with a stale symlink exited non-zero (rc=$rc)"
elif echo "$out" | grep -q "syntax error"; then
  _fail "dry-run with a stale symlink spews an arithmetic syntax error"
else
  _ok "dry-run with stale symlink"
fi

# --- 4. Uninstall removes ALL 6 shims and every settings entry ---
CD="$(_sandbox uninstall)"
echo '{}' > "$CD/settings.json"
CLAUDE_DIR="$CD" "$REPO/install.sh" >/dev/null 2>&1 || _fail "install (for uninstall case) failed"
CLAUDE_DIR="$CD" "$REPO/uninstall.sh" >/dev/null 2>&1 || _fail "uninstall exited non-zero"
left=$(find "$CD/hooks/v2" -name '*.sh' 2>/dev/null | wc -l | tr -d ' ')
[ "$left" -eq 0 ] || _fail "uninstall left $left shim(s) behind"
reg=$(jq '[.hooks // {} | to_entries[] | .value[]?.hooks[]?
           | select((.command // "") | test("hooks/v2/"))] | length' "$CD/settings.json")
[ "$reg" -eq 0 ] || _fail "uninstall left $reg hook entries in settings.json"
_ok "uninstall"

# --- 5. --discover is removed (its memoryctl verb never existed) ---
CD="$(_sandbox discover)"
echo '{}' > "$CD/settings.json"
if CLAUDE_DIR="$CD" "$REPO/install.sh" --discover >/dev/null 2>&1; then
  _fail "--discover must be rejected with an explanation"
else
  _ok "--discover rejected"
fi

echo ""
if [ "$FAILS" -gt 0 ]; then
  echo "install_test: $FAILS failure(s)"
  exit 1
fi
echo "install_test: all checks passed"
