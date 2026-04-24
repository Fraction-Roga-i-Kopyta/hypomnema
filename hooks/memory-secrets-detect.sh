#!/bin/bash
# memory-secrets-detect.sh — block memory writes that embed plaintext
# secrets. Runs as a PreToolUse:Write|Edit hook; only scans writes
# whose target lives under ~/.claude/memory/ (the user is
# intentionally authoring memory there) and only blocks when a
# secret-looking token appears OUTSIDE a fenced code block.
set -o pipefail
export LC_ALL=C

# Reads the PreToolUse JSON envelope on stdin:
#   { "tool_name": "Write"|"Edit", "tool_input": { "file_path": "...", "content": "..." } }
# Exits 2 (block) on a hit, 0 otherwise. Escape hatch:
#   HYPOMNEMA_ALLOW_SECRETS=1  — single-invocation override.

INPUT=$(cat)

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"

# Bypass: operator has deliberately approved this write.
if [ "${HYPOMNEMA_ALLOW_SECRETS:-0}" = "1" ]; then
  exit 0
fi

FILE_PATH=$(printf '%s' "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)
[ -z "$FILE_PATH" ] && exit 0

# Only scan memory-root writes. Everything else is out of scope —
# user may be editing code that legitimately contains literal
# API-key tokens (secrets-rotation scripts, test fixtures, …).
case "$FILE_PATH" in
  "$MEMORY_DIR"/*|"$MEMORY_DIR"/.*) ;;
  *) exit 0 ;;
esac

# .secretsignore whitelist (P1.7). Two files, either of which can
# whitelist a path:
#   .secretsignore.default — committed to the repo and copied by
#       install.sh; holds maintainer-supplied rules (e.g. seeds/**).
#   .secretsignore         — user-managed, optional, for domain-
#       specific paths the user knows are safe (docs/examples/**).
# Syntax is a minimal subset of .gitignore: one pattern per line,
# `#` comments, leading `!` to un-ignore, `**` matches any number
# of path segments. Paths are matched relative to $MEMORY_DIR.
_secretsignore_match() {
  local ignore_file="$1" path="$2"
  [ -f "$ignore_file" ] || return 1
  local line pattern negate matched=1 raw
  shopt -s globstar extglob 2>/dev/null || true
  while IFS= read -r raw || [ -n "$raw" ]; do
    line="${raw%%#*}"
    # Strip leading / trailing whitespace.
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    [ -z "$line" ] && continue
    negate=0
    if [ "${line:0:1}" = "!" ]; then
      negate=1; pattern="${line#!}"
    else
      pattern="$line"
    fi
    # shellcheck disable=SC2053  # glob match, intentional
    if [[ "$path" == $pattern ]]; then
      matched=$negate
    fi
  done < "$ignore_file"
  # matched=0 → whitelisted; matched=1 → not whitelisted (shell bool inverted).
  return "$matched"
}

REL_PATH="${FILE_PATH#$MEMORY_DIR/}"
if _secretsignore_match "$MEMORY_DIR/.secretsignore.default" "$REL_PATH" ||
   _secretsignore_match "$MEMORY_DIR/.secretsignore" "$REL_PATH"; then
  exit 0
fi

# Write tool delivers text via `content`; Edit delivers it via
# `new_string`. Hook is registered on both (PreToolUse:Write|Edit in
# install.sh), so fall back between the two fields or Edit writes
# slip past the gate entirely.
CONTENT=$(printf '%s' "$INPUT" | jq -r '.tool_input.content // .tool_input.new_string // empty' 2>/dev/null)
[ -z "$CONTENT" ] && exit 0

# Scan the content. Strategy: strip fenced code blocks and inline
# code spans before regex-matching so documentation-style examples
# (`api_key: XXX`) don't trip the gate.
#
# Then look for patterns that strongly suggest a real credential:
#
#   - `api_key`, `api-key`, `apikey`
#   - `secret`, `password`, `token`
#   - `aws_access_key`, `aws_secret_key` (and variants)
#
# followed by `:` or `=` and a non-whitespace value of length ≥ 8.
# Length floor filters out empty placeholders ("password: xxx").
#
# perl is used rather than awk because BSD awk lacks \b word-boundary
# support; the repo already depends on perl (install.sh preflight).
SCAN=$(printf '%s' "$CONTENT" | perl -e '
  my $in_fence = 0;
  my $n = 0;
  while (my $line = <STDIN>) {
    $n++;
    if ($line =~ /^```/) { $in_fence = !$in_fence; next; }
    next if $in_fence;
    $line =~ s/`[^`]*`//g;
    if ($line =~ /\b(api[_-]?key|apikey|aws[_-]?(?:access|secret)[_-]?key|secret|password|token)\s*[:=]\s*[^\s"'"'"'`]{8,}/i) {
      my $match = $&;
      print "$n: $match\n";
    }
  }
')

if [ -z "$SCAN" ]; then
  exit 0
fi

# Report on stderr so Claude Code surfaces the message to the user.
# Point at the offending line(s) + the escape hatch.
{
  echo "Blocked: $FILE_PATH would write a plaintext secret-looking token."
  echo "Matches (line : fragment):"
  printf '%s\n' "$SCAN" | sed 's/^/  /'
  echo ""
  echo "If this is intentional (e.g. a seed file documenting what NOT to do),"
  echo "set HYPOMNEMA_ALLOW_SECRETS=1 for this single tool invocation and retry."
} >&2

exit 2
