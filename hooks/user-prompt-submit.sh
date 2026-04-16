#!/bin/bash
# UserPromptSubmit hook — inject memory files matching prompt triggers (v0.5)
set -o pipefail

# LC_ALL=C keeps awk/perl printf float output using '.' as decimal separator
# (audit-2026-04-16 R1).
export LC_ALL=C
# S5 (audit-2026-04-16): session-private files created mode 0600.
umask 077

# Scans memory/*/*.md frontmatter for `trigger:` (single) and `triggers:` (array),
# matches case-insensitive substring against the prompt, and injects up to
# MAX_TRIGGERED matching files as a "## Triggered (from prompt)" section.
#
# Dedup: skips files already injected at SessionStart (tracked in /tmp).
# WAL: logs `trigger-match|<slug>|<session_id>` per match.
# Graceful degradation: any error → exit 0 with no output.

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"

# User-overridable runtime config (templates/.config.sh.example)
# SECURITY: do NOT source .config.sh as shell — it is LLM-writable.
# Parse only whitelisted integer keys via load_memory_config (see load-config.sh).
# shellcheck source=lib/load-config.sh
. "$(dirname "$0")/lib/load-config.sh" 2>/dev/null || true
load_memory_config "$MEMORY_DIR/.config.sh"
MAX_TRIGGERED=${MAX_TRIGGERED:-4}
TODAY=$(date +%Y-%m-%d)
NOW_EPOCH=$(date +%s)

# --- Parse hook input ---

INPUT=$(cat)
SESSION_ID=$(printf '%s' "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)
PROMPT=$(printf '%s' "$INPUT" | jq -r '.prompt // empty' 2>/dev/null)
CWD=$(printf '%s' "$INPUT" | jq -r '.cwd // empty' 2>/dev/null)

[ -z "$SESSION_ID" ] && exit 0
[ -z "$PROMPT" ] && exit 0
[ -d "$MEMORY_DIR" ] || exit 0

# Detect current project from cwd via projects.json (matches session-start logic).
CURRENT_PROJECT=""
PROJECTS_JSON="$MEMORY_DIR/projects.json"
if [ -n "$CWD" ] && [ -f "$PROJECTS_JSON" ] && jq empty "$PROJECTS_JSON" 2>/dev/null; then
  while IFS= read -r prefix; do
    case "$CWD" in
      "$prefix"|"$prefix"/*)
        CURRENT_PROJECT=$(jq -r --arg k "$prefix" '.[$k] // empty' "$PROJECTS_JSON" 2>/dev/null)
        break ;;
    esac
  done < <(jq -r 'to_entries | sort_by(.key | length) | reverse | .[].key' "$PROJECTS_JSON" 2>/dev/null)
fi

# Strip fenced code blocks, inline backtick code, and blockquote lines before matching.
# This prevents triggers from matching quoted examples like `"tailwind hsl"` or meta-text
# inside ``` fences (the 2026-04-12 false-positive that motivated this fix).
CLEAN_PROMPT=$(printf '%s' "$PROMPT" | awk '
  /^[[:space:]]*```/ { in_fence = !in_fence; next }
  in_fence { next }
  /^[[:space:]]*>/ { next }
  { print }
' | sed 's/`[^`]*`//g')
LOWER_PROMPT=$(printf '%s' "$CLEAN_PROMPT" | tr '[:upper:]' '[:lower:]')

SAFE_SESSION_ID="${SESSION_ID//|/_}"
SAFE_SESSION_ID="${SAFE_SESSION_ID//\//_}"
RUNTIME_DIR="$MEMORY_DIR/.runtime"
DEDUP_FILE="$RUNTIME_DIR/injected-${SAFE_SESSION_ID}.list"
WAL_FILE="$MEMORY_DIR/.wal"
mkdir -p "$RUNTIME_DIR" 2>/dev/null || true

# shellcheck source=lib/wal-lock.sh
. "$(dirname "$0")/lib/wal-lock.sh" 2>/dev/null || true

# --- Extract frontmatter fields from a memory file ---
# Output (tab-separated, single line):
#   status<TAB>severity<TAB>recurrence<TAB>ref_count<TAB>project<TAB>trigger_joined
# where trigger_joined is \x1f-separated phrases (trigger + triggers[]).
extract_meta() {
  local file="$1"
  awk '
    BEGIN { fm_count = 0; printed = 0; status = ""; severity = ""; recurrence = 0; ref_count = 0; project = ""; created = ""; trig_single = ""; in_trigs = 0 }
    # R5: strip CRLF — mirror parse-memory.sh so Windows-authored files do
    # not report SCHEMA_ERROR universally. (audit-2026-04-16 R5)
    { gsub(/\r/, "") }
    /^---$/ { fm_count++; if (fm_count >= 2) { print_out(); printed = 1; exit } next }
    fm_count != 1 { next }
    /^status:/        { val = $0; sub(/^status: */, "", val); gsub(/["'"'"']/, "", val); status = val; next }
    /^severity:/      { val = $0; sub(/^severity: */, "", val); gsub(/["'"'"']/, "", val); severity = val; next }
    /^recurrence:/    { val = $0; sub(/^recurrence: */, "", val); recurrence = val + 0; next }
    /^ref_count:/     { val = $0; sub(/^ref_count: */, "", val); ref_count = val + 0; next }
    /^project:/       { val = $0; sub(/^project: */, "", val); gsub(/["'"'"']/, "", val); project = val; next }
    /^created:/       { val = $0; sub(/^created: */, "", val); gsub(/["'"'"']/, "", val); created = val; next }
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
    END {
      if (fm_count < 2) { printf "SCHEMA_ERROR"; exit }
      if (!printed) print_out()
    }
    function print_out(   i, joined, SEP, FS_OUT) {
      SEP = "\x1f"
      # Field separator: \x1e (RS). Non-whitespace so IFS read preserves
      # empty fields (tabs would be collapsed as whitespace IFS).
      FS_OUT = "\x1e"
      joined = ""
      if (trig_single != "") joined = trig_single
      for (i = 1; i <= trig_count; i++) {
        if (joined != "") joined = joined SEP trig_arr[i]
        else joined = trig_arr[i]
      }
      printf "%s%s%s%s%d%s%d%s%s%s%s%s%s", status, FS_OUT, severity, FS_OUT, recurrence, FS_OUT, ref_count, FS_OUT, project, FS_OUT, created, FS_OUT, joined
    }
  ' "$file"
}

# --- Case-insensitive substring match with negation context check ---
# Returns 0 if any phrase matches $LOWER_PROMPT without negation in ±40 char window.
# Echoes the matched phrase on stdout.
match_prompt() {
  local phrases="$1"
  local phrase lower_phrase before after context
  # Phrases separated by \x1f (Unit Separator) to avoid collision with newlines in values
  local IFS=$'\x1f'
  for phrase in $phrases; do
    [ -z "$phrase" ] && continue
    lower_phrase=$(printf '%s' "$phrase" | tr '[:upper:]' '[:lower:]')
    case "$LOWER_PROMPT" in
      *"$lower_phrase"*) ;;
      *) continue ;;
    esac
    # Extract ±40 chars around match for negation sniffing.
    # Bash 3.2 (macOS default) returns "" for `${var: -N}` when N > len; compute offset explicitly.
    before="${LOWER_PROMPT%%${lower_phrase}*}"
    after="${LOWER_PROMPT#*${lower_phrase}}"
    _boff=$(( ${#before} - 40 ))
    [ "$_boff" -lt 0 ] && _boff=0
    context=" ${before:$_boff}${lower_phrase}${after:0:40} "
    # Skip if negated nearby — ru: не/без/уже/нет/игнор, en: already/fixed/skip/no/don't/ignore/without
    case "$context" in
      *" не "*|*" без "*|*" уже "*|*" нет "*|*" нету "*|*" игнор"*|\
      *" already "*|*" fixed "*|*" skip "*|*" no "*|*"don't"*|*"dont "*|*"dont"|\
      *" ignore "*|*" ignore"|*" without "*)
        continue ;;
    esac
    printf '%s' "$phrase"
    return 0
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

for _dir in mistakes feedback strategies knowledge notes seeds; do
  [ -d "$MEMORY_DIR/$_dir" ] || continue
  for _f in "$MEMORY_DIR/$_dir"/*.md; do
    [ -f "$_f" ] || continue
    # P5: param expansion instead of basename subshell.
    _slug="${_f##*/}"; _slug="${_slug%.md}"

    # Dedup: skip if already in SessionStart injection
    case "$DEDUP_SLUGS" in *" ${_slug} "*) continue ;; esac

    # Extract metadata + triggers
    _meta=$(extract_meta "$_f" 2>/dev/null)
    [ -z "$_meta" ] && continue

    # Schema validation: malformed frontmatter → log once per day, skip
    if [ "$_meta" = "SCHEMA_ERROR" ]; then
      if ! grep -q "^${TODAY}|schema-error|${_slug}|" "$WAL_FILE" 2>/dev/null; then
        printf '%s|schema-error|%s|%s\n' "$TODAY" "$_slug" "$SAFE_SESSION_ID" |
          wal_run_locked bash -c 'cat >> "$1"' _ "$WAL_FILE" 2>/dev/null || true
      fi
      continue
    fi

    # P2: one IFS read replaces 7 printf|cut subshells (~20ms -> ~0.3ms per
    # file). Uses \x1e (RS) as field separator — non-whitespace so
    # consecutive empties (project+created often blank) are not collapsed.
    # (audit-2026-04-16 P2)
    IFS=$'\x1e' read -r _status _severity _recurrence _ref_count _project _created _phrases <<< "$_meta"

    # Status filter
    case "$_status" in active|pinned) ;; *) continue ;; esac
    # Skip if no triggers
    [ -z "$_phrases" ] && continue

    # Match phrases against prompt
    _matched=$(match_prompt "$_phrases") || continue

    # Build priority key (higher = better, sort -r)
    # Priority order: project → status → severity → recency → log_ref_count → recurrence → ref_count
    # Recency boost lets fresh records surface above old popular ones within same severity.
    # ref_count is log10-bucketed so 100 isn't 100x better than 1 — diminishing returns.
    _project_rank=0
    if [ -z "$_project" ] || [ "$_project" = "global" ]; then
      _project_rank=1
    elif [ -n "$CURRENT_PROJECT" ] && [ "$_project" = "$CURRENT_PROJECT" ]; then
      _project_rank=2
    fi
    _status_rank=0
    [ "$_status" = "pinned" ] && _status_rank=1
    _sev_rank=0
    case "$_severity" in major) _sev_rank=2 ;; minor) _sev_rank=1 ;; esac

    # Recency rank from `created` — newer wins within same severity
    # C8 (audit-2026-04-16): a future-dated `created:` (typo like `2099-01-01`
    # or clock-skew) yields a negative _age_days that satisfies `-le 7` and
    # silently granted the maximum recency_rank. Treat negative ages as
    # rank 0 so obviously-wrong dates lose the boost instead of winning it.
    _recency_rank=0
    if [ -n "$_created" ]; then
      if [[ "$OSTYPE" == darwin* ]]; then
        _created_sec=$(date -j -f "%Y-%m-%d" "$_created" +%s 2>/dev/null)
      else
        _created_sec=$(date -d "$_created" +%s 2>/dev/null)
      fi
      if [ -n "$_created_sec" ]; then
        _age_days=$(( (NOW_EPOCH - _created_sec) / 86400 ))
        if [ "$_age_days" -lt 0 ]; then
          _recency_rank=0
        elif [ "$_age_days" -le 7 ]; then
          _recency_rank=3
        elif [ "$_age_days" -le 30 ]; then
          _recency_rank=2
        elif [ "$_age_days" -le 90 ]; then
          _recency_rank=1
        fi
      fi
    fi

    # log10-bucket of ref_count (0=1-9, 1=10-99, 2=100-999, 3=1000+).
    # C5 (audit-2026-04-16): earlier implementation used `log(r+1)/log(10)`
    # which rolls every decade one bucket early (r=9 → bucket 1, r=99 →
    # bucket 2). Plain `log(r)/log(10)` is correct mathematically but
    # `log(1000)/log(10)` evaluates to 2.999999… in awk, so `int()` would
    # return 2 for a clean-decade value. Add a small epsilon before
    # truncating so exact decade boundaries round to the next bucket.
    _log_ref=$(awk -v r="$_ref_count" 'BEGIN{if (r < 1) { print 0; exit } v=log(r)/log(10); print int(v + 1e-9)}')

    # Zero-pad for lex sort (descending)
    _key=$(printf '%d.%d.%d.%d.%d.%06d.%06d' "$_project_rank" "$_status_rank" "$_sev_rank" "$_recency_rank" "$_log_ref" "$_recurrence" "$_ref_count")

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
  # Batch all WAL appends under single lock to avoid interleaving with other sessions.
  wal_run_locked bash -c '
    while IFS= read -r _slug; do
      [ -z "$_slug" ] && continue
      printf "%s|trigger-match|%s|%s\n" "$0" "$_slug" "$1" >> "$2"
    done <<< "$3"
  ' "$TODAY" "$SAFE_SESSION_ID" "$WAL_FILE" "$NEW_INJECTED" 2>/dev/null || true

  # Append to dedup list so repeated prompts don't re-inject
  wal_run_locked bash -c 'printf "%s" "$1" >> "$2"' _ "$NEW_INJECTED" "$DEDUP_FILE" 2>/dev/null || true
fi

# --- Emit hookSpecificOutput JSON ---

cat <<EOF
{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":$(printf '%s\n' "$OUTPUT" | jq -Rs .)}}
EOF

exit 0
