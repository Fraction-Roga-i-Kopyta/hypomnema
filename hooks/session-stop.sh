#!/bin/bash
# Stop hook — remind Claude to record if work happened but nothing was saved
# Dependencies: jq, find, stat
# Graceful degradation: any error → exit 0

set -o pipefail

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
MIN_SESSION_SECONDS=120  # 2 minutes
WAL_FILE="$MEMORY_DIR/.wal"

# shellcheck source=lib/wal-lock.sh
. "$(dirname "$0")/lib/wal-lock.sh" 2>/dev/null || true
# shellcheck source=lib/stat-helpers.sh
. "$(dirname "$0")/lib/stat-helpers.sh" 2>/dev/null || true

INPUT=$(cat)
SESSION_ID=$(printf '%s\n' "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)

[ -z "$SESSION_ID" ] && exit 0

SAFE_MARKER_ID="${SESSION_ID//\//_}"
SAFE_SESSION_ID="${SESSION_ID//|/_}"
SAFE_SESSION_ID="${SAFE_SESSION_ID//\//_}"
MARKER="/tmp/.claude-session-${SAFE_MARKER_ID}"

# --- Lifecycle rotation (always runs, even without session marker) ---
lifecycle_rotate() {
  local ARCHIVE_DIR="$MEMORY_DIR/archive"
  local NOW_SEC=$(date +%s)

  # Decay rates per type (days)
  # Type      | stale_days | archive_days
  # mistakes  | 60         | 180
  # strategies| 90         | 180
  # knowledge | 90         | 365
  # feedback  | 45         | 120
  # notes     | 30         | 90
  # decisions | 90         | 365
  # journal   | 30         | 90
  # projects  | never      | never (excluded)

  # Collect all candidate files
  local all_files=()
  for subdir in mistakes feedback knowledge notes strategies decisions journal; do
    while IFS= read -r f; do
      all_files+=("$f")
    done < <(find "$MEMORY_DIR/$subdir" -name "*.md" -type f 2>/dev/null)
  done
  [ ${#all_files[@]} -gt 0 ] || return 0

  local rotation_today rotation_stale_count=0 rotation_archive_count=0 rotation_checked=${#all_files[@]}
  rotation_today=$(date +%Y-%m-%d)

  # Single-pass awk: extract status, referenced, created for all files
  awk '
  { gsub(/\r/, "") }
  FNR == 1 {
    if (prev_file != "") printf "%s\t%s\t%s\t%s\t%s\n", prev_file, status, ref, created, decay
    in_fm = 0; n = 0; status = "_empty_"; ref = ""; created = ""; decay = ""; prev_file = FILENAME
  }
  /^---$/ { n++; if (n==1) in_fm=1; if (n==2) in_fm=0 }
  in_fm && /^status:/ { sub(/^status: */, ""); status = $0 }
  in_fm && /^referenced:/ { sub(/^referenced: */, ""); ref = $0 }
  in_fm && /^created:/ { sub(/^created: */, ""); created = $0 }
  in_fm && /^decay_rate:/ { sub(/^decay_rate: */, ""); decay = $0 }
  END { if (prev_file != "") printf "%s\t%s\t%s\t%s\t%s\n", prev_file, status, ref, created, decay }
  ' "${all_files[@]}" 2>/dev/null | while IFS=$'\t' read -r f file_status ref_date created_date file_decay; do
    [ "$file_status" = "pinned" ] && continue
    [ "$file_status" = "archived" ] && continue

    [ -z "$ref_date" ] && ref_date="$created_date"
    [ -z "$ref_date" ] && continue

    local ref_sec
    if [[ "$OSTYPE" == darwin* ]]; then
      ref_sec=$(date -j -f "%Y-%m-%d" "$ref_date" +%s 2>/dev/null) || continue
    else
      ref_sec=$(date -d "$ref_date" +%s 2>/dev/null) || continue
    fi
    local age=$(( (NOW_SEC - ref_sec) / 86400 ))

    # decay_rate from frontmatter overrides type-based defaults
    local stale_days=30 archive_days=90
    case "$file_decay" in
      slow)   stale_days=180; archive_days=365 ;;
      fast)   stale_days=14;  archive_days=45  ;;
      normal|"")
        case "$f" in
          */mistakes/*)   stale_days=60;  archive_days=180 ;;
          */strategies/*) stale_days=90;  archive_days=180 ;;
          */knowledge/*)  stale_days=90;  archive_days=365 ;;
          */feedback/*)   stale_days=45;  archive_days=120 ;;
          */notes/*)      stale_days=30;  archive_days=90  ;;
          */decisions/*)  stale_days=90;  archive_days=365 ;;
          */journal/*)    stale_days=30;  archive_days=90  ;;
        esac
        ;;
    esac

    if [ "$file_status" = "stale" ] && [ "$age" -gt "$archive_days" ]; then
      local rel="${f#$MEMORY_DIR/}"
      mkdir -p "$ARCHIVE_DIR/$(dirname "$rel")"
      if mv "$f" "$ARCHIVE_DIR/$rel" 2>/dev/null; then
        rotation_archive_count=$((rotation_archive_count + 1))
        wal_append "$rotation_today|rotation-archive|$rel|age_${age}d" 2>/dev/null
      fi
      continue
    fi

    if [ "$age" -gt "$stale_days" ] && [ "$file_status" = "active" ]; then
      if perl -pi -e '
        $in_fm = 1 if /^---$/ && !$seen_fm++;
        $in_fm = 0 if /^---$/ && $seen_fm > 1;
        s/^status: active/status: stale/ if $in_fm;
        if (eof) { $seen_fm = 0; $in_fm = 0; }
      ' "$f" 2>/dev/null; then
        local rel="${f#$MEMORY_DIR/}"
        rotation_stale_count=$((rotation_stale_count + 1))
        wal_append "$rotation_today|rotation-stale|$rel|age_${age}d" 2>/dev/null
      fi
    fi
  done

  # Archive files without valid frontmatter if older than 90 days by mtime
  for f in "${all_files[@]}"; do
    head_line=$(head -1 "$f" 2>/dev/null)
    [ "$head_line" = "---" ] && continue
    fmtime=$(_stat_mtime "$f")
    local fage=$(( (NOW_SEC - fmtime) / 86400 ))
    if [ "$fage" -gt 90 ]; then
      local rel="${f#$MEMORY_DIR/}"
      mkdir -p "$ARCHIVE_DIR/$(dirname "$rel")"
      if mv "$f" "$ARCHIVE_DIR/$rel" 2>/dev/null; then
        rotation_archive_count=$((rotation_archive_count + 1))
        wal_append "$rotation_today|rotation-archive|$rel|no_frontmatter_age_${fage}d" 2>/dev/null
      fi
    fi
  done

  # Single summary event per rotation run — easy to grep/verify
  wal_append "$rotation_today|rotation-summary|checked_${rotation_checked}|stale_${rotation_stale_count},archived_${rotation_archive_count}" 2>/dev/null
}

lifecycle_rotate 2>/dev/null || true

# Session-specific processing requires marker
[ -f "$MARKER" ] || exit 0

# Check session duration
MARKER_TIME=$(_stat_mtime "$MARKER")
NOW=$(date +%s)
ELAPSED=$((NOW - MARKER_TIME))

# Short session — skip
if [ "$ELAPSED" -lt "$MIN_SESSION_SECONDS" ]; then
  rm -f "$MARKER" 2>/dev/null
  exit 0
fi

# Rebuild TF-IDF index if memory files changed or index missing
MODIFIED_FOR_INDEX=$(find "$MEMORY_DIR" -name "*.md" -newer "$MARKER" -not -name "MEMORY.md" -not -name "_agent_context.md" 2>/dev/null | head -1)
if [ -n "$MODIFIED_FOR_INDEX" ] || [ ! -f "$MEMORY_DIR/.tfidf-index" ]; then
  bash "$(dirname "$0")/memory-index.sh" 2>/dev/null &
  disown 2>/dev/null || true
fi

# Rebuild analytics report if missing or stale (>7 days)
WAL_FILE="$MEMORY_DIR/.wal"
ANALYTICS_REPORT="$MEMORY_DIR/.analytics-report"
ANALYTICS_STALE=0
if [ ! -f "$ANALYTICS_REPORT" ]; then
  ANALYTICS_STALE=1
elif [ -f "$WAL_FILE" ]; then
  ar_mtime=$(_stat_mtime "$ANALYTICS_REPORT")
  ar_age=$(( (NOW - ar_mtime) / 86400 ))
  [ "$ar_age" -gt 7 ] && ANALYTICS_STALE=1
fi
if [ "$ANALYTICS_STALE" -eq 1 ] && [ -f "$WAL_FILE" ]; then
  ANALYTICS_SCRIPT="$(dirname "$0")/memory-analytics.sh"
  if [ -f "$ANALYTICS_SCRIPT" ]; then
    CLAUDE_MEMORY_DIR="$MEMORY_DIR" bash "$ANALYTICS_SCRIPT" 2>/dev/null &
    disown 2>/dev/null || true
  fi
fi

# --- Auto-generate continuity from git state ---
CWD=$(printf '%s\n' "$INPUT" | jq -r '.cwd // empty' 2>/dev/null)
[ -z "$CWD" ] && CWD="$PWD"

# Detect project (same logic as session-start)
PROJECT=""
PROJECTS_JSON="$MEMORY_DIR/projects.json"
if [ -f "$PROJECTS_JSON" ] && jq empty "$PROJECTS_JSON" 2>/dev/null; then
  while read -r prefix; do
    case "$CWD" in
      "${prefix}"|"${prefix}"/*)
        PROJECT=$(jq -r --arg k "$prefix" '.[$k]' "$PROJECTS_JSON" 2>/dev/null)
        break ;;
    esac
  done < <(jq -r 'to_entries | sort_by(.key | length) | reverse | .[].key' "$PROJECTS_JSON" 2>/dev/null)
fi

# Generate git-based continuity if in a git repo with a known project
if [ -n "$PROJECT" ] && { [ -d "$CWD/.git" ] || git -C "$CWD" rev-parse --git-dir >/dev/null 2>&1; }; then
  CONT_FILE="$MEMORY_DIR/continuity/${PROJECT}.md"
  CONT_STALE=0

  # Check if continuity is stale (older than session marker or missing)
  if [ ! -f "$CONT_FILE" ]; then
    CONT_STALE=1
  elif [ -f "$MARKER" ]; then
    CONT_OLDER=$(find "$CONT_FILE" ! -newer "$MARKER" 2>/dev/null)
    [ -n "$CONT_OLDER" ] && CONT_STALE=1
  fi

  if [ "$CONT_STALE" = "1" ]; then
    branch=$(git -C "$CWD" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")
    last_commit=$(git -C "$CWD" log -1 --oneline 2>/dev/null || echo "no commits")
    uncommitted=$(git -C "$CWD" diff --stat --no-color 2>/dev/null | tail -1)
    staged=$(git -C "$CWD" diff --cached --stat --no-color 2>/dev/null | tail -1)

    # Only write git state section, preserve existing manual content
    GIT_STATE="Branch: ${branch} | Last: ${last_commit}"
    [ -n "$uncommitted" ] && GIT_STATE="${GIT_STATE} | Uncommitted: ${uncommitted}"
    [ -n "$staged" ] && GIT_STATE="${GIT_STATE} | Staged: ${staged}"

    mkdir -p "$MEMORY_DIR/continuity"
    if [ -f "$CONT_FILE" ]; then
      # Append git state if not already there
      if ! grep -q "^## Git State" "$CONT_FILE" 2>/dev/null; then
        printf '\n## Git State\n%s\n' "$GIT_STATE" >> "$CONT_FILE"
      else
        # Update existing git state section — safe from special chars
        awk -v state="$GIT_STATE" '
          /^## Git State/ { print; print state; skip=1; next }
          skip && /^## / { skip=0 }
          !skip { print }
        ' "$CONT_FILE" > "$CONT_FILE.tmp" && mv "$CONT_FILE.tmp" "$CONT_FILE"
      fi
    else
      cat > "$CONT_FILE" << CONTEOF
---
type: continuity
project: $PROJECT
created: $(date +%Y-%m-%d)
status: active
---

## Git State
$GIT_STATE
CONTEOF
    fi
  fi
fi

# Check if any memory files were modified during this session
MODIFIED=$(find "$MEMORY_DIR" -name "*.md" -newer "$MARKER" -not -name "MEMORY.md" -not -name "_agent_context.md" 2>/dev/null | head -1)

# --- Transcript error detection ---
TRANSCRIPT_PATH=$(printf '%s\n' "$INPUT" | jq -r '.transcript_path // empty' 2>/dev/null)
ERROR_SUMMARY=""
if [ -n "$TRANSCRIPT_PATH" ] && [ -f "$TRANSCRIPT_PATH" ]; then
  EXCLUDES_FILE="$MEMORY_DIR/.confidence-excludes"
  EXCLUDES_PATTERN=""
  if [ -f "$EXCLUDES_FILE" ]; then
    EXCLUDES_PATTERN=$(tr '\n' '|' < "$EXCLUDES_FILE" | sed 's/|$//' | sed 's/\[/\\[/g')
  fi
  ERROR_SUMMARY=$(awk -v excludes="$EXCLUDES_PATTERN" '
  /"is_error":true/ {
    content = $0; sub(/.*"content":"/, "", content); sub(/"[,}].*/, "", content)
    gsub(/\\n/, " ", content)
    tid = $0; sub(/.*"tool_use_id":"/, "", tid); sub(/".*/, "", tid)
    if (tid != "") error_tids[tid] = content
  }
  /"name":"Bash"/ && /"tool_use"/ {
    tid = $0
    if (match(tid, /"id":"[^"]+"/)) tid = substr(tid, RSTART+6, RLENGTH-7)
    cmd = $0; sub(/.*"command":"/, "", cmd); sub(/"[,}].*/, "", cmd)
    gsub(/\\n/, " ", cmd); gsub(/\\"/, "\"", cmd)
    if (tid != "") commands[tid] = cmd
  }
  END {
    for (tid in error_tids) {
      cmd = commands[tid]; if (cmd == "") continue
      split(cmd, words, " "); fw = words[1]; sub(/.*\//, "", fw)
      if (excludes != "" && fw ~ "^(" excludes ")$") continue
      err = error_tids[tid]
      if (err ~ /PASSED|FAILED|pytest|jest|vitest|rspec/) continue
      score = 0.5
      if (err ~ /[Ee]rror|[Ff]atal|[Pp]anic|[Tt]raceback/) score += 0.3
      if (err ~ /[Ww]arning|[Dd]eprecated/) score += 0.1
      cmd_counts[fw]++; if (cmd_counts[fw] > 1) score += 0.2
      if (score >= 0.7) level = "HIGH"
      else if (score >= 0.4) level = "MEDIUM"
      else continue
      if (!(fw in best_level) || level == "HIGH") {
        best_level[fw] = level; best_cmd[fw] = substr(cmd, 1, 60); error_count[fw] = cmd_counts[fw]
      }
    }
    for (fw in best_level) printf "%s|%s|x%d\n", best_cmd[fw], best_level[fw], error_count[fw]
  }
  ' "$TRANSCRIPT_PATH" 2>/dev/null)
fi

REMINDER=""
if [ -z "$MODIFIED" ]; then
  REMINDER="[MEMORY CHECKPOINT] Session lasted >2 min but nothing was recorded.
If there's anything to save — mistakes, decisions, journal — do it now."
fi

# Structured continuity prompt with git context
if [ "${CONT_STALE:-0}" = "1" ] && [ -n "$PROJECT" ]; then
  cont_branch=$(git -C "$CWD" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")
  cont_commit=$(git -C "$CWD" log -1 --oneline 2>/dev/null || echo "no commits")
  REMINDER="${REMINDER}${REMINDER:+
}[CONTINUITY] Заполни continuity/${PROJECT}.md:

Задача: ___
Статус: ___
Следующий шаг: ___

Контекст: ветка \`${cont_branch}\`, последний коммит: \"${cont_commit}\""
fi

if [ -n "$ERROR_SUMMARY" ]; then
  ERROR_LINES=""
  while IFS='|' read -r cmd level count; do
    [ -z "$cmd" ] && continue
    ERROR_LINES="${ERROR_LINES}
- \`${cmd}\` (${count}, confidence: ${level})"
    first_word=$(echo "$cmd" | awk '{print $1}' | sed 's/.*\///')
    wal_append "$(date +%Y-%m-%d)|error-detected-$level|$first_word|$SAFE_SESSION_ID" "error-detected-$level|$first_word|$SAFE_SESSION_ID"
  done <<< "$ERROR_SUMMARY"
  if [ -n "$ERROR_LINES" ]; then
    REMINDER="${REMINDER}${REMINDER:+
}[ERRORS] Обнаружены ошибки в этой сессии:${ERROR_LINES}
Запиши в mistakes/ если это повторяющийся паттерн."
  fi
fi

# --- Outcome-positive detection ---
# Injected mistakes that were NOT repeated → positive signal
TODAY=$(date +%Y-%m-%d)
WAL_FILE="$MEMORY_DIR/.wal"
if [ -f "$WAL_FILE" ] && [ -n "$SAFE_SESSION_ID" ]; then
  INJECTED_MISTAKES=$(wal_run_locked awk -F'|' -v sid="$SAFE_SESSION_ID" '
    $4 == sid && $2 == "inject" { print $3 }
  ' "$WAL_FILE" 2>/dev/null)

  NEGATIVE_MISTAKES=$(wal_run_locked awk -F'|' -v sid="$SAFE_SESSION_ID" '
    $4 == sid && $2 == "outcome-negative" { print $3 }
  ' "$WAL_FILE" 2>/dev/null)

  if [ -n "$INJECTED_MISTAKES" ]; then
    # Detect session domains for domain filtering
    SESSION_DOMAINS=""
    if [ -n "$CWD" ]; then
      if [ -d "$CWD/.git" ] || git -C "$CWD" rev-parse --git-dir >/dev/null 2>&1; then
        SESSION_DOMAINS=$(git -C "$CWD" diff --name-only HEAD 2>/dev/null | awk -F. '
          NF > 1 {
            ext = tolower($NF)
            if (ext == "css" || ext == "scss") print "css"
            else if (ext == "tsx" || ext == "jsx" || ext == "ts" || ext == "js") print "frontend"
            else if (ext == "py" || ext == "go" || ext == "java") print "backend"
            else if (ext == "sql") print "db"
            else if (ext == "groovy") print "groovy"
          }' | sort -u | tr '\n' ' ')
      fi
    fi

    while IFS= read -r mistake_name; do
      [ -z "$mistake_name" ] && continue
      if printf '%s\n' "$NEGATIVE_MISTAKES" | grep -qx "$mistake_name" 2>/dev/null; then
        continue
      fi
      MISTAKE_FILE="$MEMORY_DIR/mistakes/${mistake_name}.md"
      [ -f "$MISTAKE_FILE" ] || continue

      # Domain filter: skip if mistake domains don't intersect with session domains
      if [ -n "$SESSION_DOMAINS" ]; then
        MISTAKE_DOMAINS=$(awk '
          /^---$/ { n++ }
          n==1 && /^domains:/ { sub(/^domains: *\[?/, ""); sub(/\].*/, ""); gsub(/, */, " "); print; exit }
        ' "$MISTAKE_FILE" 2>/dev/null)

        if [ -n "$MISTAKE_DOMAINS" ] && [ "$MISTAKE_DOMAINS" != "general" ]; then
          DOMAIN_MATCH=0
          for md in $MISTAKE_DOMAINS; do
            [ "$md" = "general" ] && { DOMAIN_MATCH=1; break; }
            for sd in $SESSION_DOMAINS; do
              [ "$md" = "$sd" ] && { DOMAIN_MATCH=1; break 2; }
            done
          done
          [ "$DOMAIN_MATCH" -eq 0 ] && continue
        fi
      fi

      wal_append "$TODAY|outcome-positive|$mistake_name|$SAFE_SESSION_ID" "outcome-positive|$mistake_name|$SAFE_SESSION_ID"
    done <<< "$INJECTED_MISTAKES"
  fi
fi

# --- Session metrics ---
if [ -n "$TRANSCRIPT_PATH" ] && [ -f "$TRANSCRIPT_PATH" ]; then
  TOOL_CALLS=$(grep -c '"tool_use"' "$TRANSCRIPT_PATH" 2>/dev/null || echo 0)
  if [ -n "$ERROR_SUMMARY" ]; then
    METRIC_ERROR_COUNT=$(printf '%s\n' "$ERROR_SUMMARY" | grep -c '.' 2>/dev/null)
  else
    METRIC_ERROR_COUNT=0
  fi
  METRIC_DURATION=$ELAPSED

  METRIC_DOMAINS="${SESSION_DOMAINS:-unknown}"
  [ -z "$METRIC_DOMAINS" ] && METRIC_DOMAINS="unknown"
  METRIC_DOMAINS=$(printf '%s' "$METRIC_DOMAINS" | tr ' ' ',')
  [ -z "$METRIC_DOMAINS" ] && METRIC_DOMAINS="unknown"

  wal_append "$TODAY|session-metrics|$METRIC_DOMAINS|error_count:$METRIC_ERROR_COUNT,tool_calls:$TOOL_CALLS,duration:${METRIC_DURATION}s"

  if [ "${METRIC_ERROR_COUNT:-0}" -eq 0 ]; then
    wal_append "$TODAY|clean-session|$METRIC_DOMAINS|$SAFE_SESSION_ID" "clean-session|$METRIC_DOMAINS|$SAFE_SESSION_ID"
  fi
fi

# --- Strategy tracking ---
if [ -f "$WAL_FILE" ] && [ -n "$SAFE_SESSION_ID" ]; then
  INJECTED_STRATEGIES=$(wal_run_locked awk -F'|' -v sid="$SAFE_SESSION_ID" '
    $4 == sid && $2 == "inject" { print $3 }
  ' "$WAL_FILE" 2>/dev/null)

  HAS_STRATEGIES=0
  if [ -n "$INJECTED_STRATEGIES" ]; then
    while IFS= read -r strat_name; do
      [ -z "$strat_name" ] && continue
      [ -f "$MEMORY_DIR/strategies/${strat_name}.md" ] || continue
      HAS_STRATEGIES=1

      if [ "${METRIC_ERROR_COUNT:-0}" -eq 0 ]; then
        wal_append "$TODAY|strategy-used|$strat_name|$SAFE_SESSION_ID" "strategy-used|$strat_name|$SAFE_SESSION_ID"
      fi
    done <<< "$INJECTED_STRATEGIES"
  fi

  if [ "${METRIC_ERROR_COUNT:-0}" -eq 0 ] && [ "$HAS_STRATEGIES" -eq 0 ]; then
    METRIC_DOMAINS_GAP="${SESSION_DOMAINS:-unknown}"
    [ -z "$METRIC_DOMAINS_GAP" ] && METRIC_DOMAINS_GAP="unknown"
    METRIC_DOMAINS_GAP=$(printf '%s' "$METRIC_DOMAINS_GAP" | tr ' ' ',')
    [ -z "$METRIC_DOMAINS_GAP" ] && METRIC_DOMAINS_GAP="unknown"
    wal_append "$TODAY|strategy-gap|$METRIC_DOMAINS_GAP|$SAFE_SESSION_ID" "strategy-gap|$METRIC_DOMAINS_GAP|$SAFE_SESSION_ID"
  fi
fi

# --- Feedback loop: useful/silent signal for injected memories ---
# Uses hooks/lib/evidence-extract.sh to match memory content against assistant text.
# evidence: frontmatter path (threshold ≥1) or body-mining fallback (threshold ≥2).
if [ -f "$WAL_FILE" ] && [ -n "$SAFE_SESSION_ID" ] && [ -n "$TRANSCRIPT_PATH" ] && [ -f "$TRANSCRIPT_PATH" ]; then
  _INJECTED_ALL=$(wal_run_locked awk -F'|' -v sid="$SAFE_SESSION_ID" '
    $4 == sid && ($2 == "inject" || $2 == "trigger-match") { print $3 }
  ' "$WAL_FILE" 2>/dev/null | sort -u)

  if [ -n "$_INJECTED_ALL" ]; then
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

    # shellcheck source=lib/evidence-extract.sh
    . "$(dirname "$0")/lib/evidence-extract.sh" 2>/dev/null || true
    # shellcheck source=lib/wal-lock.sh
    . "$(dirname "$0")/lib/wal-lock.sh" 2>/dev/null || true

    _FEEDBACK_LINES=""
    _DIAG_LINES=""
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
  fi
fi

# Strategies reminder: long clean session → suggest recording approach
if [ "${METRIC_ERROR_COUNT:-0}" -eq 0 ] && [ "${ELAPSED:-0}" -gt 600 ]; then
  REMINDER="${REMINDER}${REMINDER:+
}[STRATEGIES] Сессия чистая (0 ошибок, ${ELAPSED}с). Если задача решена с первого подхода — запиши в strategies/:
trigger, steps (3-5), outcome."
fi

if [ -n "$REMINDER" ]; then
  cat <<EOF
{"hookSpecificOutput":{"hookEventName":"Stop","additionalContext":$(printf '%s\n' "$REMINDER" | jq -Rs .)}}
EOF
fi

# Strategy scorer — update success_count in strategies/*.md from WAL.
SCORE_SCRIPT="$HOME/.claude/bin/memory-strategy-score.sh"
if [ -x "$SCORE_SCRIPT" ]; then
  CLAUDE_MEMORY_DIR="$MEMORY_DIR" bash "$SCORE_SCRIPT" 2>/dev/null &
  disown 2>/dev/null || true
fi

# Self-profile generator — derived view of WAL + mistakes + strategies.
PROFILE_SCRIPT="$HOME/.claude/bin/memory-self-profile.sh"
if [ -x "$PROFILE_SCRIPT" ]; then
  CLAUDE_MEMORY_DIR="$MEMORY_DIR" bash "$PROFILE_SCRIPT" 2>/dev/null &
  disown 2>/dev/null || true
fi

# WAL compaction — ensure WAL doesn't grow unbounded between sessions.
# wal-compact.sh self-checks threshold (>1200 lines) and acquires lock.
if [ -f "$WAL_FILE" ]; then
  COMPACT_SCRIPT="$(dirname "$0")/wal-compact.sh"
  if [ -x "$COMPACT_SCRIPT" ]; then
    CLAUDE_MEMORY_DIR="$MEMORY_DIR" bash "$COMPACT_SCRIPT" 2>/dev/null &
    disown 2>/dev/null || true
  fi
fi

# Clean up marker
rm -f "$MARKER" 2>/dev/null

exit 0
