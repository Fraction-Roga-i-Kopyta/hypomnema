#!/bin/bash
# SessionStart hook — inject relevant memory context
# v2: single-pass awk for all frontmatter parsing (O(1) process spawns per dir)
# Uses BSD awk (macOS) — no BEGINFILE/ENDFILE, uses FNR==1 for file boundary detection
# Dependencies: jq, awk, find, sed
# Graceful degradation: any error → exit 0

set -o pipefail

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
MAX_GLOBAL_MISTAKES=3
MAX_PROJECT_MISTAKES=3
MAX_FEEDBACK=5
MAX_FILES=15
TODAY=$(date +%Y-%m-%d)

# --- Main ---

INPUT=$(cat)
SESSION_ID=$(printf '%s\n' "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)
CWD=$(printf '%s\n' "$INPUT" | jq -r '.cwd // empty' 2>/dev/null)

[ -z "$SESSION_ID" ] && exit 0
[ -z "$CWD" ] && CWD="$PWD"
[ -d "$MEMORY_DIR" ] || exit 0

# Clean up stale session markers (older than 1 day)
find /tmp -maxdepth 1 -name ".claude-session-*" -mtime +1 -delete 2>/dev/null || true

# --- Detect project ---

PROJECT=""
PROJECTS_JSON="$MEMORY_DIR/projects.json"
if [ -f "$PROJECTS_JSON" ] && jq empty "$PROJECTS_JSON" 2>/dev/null; then
  while read -r prefix; do
    case "$CWD" in
      "${prefix}"*)
        PROJECT=$(jq -r --arg k "$prefix" '.[$k]' "$PROJECTS_JSON" 2>/dev/null)
        break
        ;;
    esac
  done < <(jq -r 'to_entries | sort_by(.key | length) | reverse | .[].key' "$PROJECTS_JSON" 2>/dev/null)
fi

# --- Single-pass frontmatter parsers (BSD awk compatible) ---
# Uses FNR==1 to detect file boundaries instead of BEGINFILE/ENDFILE
# Output: TSV lines per file, one awk process per directory

AWK_MISTAKES='
FNR == 1 {
  if (prev_file != "") {
    printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", \
      prev_file, status, project, recurrence, injected, severity, root_cause, prevention, body
  }
  in_fm = 0; fm_end_count = 0
  status = ""; project = ""; recurrence = "0"; injected = "0000-00-00"
  severity = ""; root_cause = ""; prevention = ""
  body = ""; past_fm = 0; prev_file = FILENAME
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
past_fm && !/^$/ { body = body $0 "\n" }
END {
  if (prev_file != "") {
    printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", \
      prev_file, status, project, recurrence, injected, severity, root_cause, prevention, body
  }
}'

AWK_FEEDBACK='
FNR == 1 {
  if (prev_file != "") {
    printf "%s\t%s\t%s\t%s\n", prev_file, status, project, body
  }
  in_fm = 0; fm_end_count = 0
  status = ""; project = ""; body = ""; past_fm = 0; prev_file = FILENAME
}
/^---$/ {
  fm_end_count++
  if (fm_end_count == 1) { in_fm = 1; next }
  if (fm_end_count == 2) { in_fm = 0; past_fm = 1; next }
}
in_fm && /^status:/ { sub(/^status: */, ""); status = $0; next }
in_fm && /^project:/ { sub(/^project: */, ""); project = $0; next }
past_fm && !/^$/ { body = body $0 "\n" }
END {
  if (prev_file != "") {
    printf "%s\t%s\t%s\t%s\n", prev_file, status, project, body
  }
}'

# AWK_GENERIC reuses AWK_FEEDBACK format (same fields: file, status, project, body)
AWK_GENERIC="$AWK_FEEDBACK"

# --- Collect mistakes ---

INJECTED_FILES=()
MISTAKES_MD=""
GLOBAL_COUNT=0
PROJECT_COUNT=0

if [ -d "$MEMORY_DIR/mistakes" ]; then
  MISTAKE_FILES=()
  while IFS= read -r f; do
    MISTAKE_FILES+=("$f")
  done < <(find "$MEMORY_DIR/mistakes" -name "*.md" -type f 2>/dev/null)

  if [ ${#MISTAKE_FILES[@]} -gt 0 ]; then
    while IFS=$'\t' read -r file status file_project recurrence injected severity root_cause prevention body; do
      [ -z "$file" ] && continue
      [ "$status" != "active" ] && [ "$status" != "pinned" ] && continue

      if [ "$file_project" = "global" ]; then
        [ $GLOBAL_COUNT -ge $MAX_GLOBAL_MISTAKES ] && continue
        GLOBAL_COUNT=$((GLOBAL_COUNT + 1))
      elif [ -n "$PROJECT" ] && [ "$file_project" = "$PROJECT" ]; then
        [ $PROJECT_COUNT -ge $MAX_PROJECT_MISTAKES ] && continue
        PROJECT_COUNT=$((PROJECT_COUNT + 1))
      else
        continue
      fi

      filename=$(basename "$file" .md)

      # If body is whitespace-only, use root-cause and prevention
      clean_body=$(printf '%s' "$body" | tr -d '[:space:]')
      if [ -z "$clean_body" ]; then
        body=""
        [ -n "$root_cause" ] && body="Root cause: ${root_cause}"
        [ -n "$prevention" ] && body="${body}
Prevention: ${prevention}"
      fi

      MISTAKES_MD="${MISTAKES_MD}
### ${filename} [${severity}, x${recurrence}]
${body}
"
      INJECTED_FILES+=("$file")
    done < <(awk "$AWK_MISTAKES" "${MISTAKE_FILES[@]}" 2>/dev/null | sort -t$'\t' -k4,4rn -k5,5r)
  fi
fi

# --- Collect feedback ---

FEEDBACK_MD=""
FEEDBACK_COUNT=0

if [ -d "$MEMORY_DIR/feedback" ]; then
  FEEDBACK_FILES=()
  while IFS= read -r f; do
    FEEDBACK_FILES+=("$f")
  done < <(find "$MEMORY_DIR/feedback" -name "*.md" -type f 2>/dev/null | sort)

  if [ ${#FEEDBACK_FILES[@]} -gt 0 ]; then
    while IFS=$'\t' read -r file status file_project body; do
      [ -z "$file" ] && continue
      [ $FEEDBACK_COUNT -ge $MAX_FEEDBACK ] && break
      [ "$status" != "active" ] && [ "$status" != "pinned" ] && continue
      [ "$file_project" != "global" ] && [ "$file_project" != "$PROJECT" ] && continue

      clean_body=$(printf '%s' "$body" | tr -d '[:space:]')
      [ -z "$clean_body" ] && continue

      filename=$(basename "$file" .md)
      FEEDBACK_MD="${FEEDBACK_MD}
### ${filename}
${body}
"
      INJECTED_FILES+=("$file")
      FEEDBACK_COUNT=$((FEEDBACK_COUNT + 1))
    done < <(awk "$AWK_FEEDBACK" "${FEEDBACK_FILES[@]}" 2>/dev/null)
  fi
fi

# --- Collect knowledge ---

KNOWLEDGE_MD=""
MAX_KNOWLEDGE=3

if [ -d "$MEMORY_DIR/knowledge" ]; then
  KNOWLEDGE_FILES=()
  while IFS= read -r f; do
    KNOWLEDGE_FILES+=("$f")
  done < <(find "$MEMORY_DIR/knowledge" -name "*.md" -type f 2>/dev/null | sort)

  if [ ${#KNOWLEDGE_FILES[@]} -gt 0 ]; then
    KNOWLEDGE_COUNT=0
    while IFS=$'\t' read -r file status file_project body; do
      [ -z "$file" ] && continue
      [ $KNOWLEDGE_COUNT -ge $MAX_KNOWLEDGE ] && break
      [ "$status" != "active" ] && [ "$status" != "pinned" ] && continue
      [ "$file_project" != "global" ] && [ "$file_project" != "$PROJECT" ] && continue

      clean_body=$(printf '%s' "$body" | tr -d '[:space:]')
      [ -z "$clean_body" ] && continue

      filename=$(basename "$file" .md)
      KNOWLEDGE_MD="${KNOWLEDGE_MD}
### ${filename}
${body}
"
      INJECTED_FILES+=("$file")
      KNOWLEDGE_COUNT=$((KNOWLEDGE_COUNT + 1))
    done < <(awk "$AWK_GENERIC" "${KNOWLEDGE_FILES[@]}" 2>/dev/null)
  fi
fi

# --- Collect strategies ---

STRATEGIES_MD=""
MAX_STRATEGIES=3

if [ -d "$MEMORY_DIR/strategies" ]; then
  STRATEGY_FILES=()
  while IFS= read -r f; do
    STRATEGY_FILES+=("$f")
  done < <(find "$MEMORY_DIR/strategies" -name "*.md" -type f 2>/dev/null | sort)

  if [ ${#STRATEGY_FILES[@]} -gt 0 ]; then
    STRATEGY_COUNT=0
    while IFS=$'\t' read -r file status file_project body; do
      [ -z "$file" ] && continue
      [ $STRATEGY_COUNT -ge $MAX_STRATEGIES ] && break
      [ "$status" != "active" ] && [ "$status" != "pinned" ] && continue
      [ "$file_project" != "global" ] && [ "$file_project" != "$PROJECT" ] && continue

      clean_body=$(printf '%s' "$body" | tr -d '[:space:]')
      [ -z "$clean_body" ] && continue

      filename=$(basename "$file" .md)
      STRATEGIES_MD="${STRATEGIES_MD}
### ${filename}
${body}
"
      INJECTED_FILES+=("$file")
      STRATEGY_COUNT=$((STRATEGY_COUNT + 1))
    done < <(awk "$AWK_GENERIC" "${STRATEGY_FILES[@]}" 2>/dev/null)
  fi
fi

# --- Collect project overview ---

PROJECT_MD=""
if [ -n "$PROJECT" ]; then
  overview="$MEMORY_DIR/projects/${PROJECT}.md"
  if [ -f "$overview" ]; then
    body=$(sed '1,/^---$/d; 1,/^---$/d' "$overview" 2>/dev/null)
    PROJECT_MD="
## Project: ${PROJECT}
${body}
"
    INJECTED_FILES+=("$overview")
  fi
fi

# --- Inject continuity ---

CONTINUITY_MD=""
if [ -n "$PROJECT" ]; then
  cont="$MEMORY_DIR/continuity/${PROJECT}.md"
  if [ -f "$cont" ]; then
    cont_body=$(sed '1,/^---$/d; 1,/^---$/d' "$cont" 2>/dev/null | sed '/^$/d')
    if [ -n "$(printf '%s' "$cont_body" | tr -d '[:space:]')" ]; then
      CONTINUITY_MD="
## Last Session
${cont_body}
"
      INJECTED_FILES+=("$cont")
    fi
  fi
fi

# --- Build output ---

CONTEXT=""
# Continuity first — most relevant context
[ -n "$CONTINUITY_MD" ] && CONTEXT="${CONTINUITY_MD}"
[ -n "$PROJECT_MD" ] && CONTEXT="${CONTEXT}${PROJECT_MD}"
if [ -n "$MISTAKES_MD" ]; then
  CONTEXT="${CONTEXT}
## Active Mistakes
${MISTAKES_MD}"
fi
if [ -n "$FEEDBACK_MD" ]; then
  CONTEXT="${CONTEXT}
## Feedback
${FEEDBACK_MD}"
fi
if [ -n "$KNOWLEDGE_MD" ]; then
  CONTEXT="${CONTEXT}
## Knowledge
${KNOWLEDGE_MD}"
fi
if [ -n "$STRATEGIES_MD" ]; then
  CONTEXT="${CONTEXT}
## Strategies
${STRATEGIES_MD}"
fi

# Nothing to inject
[ -z "$CONTEXT" ] && exit 0

# Cap total injected files
if [ ${#INJECTED_FILES[@]} -gt $MAX_FILES ]; then
  INJECTED_FILES=("${INJECTED_FILES[@]:0:$MAX_FILES}")
fi

# Update accessed dates — single sed per file (unavoidable writes)
for f in "${INJECTED_FILES[@]}"; do
  if grep -q "^injected:" "$f" 2>/dev/null; then
    sed -i.bak "s/^injected: .*/injected: $TODAY/" "$f" 2>/dev/null && rm -f "$f.bak" 2>/dev/null || true
  fi
done

# Create session marker AFTER all file modifications
touch "/tmp/.claude-session-${SESSION_ID}" 2>/dev/null || true

# Generate compact agent context file
AGENT_CTX="$MEMORY_DIR/_agent_context.md"
{
  echo "# Memory Context (compact, for subagents)"
  echo ""
  [ -n "$PROJECT" ] && echo "**Project:** $PROJECT"
  echo ""
  if [ -n "$MISTAKES_MD" ]; then
    echo "## Key Mistakes"
    printf '%s\n' "$MISTAKES_MD" | grep "^### " | head -5
    echo ""
  fi
  if [ -n "$STRATEGIES_MD" ]; then
    echo "## Strategies"
    printf '%s\n' "$STRATEGIES_MD" | grep "^### " | head -3
    echo ""
  fi
  echo "Full context: \`~/.claude/memory/\`"
} > "$AGENT_CTX" 2>/dev/null || true

# Output for Claude Code
MARKDOWN="# Memory Context
${CONTEXT}"

cat <<EOF
{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":$(printf '%s\n' "$MARKDOWN" | jq -Rs .)}}
EOF

exit 0
