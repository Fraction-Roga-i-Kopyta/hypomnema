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
# R8: BOM is embedded as literal bytes below — BSD awk does not interpret
# \xNN / \NNN escapes in inline regex literals, so we concatenate the
# actual bytes via the shell at source time.
_UTF8_BOM=$(printf "\xef\xbb\xbf")

AWK_MISTAKES='
# R8: strip UTF-8 BOM on the first line of every new file
FNR == 1 { sub(/^'"$_UTF8_BOM"'/, "") }
{ gsub(/\r/, "") }
FNR == 1 {
  # R3: only emit a record if the previous file had a closed frontmatter
  # (past_fm == 1). Unclosed files used to appear as empty-body entries
  # with valid-looking metadata. (audit-2026-04-16 R3)
  if (prev_file != "" && past_fm == 1) {
    printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", \
      prev_file, status, project, recurrence, injected, severity, root_cause, prevention, domains, keywords, scope, body
  }
  in_fm = 0; fm_end_count = 0
  status = "_empty_"; project = "_empty_"; recurrence = "0"; injected = "0000-00-00"
  severity = "_empty_"; root_cause = "_empty_"; prevention = "_empty_"; domains = "_none_"; keywords = "_none_"
  scope = "domain"; body = ""; past_fm = 0; prev_file = FILENAME
  in_kw = 0; in_dom = 0
}
/^---$/ {
  fm_end_count++
  if (fm_end_count == 1) { in_fm = 1; next }
  if (fm_end_count == 2) { in_fm = 0; past_fm = 1; next }
}
# R2: tolerate tab after colon (was space-only). R4: strip surrounding quotes.
# R6: detect block-scalar marker (| or >) and replace with placeholder.
in_fm && /^status:/        { sub(/^status:[[:space:]]*/, ""); gsub(/^["\x27]+|["\x27]+$/, ""); if ($0 == "|" || $0 == ">") $0 = "_multiline_unsupported_"; status = $0; next }
in_fm && /^project:/       { sub(/^project:[[:space:]]*/, ""); gsub(/^["\x27]+|["\x27]+$/, ""); project = $0; next }
in_fm && /^recurrence:/    { sub(/^recurrence:[[:space:]]*/, ""); gsub(/^["\x27]+|["\x27]+$/, ""); recurrence = $0; next }
in_fm && /^injected:/      { sub(/^injected:[[:space:]]*/, ""); gsub(/^["\x27]+|["\x27]+$/, ""); injected = $0; next }
in_fm && /^severity:/      { sub(/^severity:[[:space:]]*/, ""); gsub(/^["\x27]+|["\x27]+$/, ""); severity = $0; next }
in_fm && /^root-cause:/    { sub(/^root-cause:[[:space:]]*/, ""); gsub(/^["\x27]+|["\x27]+$/, ""); if ($0 == "|" || $0 == ">") $0 = "_multiline_unsupported_"; root_cause = $0; next }
in_fm && /^prevention:/    { sub(/^prevention:[[:space:]]*/, ""); gsub(/^["\x27]+|["\x27]+$/, ""); if ($0 == "|" || $0 == ">") $0 = "_multiline_unsupported_"; prevention = $0; next }
in_fm && /^scope:/         { sub(/^scope:[[:space:]]*/, ""); gsub(/^["\x27]+|["\x27]+$/, ""); scope = $0; next }
# R7: list keys — handle inline [a, b] AND block form (next lines `  - x`).
in_fm && /^domains:/       { sub(/^domains:[[:space:]]*\[?/, ""); sub(/\].*/, ""); gsub(/, */, " "); domains = $0; if (domains == "") in_dom = 1; next }
in_fm && /^keywords:/      { sub(/^keywords:[[:space:]]*\[?/, ""); sub(/\].*/, ""); gsub(/, */, " "); keywords = $0; if (keywords == "") in_kw = 1; next }
in_fm && (in_kw || in_dom) && /^[[:space:]]+-[[:space:]]/ {
  v = $0; sub(/^[[:space:]]+-[[:space:]]+/, "", v); gsub(/^["\x27]+|["\x27]+$/, "", v)
  if (in_kw) { if (keywords == "" || keywords == "_none_") keywords = v; else keywords = keywords " " v }
  else       { if (domains  == "" || domains  == "_none_") domains  = v; else domains  = domains  " " v }
  next
}
in_fm && (in_kw || in_dom) && !/^[[:space:]]+-[[:space:]]/ { in_kw = 0; in_dom = 0 }
past_fm && !/^$/ { gsub(/\t/, " ", $0); body = body $0 "\x1e" }
# R3: track that the second '---' was seen; END block refuses to emit a
# record for files with unclosed frontmatter.
END {
  # R3: skip unclosed frontmatter.
  if (prev_file != "" && past_fm == 1) {
    gsub(/\t/, " ", root_cause); gsub(/\t/, " ", prevention)
    if (keywords == "") keywords = "_none_"
    if (domains  == "") domains  = "_none_"
    printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", \
      prev_file, status, project, recurrence, injected, severity, root_cause, prevention, domains, keywords, scope, body
  }
}'

AWK_SCORED='
# R8: strip UTF-8 BOM on the first line of every new file
FNR == 1 { sub(/^'"$_UTF8_BOM"'/, "") }
{ gsub(/\r/, "") }
FNR == 1 {
  # R3: only emit when previous file had a closed frontmatter.
  if (prev_file != "" && past_fm == 1) {
    printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", prev_file, status, project, referenced, domains, keywords, body
  }
  in_fm = 0; fm_end_count = 0
  status = "_empty_"; project = "_empty_"; referenced = "0000-00-00"; domains = "_none_"; keywords = "_none_"; body = ""; past_fm = 0; prev_file = FILENAME
  in_kw = 0; in_dom = 0
}
/^---$/ {
  fm_end_count++
  if (fm_end_count == 1) { in_fm = 1; next }
  if (fm_end_count == 2) { in_fm = 0; past_fm = 1; next }
}
# R2/R4/R6 scalar fixes; R7 block-array list fix; R8 BOM already stripped above.
in_fm && /^status:/     { sub(/^status:[[:space:]]*/, ""); gsub(/^["\x27]+|["\x27]+$/, ""); if ($0 == "|" || $0 == ">") $0 = "_multiline_unsupported_"; status = $0; next }
in_fm && /^project:/    { sub(/^project:[[:space:]]*/, ""); gsub(/^["\x27]+|["\x27]+$/, ""); project = $0; next }
in_fm && /^referenced:/ { sub(/^referenced:[[:space:]]*/, ""); gsub(/^["\x27]+|["\x27]+$/, ""); referenced = $0; next }
in_fm && /^injected:/   { sub(/^injected:[[:space:]]*/, ""); gsub(/^["\x27]+|["\x27]+$/, ""); if (referenced == "0000-00-00") referenced = $0; next }
in_fm && /^domains:/    { sub(/^domains:[[:space:]]*\[?/, ""); sub(/\].*/, ""); gsub(/, */, " "); domains = $0; if (domains == "") in_dom = 1; next }
in_fm && /^keywords:/   { sub(/^keywords:[[:space:]]*\[?/, ""); sub(/\].*/, ""); gsub(/, */, " "); keywords = $0; if (keywords == "") in_kw = 1; next }
in_fm && (in_kw || in_dom) && /^[[:space:]]+-[[:space:]]/ {
  v = $0; sub(/^[[:space:]]+-[[:space:]]+/, "", v); gsub(/^["\x27]+|["\x27]+$/, "", v)
  if (in_kw) { if (keywords == "" || keywords == "_none_") keywords = v; else keywords = keywords " " v }
  else       { if (domains  == "" || domains  == "_none_") domains  = v; else domains  = domains  " " v }
  next
}
in_fm && (in_kw || in_dom) && !/^[[:space:]]+-[[:space:]]/ { in_kw = 0; in_dom = 0 }
past_fm && !/^$/ { gsub(/\t/, " ", $0); body = body $0 "\x1e" }
# R3: track that the second '---' was seen; END block refuses to emit a
# record for files with unclosed frontmatter.
END {
  # R3: skip unclosed frontmatter.
  if (prev_file != "" && past_fm == 1) {
    if (keywords == "") keywords = "_none_"
    if (domains  == "") domains  = "_none_"
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
# SECURITY (audit-2026-04-16 S2, CWE-22): `related:` slugs originate from
# LLM-authored frontmatter. A slug like `../../victim` would resolve outside
# $MEMORY_DIR and the caller would later rewrite `referenced:` / `ref_count:`
# on the traversed file via `perl -pi -e`. Reject any slug that contains a
# path separator, `..`, NUL, or a leading dot — keep the identifier space
# restricted to filesystem-safe basenames.
find_memory_file() {
  local slug="$1"
  [ -n "$slug" ] || return 1
  # Reject slugs that could escape the memory dir or break downstream tools.
  # NUL is already impossible in a shell variable.
  case "$slug" in
    */*|*..*|.*|*$'\n'*) return 1 ;;
  esac
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
