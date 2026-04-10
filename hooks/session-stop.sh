#!/bin/bash
# Stop hook — remind Claude to record if work happened but nothing was saved
# Dependencies: jq, find, stat
# Graceful degradation: any error → exit 0

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
MIN_SESSION_SECONDS=120  # 2 minutes

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
  # journal   | 30         | 90
  # projects  | never      | never (excluded)

  # Collect all candidate files
  local all_files=()
  for subdir in mistakes feedback knowledge notes strategies journal; do
    while IFS= read -r f; do
      all_files+=("$f")
    done < <(find "$MEMORY_DIR/$subdir" -name "*.md" -type f 2>/dev/null)
  done
  [ ${#all_files[@]} -gt 0 ] || return 0

  # Single-pass awk: extract status, referenced, created for all files
  awk '
  { gsub(/\r/, "") }
  FNR == 1 {
    if (prev_file != "") printf "%s\t%s\t%s\t%s\n", prev_file, status, ref, created
    in_fm = 0; n = 0; status = "_empty_"; ref = ""; created = ""; prev_file = FILENAME
  }
  /^---$/ { n++; if (n==1) in_fm=1; if (n==2) in_fm=0 }
  in_fm && /^status:/ { sub(/^status: */, ""); status = $0 }
  in_fm && /^referenced:/ { sub(/^referenced: */, ""); ref = $0 }
  in_fm && /^created:/ { sub(/^created: */, ""); created = $0 }
  END { if (prev_file != "") printf "%s\t%s\t%s\t%s\n", prev_file, status, ref, created }
  ' "${all_files[@]}" 2>/dev/null | while IFS=$'\t' read -r f file_status ref_date created_date; do
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

    local stale_days=30 archive_days=90
    case "$f" in
      */mistakes/*)   stale_days=60;  archive_days=180 ;;
      */strategies/*) stale_days=90;  archive_days=180 ;;
      */knowledge/*)  stale_days=90;  archive_days=365 ;;
      */feedback/*)   stale_days=45;  archive_days=120 ;;
      */notes/*)      stale_days=30;  archive_days=90  ;;
      */journal/*)    stale_days=30;  archive_days=90  ;;
    esac

    if [ "$file_status" = "stale" ] && [ "$age" -gt "$archive_days" ]; then
      local rel="${f#$MEMORY_DIR/}"
      mkdir -p "$ARCHIVE_DIR/$(dirname "$rel")"
      mv "$f" "$ARCHIVE_DIR/$rel"
      continue
    fi

    if [ "$age" -gt "$stale_days" ] && [ "$file_status" = "active" ]; then
      perl -pi -e '
        $in_fm = 1 if /^---$/ && !$seen_fm++;
        $in_fm = 0 if /^---$/ && $seen_fm > 1;
        s/^status: active/status: stale/ if $in_fm;
        if (eof) { $seen_fm = 0; $in_fm = 0; }
      ' "$f" 2>/dev/null || true
    fi
  done

  # Archive files without valid frontmatter if older than 90 days by mtime
  for f in "${all_files[@]}"; do
    head_line=$(head -1 "$f" 2>/dev/null)
    [ "$head_line" = "---" ] && continue
    if [[ "$OSTYPE" == darwin* ]]; then
      fmtime=$(stat -f %m "$f" 2>/dev/null || echo 0)
    else
      fmtime=$(stat -c %Y "$f" 2>/dev/null || echo 0)
    fi
    local fage=$(( (NOW_SEC - fmtime) / 86400 ))
    if [ "$fage" -gt 90 ]; then
      local rel="${f#$MEMORY_DIR/}"
      mkdir -p "$ARCHIVE_DIR/$(dirname "$rel")"
      mv "$f" "$ARCHIVE_DIR/$rel"
    fi
  done
}

lifecycle_rotate 2>/dev/null || true

# Session-specific processing requires marker
[ -f "$MARKER" ] || exit 0

# Check session duration
if [[ "$OSTYPE" == darwin* ]]; then
  MARKER_TIME=$(stat -f %m "$MARKER" 2>/dev/null || echo 0)
else
  MARKER_TIME=$(stat -c %Y "$MARKER" 2>/dev/null || echo 0)
fi
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
    printf '%s|error-detected-%s|%s|%s\n' "$(date +%Y-%m-%d)" "$level" "$first_word" "$SAFE_SESSION_ID" >> "$MEMORY_DIR/.wal" 2>/dev/null
  done <<< "$ERROR_SUMMARY"
  if [ -n "$ERROR_LINES" ]; then
    REMINDER="${REMINDER}${REMINDER:+
}[ERRORS] Обнаружены ошибки в этой сессии:${ERROR_LINES}
Запиши в mistakes/ если это повторяющийся паттерн."
  fi
fi

if [ -n "$REMINDER" ]; then
  cat <<EOF
{"hookSpecificOutput":{"hookEventName":"Stop","additionalContext":$(printf '%s\n' "$REMINDER" | jq -Rs .)}}
EOF
fi

# Clean up marker
rm -f "$MARKER" 2>/dev/null

exit 0
