#!/bin/bash
# Stop hook — remind Claude to record if work happened but nothing was saved
# Dependencies: jq, find, stat
# Graceful degradation: any error → exit 0

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
MIN_SESSION_SECONDS=120  # 2 minutes

INPUT=$(cat)
SESSION_ID=$(printf '%s\n' "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)

[ -z "$SESSION_ID" ] && exit 0

MARKER="/tmp/.claude-session-${SESSION_ID}"
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

# --- Lifecycle rotation ---
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
}

lifecycle_rotate 2>/dev/null || true

# Rebuild TF-IDF index if memory files changed or index missing
MODIFIED_FOR_INDEX=$(find "$MEMORY_DIR" -name "*.md" -newer "$MARKER" -not -name "MEMORY.md" -not -name "_agent_context.md" 2>/dev/null | head -1)
if [ -n "$MODIFIED_FOR_INDEX" ] || [ ! -f "$MEMORY_DIR/.tfidf-index" ]; then
  bash "$HOME/.claude/hooks/memory-index.sh" 2>/dev/null &
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
        # Update existing git state section
        perl -pi -e "
          if (/^## Git State/) { \$_ = \"## Git State\n${GIT_STATE}\n\"; \$skip=1; next }
          if (\$skip && /^## /) { \$skip=0 }
          \$_ = '' if \$skip;
        " "$CONT_FILE" 2>/dev/null || true
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

if [ -n "$REMINDER" ]; then
  cat <<EOF
{"hookSpecificOutput":{"hookEventName":"Stop","additionalContext":$(printf '%s\n' "$REMINDER" | jq -Rs .)}}
EOF
fi

# Clean up marker
rm -f "$MARKER" 2>/dev/null

exit 0
