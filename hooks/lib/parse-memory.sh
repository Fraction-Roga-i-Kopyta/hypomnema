#!/bin/bash
# Frontmatter parsers and memory-file lookup helpers.
# Source from hook scripts. Pure functions + AWK program strings.
#
# Provides:
#   $AWK_MISTAKES         — single-pass AWK program for mistake/* frontmatter
#   $AWK_SCORED           — single-pass AWK program for feedback/knowledge/strategies/notes/decisions/* frontmatter
#   parse_related <file>  → "target:type ..." (space-separated)
#   find_memory_file <slug> → absolute path (stdout) or rc=1
#   domain_matches <file_domains> → rc=0 if intersects $DOMAINS, else rc=1
#
# Caller must have $MEMORY_DIR set. domain_matches reads $DOMAINS from caller scope.

# --- Single-pass frontmatter parsers (BSD awk compatible) ---
# Uses FNR==1 to detect file boundaries instead of BEGINFILE/ENDFILE.
# Output: TSV lines per file, one awk process per directory.

AWK_MISTAKES='
{ gsub(/\r/, "") }
FNR == 1 {
  if (prev_file != "") {
    printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", \
      prev_file, status, project, recurrence, injected, severity, root_cause, prevention, domains, keywords, scope, body
  }
  in_fm = 0; fm_end_count = 0
  status = "_empty_"; project = "_empty_"; recurrence = "0"; injected = "0000-00-00"
  severity = "_empty_"; root_cause = "_empty_"; prevention = "_empty_"; domains = "_none_"; keywords = "_none_"
  scope = "domain"; body = ""; past_fm = 0; prev_file = FILENAME
}
/^---$/ {
  fm_end_count++
  if (fm_end_count == 1) { in_fm = 1; next }
  if (fm_end_count == 2) { in_fm = 0; past_fm = 1; next }
}
in_fm && /^status:/ { sub(/^status: */, ""); status = $0; next }
in_fm && /^project:/ { sub(/^project: */, ""); project = $0; next }
in_fm && /^recurrence:/ { sub(/^recurrence: */, ""); recurrence = $0; next }
in_fm && /^injected:/ { sub(/^injected: */, ""); injected = $0; next }
in_fm && /^severity:/ { sub(/^severity: */, ""); severity = $0; next }
in_fm && /^root-cause:/ { sub(/^root-cause: */, ""); root_cause = $0; next }
in_fm && /^prevention:/ { sub(/^prevention: */, ""); prevention = $0; next }
in_fm && /^domains:/ { sub(/^domains: *\[?/, ""); sub(/\].*/, ""); gsub(/, */, " "); domains = $0; next }
in_fm && /^keywords:/ { sub(/^keywords: *\[?/, ""); sub(/\].*/, ""); gsub(/, */, " "); keywords = $0; next }
in_fm && /^scope:/ { sub(/^scope: */, ""); scope = $0; next }
past_fm && !/^$/ { gsub(/\t/, " ", $0); body = body $0 "\x1e" }
END {
  if (prev_file != "") {
    gsub(/\t/, " ", root_cause); gsub(/\t/, " ", prevention)
    printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", \
      prev_file, status, project, recurrence, injected, severity, root_cause, prevention, domains, keywords, scope, body
  }
}'

AWK_SCORED='
{ gsub(/\r/, "") }
FNR == 1 {
  if (prev_file != "") {
    printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", prev_file, status, project, referenced, domains, keywords, body
  }
  in_fm = 0; fm_end_count = 0
  status = "_empty_"; project = "_empty_"; referenced = "0000-00-00"; domains = "_none_"; keywords = "_none_"; body = ""; past_fm = 0; prev_file = FILENAME
}
/^---$/ {
  fm_end_count++
  if (fm_end_count == 1) { in_fm = 1; next }
  if (fm_end_count == 2) { in_fm = 0; past_fm = 1; next }
}
in_fm && /^status:/ { sub(/^status: */, ""); status = $0; next }
in_fm && /^project:/ { sub(/^project: */, ""); project = $0; next }
in_fm && /^referenced:/ { sub(/^referenced: */, ""); referenced = $0; next }
in_fm && /^injected:/ { sub(/^injected: */, ""); if (referenced == "0000-00-00") referenced = $0; next }
in_fm && /^domains:/ { sub(/^domains: *\[?/, ""); sub(/\].*/, ""); gsub(/, */, " "); domains = $0; next }
in_fm && /^keywords:/ { sub(/^keywords: *\[?/, ""); sub(/\].*/, ""); gsub(/, */, " "); keywords = $0; next }
past_fm && !/^$/ { gsub(/\t/, " ", $0); body = body $0 "\x1e" }
END {
  if (prev_file != "") {
    printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", prev_file, status, project, referenced, domains, keywords, body
  }
}'

# --- Parse related links from a memory file ---
# Input: file path. Output: space-separated "target:type" pairs.
parse_related() {
  local file="$1"
  [ -f "$file" ] || return 0
  awk '
    /^---$/ { fm_count++; if (fm_count >= 2) exit; next }
    fm_count == 1 && /^related:/ { in_rel = 1; next }
    fm_count == 1 && in_rel && /^  - / {
      line = $0
      sub(/^  - /, "", line)
      gsub(/: /, ":", line)
      gsub(/ /, "", line)
      printf "%s ", line
      next
    }
    fm_count == 1 && in_rel && !/^  - / { in_rel = 0 }
  ' "$file"
}

# --- Find memory file by slug (basename without .md) ---
# Searches mistakes, feedback, strategies, knowledge, notes, decisions in $MEMORY_DIR.
find_memory_file() {
  local slug="$1"
  local f
  for dir in mistakes feedback strategies knowledge notes decisions; do
    f="$MEMORY_DIR/$dir/${slug}.md"
    [ -f "$f" ] && printf '%s' "$f" && return 0
  done
  return 1
}

# --- Domain matching helper ---
# Returns 0 (true) if file domains intersect with active $DOMAINS, or if file
# has "general" domain. Reads $DOMAINS from caller scope (not passed as arg —
# preserves call-site simplicity at the cost of one global dependency).
domain_matches() {
  local file_domains="$1"
  [ -z "$DOMAINS" ] && return 0          # no active domains → match all
  [ -z "$file_domains" ] && return 0     # no file domains → match (untagged)
  [ "$file_domains" = "_none_" ] && return 0  # untagged marker from awk
  local fd ad
  local IFS=' '
  for fd in $file_domains; do
    [ "$fd" = "general" ] && return 0    # general always matches
    for ad in $DOMAINS; do
      [ "$fd" = "$ad" ] && return 0
    done
  done
  return 1  # no intersection
}
