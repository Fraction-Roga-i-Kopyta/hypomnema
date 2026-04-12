#!/bin/bash
# Regenerate MEMORY.md index from memory/ filesystem.
# Writes to $MEMORY_INDEX_PATH (default: ~/.claude/projects/-Users-akamash/memory/MEMORY.md).
# Descriptions extracted from first body line after frontmatter.
# Pure rebuild — existing human-written hooks are lost.

set -o pipefail

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
INDEX_PATH="${MEMORY_INDEX_PATH:-$HOME/.claude/projects/-Users-akamash/memory/MEMORY.md}"
LINK_PREFIX="${MEMORY_INDEX_LINK_PREFIX:-../../../.claude/memory}"

[ -d "$MEMORY_DIR" ] || exit 0
mkdir -p "$(dirname "$INDEX_PATH")" 2>/dev/null || exit 0

extract_desc() {
  local f="$1" desc

  # Frontmatter-first: description → root-cause → trigger
  desc=$(awk '
    /^---$/ { fm++; if (fm == 2) exit; next }
    fm == 1 && /^description:/ { sub(/^description: */, ""); gsub(/^["\x27]|["\x27]$/, ""); print; exit }
    fm == 1 && /^root-cause:/ { sub(/^root-cause: */, ""); gsub(/^["\x27]|["\x27]$/, ""); root=$0 }
    fm == 1 && /^trigger:/ { sub(/^trigger: */, ""); gsub(/^["\x27]|["\x27]$/, ""); trig=$0 }
    END { if (root) print root; else if (trig) print trig }
  ' "$f" 2>/dev/null | head -1)

  # Fallback: first body line
  if [ -z "$desc" ]; then
    desc=$(awk '
      /^---$/ { fm++; if (fm == 2) body = 1; next }
      !body { next }
      /^[[:space:]]*$/ { next }
      /^#/ { next }
      { print; exit }
    ' "$f" 2>/dev/null)
  fi

  printf '%s' "$desc" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//' |
    perl -CSD -ne 'chomp; my $s = $_; if (length($s) > 90) { $s = substr($s, 0, 90) . "..."; } print $s'
}

prettify_slug() {
  printf '%s' "$1" | sed 's/[-_]/ /g' | awk '{
    $1 = toupper(substr($1,1,1)) substr($1,2)
    print
  }'
}

emit_section() {
  local title="$1" dir="$2"
  [ -d "$MEMORY_DIR/$dir" ] || return 0
  local files
  files=$(find "$MEMORY_DIR/$dir" -maxdepth 1 -type f -name '*.md' 2>/dev/null | sort)
  [ -z "$files" ] && return 0

  printf '\n## %s\n' "$title"
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    local slug title_txt desc
    slug=$(basename "$f" .md)
    title_txt=$(prettify_slug "$slug")
    desc=$(extract_desc "$f")
    if [ -n "$desc" ]; then
      printf -- '- [%s](%s/%s/%s.md) — %s\n' "$title_txt" "$LINK_PREFIX" "$dir" "$slug" "$desc"
    else
      printf -- '- [%s](%s/%s/%s.md)\n' "$title_txt" "$LINK_PREFIX" "$dir" "$slug"
    fi
  done <<< "$files"
}

{
  printf '# Memory Index\n'
  emit_section "Strategies" "strategies"
  emit_section "Feedback" "feedback"
  emit_section "Mistakes" "mistakes"
  emit_section "Knowledge" "knowledge"
  emit_section "Projects" "projects"
  emit_section "Notes" "notes"
  emit_section "Continuity" "continuity"
} > "$INDEX_PATH.tmp" 2>/dev/null && mv "$INDEX_PATH.tmp" "$INDEX_PATH" 2>/dev/null

exit 0
