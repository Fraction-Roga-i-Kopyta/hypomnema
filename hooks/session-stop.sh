#!/bin/bash
# Stop hook — remind Claude to record if work happened but nothing was saved
# Dependencies: jq, find, stat
# Graceful degradation: any error → exit 0

set -o pipefail

# LC_ALL=C keeps awk/perl printf float output using '.' as decimal separator
# (audit-2026-04-16 R1).
export LC_ALL=C
# S5 (audit-2026-04-16): session-private files created mode 0600.
umask 077

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"

# User-overridable runtime config (templates/.config.sh.example)
# SECURITY: do NOT source .config.sh as shell — it is LLM-writable.
# Parse only whitelisted integer keys via load_memory_config (see load-config.sh).
# shellcheck source=lib/load-config.sh
. "$(dirname "$0")/lib/load-config.sh" 2>/dev/null || true
load_memory_config "$MEMORY_DIR/.config.sh"

MIN_SESSION_SECONDS=${MIN_SESSION_SECONDS:-120}  # 2 minutes
WAL_FILE="$MEMORY_DIR/.wal"

# shellcheck source=lib/wal-lock.sh
. "$(dirname "$0")/lib/wal-lock.sh" 2>/dev/null || true
# shellcheck source=lib/stat-helpers.sh
. "$(dirname "$0")/lib/stat-helpers.sh" 2>/dev/null || true
# shellcheck source=lib/detect-project.sh
. "$(dirname "$0")/lib/detect-project.sh" 2>/dev/null || true
# shellcheck source=lib/rotation.sh
. "$(dirname "$0")/lib/rotation.sh" 2>/dev/null || true
# shellcheck source=lib/outcome-detection.sh
. "$(dirname "$0")/lib/outcome-detection.sh" 2>/dev/null || true
# shellcheck source=lib/evidence-extract.sh
. "$(dirname "$0")/lib/evidence-extract.sh" 2>/dev/null || true
# shellcheck source=lib/feedback-loop.sh
. "$(dirname "$0")/lib/feedback-loop.sh" 2>/dev/null || true

INPUT=$(cat)
SESSION_ID=$(printf '%s\n' "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)

[ -z "$SESSION_ID" ] && exit 0

SAFE_MARKER_ID="${SESSION_ID//\//_}"
SAFE_SESSION_ID="${SESSION_ID//|/_}"
SAFE_SESSION_ID="${SAFE_SESSION_ID//\//_}"
MARKER="/tmp/.claude-session-${SAFE_MARKER_ID}"

# Lifecycle rotation (always runs, even without session marker). Body
# lives in lib/rotation.sh — sourced above.
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

# Detect project (shared helper from lib/detect-project.sh)
PROJECTS_JSON="$MEMORY_DIR/projects.json"
PROJECT=$(detect_project "$CWD")

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
}[CONTINUITY] Fill continuity/${PROJECT}.md:

Task: ___
Status: ___
Next step: ___

Context: branch \`${cont_branch}\`, last commit: \"${cont_commit}\""
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
}[ERRORS] Errors detected this session:${ERROR_LINES}
Record in mistakes/ if this is a recurring pattern."
  fi
fi

# Outcome-positive detection — body lives in lib/outcome-detection.sh.
TODAY="${HYPOMNEMA_TODAY:-$(date +%Y-%m-%d)}"
WAL_FILE="$MEMORY_DIR/.wal"
classify_outcome_positive 2>/dev/null || true

# --- Session metrics ---
if [ -n "$TRANSCRIPT_PATH" ] && [ -f "$TRANSCRIPT_PATH" ]; then
  # C1: `grep -c ... || echo 0` produced "0\n0" when the pattern didn't
  # match (grep still prints "0" then exits 1, triggering the fallback).
  # The embedded newline then corrupted the session-metrics WAL record.
  # (audit-2026-04-16 C1)
  TOOL_CALLS=$(grep -c '"tool_use"' "$TRANSCRIPT_PATH" 2>/dev/null)
  TOOL_CALLS=${TOOL_CALLS:-0}
  if [ -n "$ERROR_SUMMARY" ]; then
    METRIC_ERROR_COUNT=$(printf '%s\n' "$ERROR_SUMMARY" | grep -c '.' 2>/dev/null)
    METRIC_ERROR_COUNT=${METRIC_ERROR_COUNT:-0}
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

# Feedback loop: trigger-useful / trigger-silent classification.
# Body lives in lib/feedback-loop.sh.
classify_feedback_from_transcript 2>/dev/null || true

# Strategies reminder: long clean session → suggest recording approach
if [ "${METRIC_ERROR_COUNT:-0}" -eq 0 ] && [ "${ELAPSED:-0}" -gt 600 ]; then
  REMINDER="${REMINDER}${REMINDER:+
}[STRATEGIES] Clean session (0 errors, ${ELAPSED}s). If the task was solved first try — record in strategies/:
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
