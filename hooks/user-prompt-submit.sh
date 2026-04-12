#!/bin/bash
# UserPromptSubmit hook — inject memory files matching prompt triggers (v0.5)
# Scans memory/*/*.md frontmatter for `trigger:` (single) and `triggers:` (array),
# matches case-insensitive substring against the prompt, and injects up to
# MAX_TRIGGERED matching files as a "## Triggered (from prompt)" section.
#
# Dedup: skips files already injected at SessionStart (tracked in /tmp).
# WAL: logs `trigger-match|<slug>|<session_id>` per match.
# Graceful degradation: any error → exit 0 with no output.

set -o pipefail

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
MAX_TRIGGERED=4
TODAY=$(date +%Y-%m-%d)

# --- Parse hook input ---

INPUT=$(cat)
SESSION_ID=$(printf '%s' "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)
PROMPT=$(printf '%s' "$INPUT" | jq -r '.prompt // empty' 2>/dev/null)

[ -z "$SESSION_ID" ] && exit 0
[ -z "$PROMPT" ] && exit 0
[ -d "$MEMORY_DIR" ] || exit 0

LOWER_PROMPT=$(printf '%s' "$PROMPT" | tr '[:upper:]' '[:lower:]')

SAFE_SESSION_ID="${SESSION_ID//\//_}"
DEDUP_FILE="/tmp/.claude-injected-${SAFE_SESSION_ID}.list"
WAL_FILE="$MEMORY_DIR/.wal"

# --- Extract frontmatter fields from a memory file ---
# Output (tab-separated, single line):
#   status<TAB>severity<TAB>recurrence<TAB>ref_count<TAB>trigger_joined
# where trigger_joined is newline-separated phrases (trigger + triggers[]).
extract_meta() {
  local file="$1"
  awk '
    BEGIN { fm_count = 0; printed = 0; status = ""; severity = ""; recurrence = 0; ref_count = 0; trig_single = ""; in_trigs = 0 }
    /^---$/ { fm_count++; if (fm_count >= 2) { print_out(); printed = 1; exit } next }
    fm_count != 1 { next }
    /^status:/        { val = $0; sub(/^status: */, "", val); gsub(/["'"'"']/, "", val); status = val; next }
    /^severity:/      { val = $0; sub(/^severity: */, "", val); gsub(/["'"'"']/, "", val); severity = val; next }
    /^recurrence:/    { val = $0; sub(/^recurrence: */, "", val); recurrence = val + 0; next }
    /^ref_count:/     { val = $0; sub(/^ref_count: */, "", val); ref_count = val + 0; next }
    /^trigger:/       {
      val = $0
      sub(/^trigger: */, "", val)
      gsub(/^["'"'"']|["'"'"']$/, "", val)
      trig_single = val
      next
    }
    /^triggers:/      { in_trigs = 1; next }
    in_trigs && /^  - / {
      val = $0
      sub(/^  - */, "", val)
      gsub(/^["'"'"']|["'"'"']$/, "", val)
      trig_arr[++trig_count] = val
      next
    }
    in_trigs && !/^  / && !/^$/ { in_trigs = 0 }
    END { if (!printed) print_out() }
    function print_out(   i, joined, SEP) {
      SEP = "\x1f"
      joined = ""
      if (trig_single != "") joined = trig_single
      for (i = 1; i <= trig_count; i++) {
        if (joined != "") joined = joined SEP trig_arr[i]
        else joined = trig_arr[i]
      }
      printf "%s\t%s\t%d\t%d\t%s", status, severity, recurrence, ref_count, joined
    }
  ' "$file"
}

# --- Case-insensitive substring match ---
# Returns 0 if any phrase in stdin matches $LOWER_PROMPT.
# Echoes the matched phrase on stdout.
match_prompt() {
  local phrases="$1"
  local phrase lower_phrase
  # Phrases separated by \x1f (Unit Separator) to avoid collision with newlines in values
  local IFS=$'\x1f'
  for phrase in $phrases; do
    [ -z "$phrase" ] && continue
    lower_phrase=$(printf '%s' "$phrase" | tr '[:upper:]' '[:lower:]')
    case "$LOWER_PROMPT" in
      *"$lower_phrase"*)
        printf '%s' "$phrase"
        return 0
        ;;
    esac
  done
  return 1
}

# --- Load dedup list (SessionStart-injected slugs) ---

DEDUP_SLUGS=" "
if [ -f "$DEDUP_FILE" ]; then
  while IFS= read -r _slug; do
    [ -n "$_slug" ] && DEDUP_SLUGS="${DEDUP_SLUGS}${_slug} "
  done < "$DEDUP_FILE"
fi

# --- Collect candidates ---
# Each candidate line: priority<TAB>slug<TAB>file<TAB>matched_phrase
# Priority: sort key built from status+severity+recurrence+ref_count
CANDIDATES=""

for _dir in mistakes feedback strategies knowledge notes; do
  [ -d "$MEMORY_DIR/$_dir" ] || continue
  for _f in "$MEMORY_DIR/$_dir"/*.md; do
    [ -f "$_f" ] || continue
    _slug=$(basename "$_f" .md)

    # Dedup: skip if already in SessionStart injection
    case "$DEDUP_SLUGS" in *" ${_slug} "*) continue ;; esac

    # Extract metadata + triggers
    _meta=$(extract_meta "$_f" 2>/dev/null)
    [ -z "$_meta" ] && continue

    _status=$(printf '%s' "$_meta" | cut -f1)
    _severity=$(printf '%s' "$_meta" | cut -f2)
    _recurrence=$(printf '%s' "$_meta" | cut -f3)
    _ref_count=$(printf '%s' "$_meta" | cut -f4)
    _phrases=$(printf '%s' "$_meta" | cut -f5-)

    # Status filter
    case "$_status" in active|pinned) ;; *) continue ;; esac
    # Skip if no triggers
    [ -z "$_phrases" ] && continue

    # Match phrases against prompt
    _matched=$(match_prompt "$_phrases") || continue

    # Build priority key (higher = better, sort -r)
    # Fields: pinned=1 active=0; severity major=2 minor=1 none=0; recurrence; ref_count
    _status_rank=0
    [ "$_status" = "pinned" ] && _status_rank=1
    _sev_rank=0
    case "$_severity" in major) _sev_rank=2 ;; minor) _sev_rank=1 ;; esac

    # Zero-pad for lex sort
    _key=$(printf '%d.%d.%06d.%06d' "$_status_rank" "$_sev_rank" "$_recurrence" "$_ref_count")

    CANDIDATES="${CANDIDATES}
${_key}	${_slug}	${_f}	${_matched}"
  done
done

# --- No matches → exit silently ---
[ -z "$(printf '%s' "$CANDIDATES" | tr -d '[:space:]')" ] && exit 0

# --- Sort descending by priority, cap MAX_TRIGGERED ---

SORTED=$(printf '%s\n' "$CANDIDATES" | grep -v '^$' | sort -r | head -n "$MAX_TRIGGERED")

# --- Build markdown output + WAL events + dedup append ---

OUTPUT="## Triggered (from prompt)"
NEW_INJECTED=""

while IFS=$'\t' read -r _key _slug _file _phrase; do
  [ -z "$_slug" ] && continue
  _body=$(sed '1,/^---$/d; 1,/^---$/d' "$_file" 2>/dev/null | sed '/^$/d')
  # Fallback: if body empty (mistakes often store info in frontmatter fields)
  # synthesize from root-cause + prevention
  if [ -z "$_body" ]; then
    _rc=$(awk '/^---$/{n++} n==1 && /^root-cause:/{sub(/^root-cause: */,""); gsub(/^["'"'"']|["'"'"']$/,""); print; exit}' "$_file" 2>/dev/null)
    _prev=$(awk '/^---$/{n++} n==1 && /^prevention:/{sub(/^prevention: */,""); gsub(/^["'"'"']|["'"'"']$/,""); print; exit}' "$_file" 2>/dev/null)
    [ -n "$_rc" ] && _body="Root cause: ${_rc}"
    [ -n "$_prev" ] && _body="${_body:+${_body}
}Prevention: ${_prev}"
  fi
  [ -z "$_body" ] && continue
  OUTPUT="${OUTPUT}

### ${_slug} (matched: \"${_phrase}\")
${_body}"
  NEW_INJECTED="${NEW_INJECTED}${_slug}
"
done <<< "$SORTED"

# --- WAL: log trigger-match events ---

if [ -n "$NEW_INJECTED" ]; then
  {
    while IFS= read -r _slug; do
      [ -z "$_slug" ] && continue
      printf '%s|trigger-match|%s|%s\n' "$TODAY" "$_slug" "$SAFE_SESSION_ID"
    done <<< "$NEW_INJECTED"
  } >> "$WAL_FILE" 2>/dev/null || true

  # Append to dedup list so repeated prompts don't re-inject
  {
    printf '%s' "$NEW_INJECTED"
  } >> "$DEDUP_FILE" 2>/dev/null || true
fi

# --- Emit hookSpecificOutput JSON ---

cat <<EOF
{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":$(printf '%s\n' "$OUTPUT" | jq -Rs .)}}
EOF

exit 0
