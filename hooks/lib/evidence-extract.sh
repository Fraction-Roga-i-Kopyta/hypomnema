# hooks/lib/evidence-extract.sh
# Evidence extraction for feedback detector (v0.7.1).
# Two pure functions; no side effects. Source from hook scripts.
#
#   evidence_from_frontmatter <path> → stdout: newline-separated phrases
#   evidence_from_body        <path> → stdout: newline-separated unique tokens (stubbed in Task 1; implemented in Task 2)

# ---------- evidence_from_frontmatter ----------
# Reads YAML array under `evidence:` key in memory frontmatter (between `---` fences).
# Supports block-style list form:
#   evidence:
#     - "phrase one"
#     - phrase two
# Output: one phrase per line, unquoted, trimmed. Empty stdout if field absent/malformed.
evidence_from_frontmatter() {
  local file="$1"
  [ -f "$file" ] || return 0
  awk '
    BEGIN { in_fm = 0; fm_closed = 0; in_evid = 0 }
    /^---[[:space:]]*$/ {
      if (in_fm == 0 && fm_closed == 0) { in_fm = 1; next }
      if (in_fm == 1) { in_fm = 0; fm_closed = 1; exit }
    }
    in_fm == 0 { next }
    /^evidence:[[:space:]]*$/ { in_evid = 1; next }
    in_evid == 1 {
      # New top-level key (no leading whitespace, contains colon) stops the block.
      if (/^[A-Za-z_][A-Za-z0-9_-]*:/) { in_evid = 0; next }
      # Match list item: "  - value" with optional quotes.
      if (match($0, /^[[:space:]]*-[[:space:]]+/)) {
        val = substr($0, RSTART + RLENGTH)
        gsub(/^"|"$/, "", val)
        gsub(/^'"'"'|'"'"'$/, "", val)
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", val)
        if (val != "") print val
      }
    }
  ' "$file" 2>/dev/null
}

# ---------- evidence_from_body (stub; implemented in Task 2) ----------
evidence_from_body() {
  return 0
}
