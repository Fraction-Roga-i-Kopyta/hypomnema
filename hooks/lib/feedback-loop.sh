# hooks/lib/feedback-loop.sh
# shellcheck shell=bash
# Post-session trigger-useful / trigger-silent classification.
#
# Walks every inject + trigger-match event for this session, extracts
# assistant text from the transcript, and classifies each injected slug
# as `trigger-useful` (evidence phrase present in assistant output) or
# `trigger-silent` (none matched). Writes `evidence-missing` when the
# memory file can't be located, `evidence-empty` when body mining
# yields no tokens.
#
# Evidence source precedence, mirroring the pre-refactor inline block:
#   1. `evidence:` frontmatter field — threshold: ≥1 phrase.
#   2. Body mining (lib/evidence-extract.sh) — threshold: ≥2 unique tokens.
#
# Depends on caller having already sourced:
#   - lib/wal-lock.sh         (wal_run_locked)
#   - lib/evidence-extract.sh (evidence_from_frontmatter, evidence_from_body)
# and set:
#   $MEMORY_DIR, $WAL_FILE, $SAFE_SESSION_ID, $TRANSCRIPT_PATH, $TODAY

classify_feedback_from_transcript() {
  [ -f "$WAL_FILE" ] || return 0
  [ -n "$SAFE_SESSION_ID" ] || return 0
  [ -n "$TRANSCRIPT_PATH" ] || return 0
  [ -f "$TRANSCRIPT_PATH" ] || return 0

  local _INJECTED_ALL
  _INJECTED_ALL=$(wal_run_locked awk -F'|' -v sid="$SAFE_SESSION_ID" '
    $4 == sid && ($2 == "inject" || $2 == "trigger-match") { print $3 }
  ' "$WAL_FILE" 2>/dev/null | sort -u)
  [ -n "$_INJECTED_ALL" ] || return 0

  local _ASSIST_TEXT _ASSIST_TEXT_LC
  _ASSIST_TEXT=$(awk '
    /"type":"assistant"/ && /"text":/ {
      t = $0
      while (match(t, /"text":"[^"]*"/)) {
        print substr(t, RSTART+8, RLENGTH-9)
        t = substr(t, RSTART+RLENGTH)
      }
    }
  ' "$TRANSCRIPT_PATH" 2>/dev/null)
  _ASSIST_TEXT_LC=$(printf '%s' "$_ASSIST_TEXT" | tr '[:upper:]' '[:lower:]')

  local _FEEDBACK_LINES="" _DIAG_LINES=""
  local _inj_slug _MEM_FILE _EVID _THRESHOLD _HITS _SEEN _phrase _phrase_lc
  while IFS= read -r _inj_slug; do
    [ -z "$_inj_slug" ] && continue

    _MEM_FILE=$(find "$MEMORY_DIR" -maxdepth 3 -name "${_inj_slug}.md" 2>/dev/null | head -1)
    if [ -z "$_MEM_FILE" ]; then
      _DIAG_LINES="${_DIAG_LINES}${TODAY}|evidence-missing|${_inj_slug}|${SAFE_SESSION_ID}
"
      _FEEDBACK_LINES="${_FEEDBACK_LINES}${TODAY}|trigger-silent|${_inj_slug}|${SAFE_SESSION_ID}
"
      continue
    fi

    _EVID=$(evidence_from_frontmatter "$_MEM_FILE" 2>/dev/null)
    if [ -n "$_EVID" ]; then
      _THRESHOLD=1
    else
      _EVID=$(evidence_from_body "$_MEM_FILE" 2>/dev/null)
      if [ -z "$_EVID" ]; then
        _DIAG_LINES="${_DIAG_LINES}${TODAY}|evidence-empty|${_inj_slug}|${SAFE_SESSION_ID}
"
        _FEEDBACK_LINES="${_FEEDBACK_LINES}${TODAY}|trigger-silent|${_inj_slug}|${SAFE_SESSION_ID}
"
        continue
      fi
      _THRESHOLD=2
    fi

    _HITS=0
    _SEEN=""
    while IFS= read -r _phrase; do
      [ -z "$_phrase" ] && continue
      _phrase_lc=$(printf '%s' "$_phrase" | tr '[:upper:]' '[:lower:]')
      case "|$_SEEN|" in *"|$_phrase_lc|"*) continue ;; esac
      if printf '%s' "$_ASSIST_TEXT_LC" | grep -qF "$_phrase_lc" 2>/dev/null; then
        _HITS=$((_HITS + 1))
        _SEEN="${_SEEN}|${_phrase_lc}"
      fi
    done <<< "$_EVID"

    if [ "$_HITS" -ge "$_THRESHOLD" ]; then
      _FEEDBACK_LINES="${_FEEDBACK_LINES}${TODAY}|trigger-useful|${_inj_slug}|${SAFE_SESSION_ID}
"
    else
      _FEEDBACK_LINES="${_FEEDBACK_LINES}${TODAY}|trigger-silent|${_inj_slug}|${SAFE_SESSION_ID}
"
    fi
  done <<< "$_INJECTED_ALL"

  if [ -n "$_FEEDBACK_LINES" ] || [ -n "$_DIAG_LINES" ]; then
    wal_run_locked bash -c 'printf "%s%s" "$1" "$2" >> "$3"' _ \
      "$_FEEDBACK_LINES" "$_DIAG_LINES" "$WAL_FILE" 2>/dev/null || true
  fi
}
