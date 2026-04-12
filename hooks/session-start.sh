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
# Adaptive quotas: shared flex pool across scored types
# Total budget for scored types (feedback+knowledge+strategies+notes): 12 slots
MAX_SCORED_TOTAL=12
# Per-type caps (prevent any single type from dominating)
CAP_FEEDBACK=6
CAP_KNOWLEDGE=4
CAP_STRATEGIES=4
CAP_NOTES=2
CAP_DECISIONS=3
MAX_FILES=22
MAX_CLUSTER=4
TODAY=$(date +%Y-%m-%d)

# shellcheck source=lib/wal-lock.sh
. "$(dirname "$0")/lib/wal-lock.sh" 2>/dev/null || true

# --- Main ---

INPUT=$(cat)
SESSION_ID=$(printf '%s\n' "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)
CWD=$(printf '%s\n' "$INPUT" | jq -r '.cwd // empty' 2>/dev/null)

[ -z "$SESSION_ID" ] && exit 0
[ -z "$CWD" ] && CWD="$PWD"
[ -d "$MEMORY_DIR" ] || exit 0

# Clean up stale session markers (older than 1 day)
find /tmp -maxdepth 1 -name ".claude-session-*" -mtime +1 -delete 2>/dev/null || true

# Regenerate MEMORY.md index in background (drift fix — fresh for next session)
REGEN_SCRIPT="$(dirname "$0")/regen-memory-index.sh"
if [ -x "$REGEN_SCRIPT" ]; then
  CLAUDE_MEMORY_DIR="$MEMORY_DIR" bash "$REGEN_SCRIPT" 2>/dev/null &
  disown 2>/dev/null || true
fi

# --- Detect project ---

PROJECT=""
PROJECTS_JSON="$MEMORY_DIR/projects.json"
if [ -f "$PROJECTS_JSON" ] && jq empty "$PROJECTS_JSON" 2>/dev/null; then
  while read -r prefix; do
    # Exact match or prefix with / boundary (prevents /fly matching /fly-other)
    case "$CWD" in
      "${prefix}"|"${prefix}"/*)
        PROJECT=$(jq -r --arg k "$prefix" '.[$k]' "$PROJECTS_JSON" 2>/dev/null)
        break
        ;;
    esac
  done < <(jq -r 'to_entries | sort_by(.key | length) | reverse | .[].key' "$PROJECTS_JSON" 2>/dev/null)
fi

# Create session marker early (ensures stop hook can run even if we crash later)
# Sanitize session_id for marker filename (slashes create subdirs)
SAFE_MARKER_ID="${SESSION_ID//\//_}"
touch "/tmp/.claude-session-${SAFE_MARKER_ID}" 2>/dev/null || true

# --- Detect domains ---

detect_domains() {
  local cwd="$1" project="$2"
  local _domains=""  # space-separated, deduped

  _add() {
    local d="$1"
    [ -z "$d" ] && return
    case " $_domains " in *" $d "*) return ;; esac
    _domains="${_domains:+$_domains }$d"
  }

  # 1. Project → default domains mapping (from projects-domains.json)
  local DOMAINS_JSON="$MEMORY_DIR/projects-domains.json"
  if [ -n "$project" ] && [ -f "$DOMAINS_JSON" ]; then
    local proj_domains
    proj_domains=$(jq -r --arg p "$project" '.[$p] // [] | .[]' "$DOMAINS_JSON" 2>/dev/null)
    while IFS= read -r d; do
      [ -n "$d" ] && _add "$d"
    done <<< "$proj_domains"
  fi

  # 2. CWD path heuristics
  local cwd_lower
  cwd_lower=$(printf '%s' "$cwd" | tr '[:upper:]' '[:lower:]')
  case "$cwd_lower" in
    *jira*|*scriptrunner*) _add "jira"; _add "groovy" ;;
    *infra*|*terraform*|*ansible*) _add "infra"; _add "devops" ;;
    *frontend*|*webapp*|*ui/*) _add "frontend" ;;
    *backend*|*api/*|*server/*) _add "backend" ;;
    *docs*|*documentation*) _add "docs" ;;
  esac

  # 3. Git diff — recent file extensions (last 3 commits)
  if [ -d "$cwd/.git" ] || git -C "$cwd" rev-parse --git-dir >/dev/null 2>&1; then
    local diff_files
    diff_files=$(git -C "$cwd" diff --name-only HEAD~3 HEAD 2>/dev/null || \
                 git -C "$cwd" diff --name-only HEAD 2>/dev/null || \
                 git -C "$cwd" diff --cached --name-only 2>/dev/null || true)

    if [ -n "$diff_files" ]; then
      local ext_domains
      ext_domains=$(printf '%s\n' "$diff_files" | awk -F. '
        NF > 1 {
          ext = tolower($NF)
          if (ext == "css" || ext == "scss" || ext == "sass" || ext == "less") print "css"
          else if (ext == "tsx" || ext == "jsx" || ext == "vue" || ext == "svelte") print "frontend"
          else if (ext == "ts" || ext == "js") print "frontend"
          else if (ext == "py" || ext == "rb" || ext == "go" || ext == "rs" || ext == "java" || ext == "kt") print "backend"
          else if (ext == "groovy" || ext == "gvy") { print "groovy"; print "jira" }
          else if (ext == "sql" || ext == "prisma") print "db"
          else if (ext == "tf" || ext == "hcl" || ext == "yaml" || ext == "yml") print "infra"
          else if (ext == "dockerfile") print "devops"
          else if (ext == "md" || ext == "rst" || ext == "adoc") print "docs"
        }
        /^[Dd]ockerfile/ { print "devops" }
      ' | sort -u)

      while IFS= read -r d; do
        [ -n "$d" ] && _add "$d"
      done <<< "$ext_domains"
    fi
  fi

  # 4. Fallback: ls CWD for signature files (no git needed)
  if [ -z "$_domains" ]; then
    { [ -f "$cwd/Dockerfile" ] || [ -f "$cwd/docker-compose.yml" ]; } && _add "devops"
    [ -f "$cwd/package.json" ] && _add "frontend"
    { [ -f "$cwd/requirements.txt" ] || [ -f "$cwd/pyproject.toml" ]; } && _add "backend"
    { [ -f "$cwd/Makefile" ] || [ -f "$cwd/Taskfile.yml" ]; } && _add "infra"
    { [ -d "$cwd/migrations" ] || [ -d "$cwd/alembic" ]; } && _add "db"
    ls "$cwd"/*.groovy >/dev/null 2>&1 && _add "groovy"
    find "$cwd/src" -maxdepth 3 \( -name "*.css" -o -name "*.scss" \) 2>/dev/null | head -1 | grep -q . && _add "css"
  fi

  printf '%s' "$_domains"
}

DOMAINS=$(detect_domains "$CWD" "$PROJECT")

# --- Extract keywords for content-aware matching ---
# Sources: git branch, diff filenames, commit messages (last 5), staged filenames, CWD basename
# Stopwords filtered to avoid generic noise (feat, fix, update, etc.)
KEYWORDS=""
STOPWORDS="the and for with from this that into not but also been have has was were are will can may use used using add added update updated fix fixed feat merge merged commit ref docs test tests chore build main master head"

if [ -d "$CWD/.git" ] || git -C "$CWD" rev-parse --git-dir >/dev/null 2>&1; then
  # 1. Branch name tokens
  branch=$(git -C "$CWD" rev-parse --abbrev-ref HEAD 2>/dev/null || true)
  [ -n "$branch" ] && KEYWORDS="$KEYWORDS $(printf '%s' "$branch" | tr '/_-' ' ')"

  # 2. Uncommitted diff filenames (basenames without extension)
  diff_names=$(git -C "$CWD" diff --name-only HEAD 2>/dev/null | sed 's/.*\///; s/\..*//' | sort -u | head -10 | tr '\n' ' ')
  [ -n "$diff_names" ] && KEYWORDS="$KEYWORDS $diff_names"

  # 3. Staged filenames
  staged_names=$(git -C "$CWD" diff --cached --name-only 2>/dev/null | sed 's/.*\///; s/\..*//' | sort -u | head -10 | tr '\n' ' ')
  [ -n "$staged_names" ] && KEYWORDS="$KEYWORDS $staged_names"

  # 4. Recent commit messages — richest keyword source
  # Tokenize subjects of last 5 commits, strip conventional commit prefixes
  commit_words=$(git -C "$CWD" log --oneline -5 --format='%s' 2>/dev/null | \
    sed 's/^[a-z]*[(:]//; s/[):]/ /g' | \
    tr '[:upper:]' '[:lower:]' | tr -cs '[:alpha:]' ' ')
  [ -n "$commit_words" ] && KEYWORDS="$KEYWORDS $commit_words"

  # 5. File extensions in diff → domain keywords (css, sql, groovy, etc.)
  ext_kw=$(git -C "$CWD" diff --name-only HEAD~3 HEAD 2>/dev/null | awk -F. '
    NF>1 { ext=tolower($NF)
      if (ext=="css"||ext=="scss"||ext=="sass") print "css"
      else if (ext=="sql"||ext=="prisma") print "sql"
      else if (ext=="groovy"||ext=="gvy") print "groovy"
      else if (ext=="tsx"||ext=="jsx") print "react"
      else if (ext=="dockerfile") print "docker"
    }' | sort -u | tr '\n' ' ')
  [ -n "$ext_kw" ] && KEYWORDS="$KEYWORDS $ext_kw"
fi
KEYWORDS="$KEYWORDS $(basename "$CWD")"

# Normalize: lowercase, deduplicate, remove short tokens and stopwords
KEYWORDS=$(printf '%s' "$KEYWORDS" | tr '[:upper:]' '[:lower:]' | tr ' ' '\n' | \
  awk -v stops="$STOPWORDS" '
    BEGIN { n=split(stops, s, " "); for(i=1;i<=n;i++) stop[s[i]]=1 }
    length>=3 && !seen[$0]++ && !stop[$0]
  ' | tr '\n' ' ')

# Domain fallback: when git keyword signal is weak, use project domains as pseudo-keywords
# Domains like "backend", "frontend", "db", "jira" provide baseline matching signal
# Only activates when git context yields < 3 keywords (cold start / empty repo)
_KW_COUNT=$(printf '%s' "$KEYWORDS" | wc -w | tr -d ' ')
if [ "${_KW_COUNT:-0}" -lt 3 ] && [ -n "$DOMAINS" ]; then
  KEYWORDS="$KEYWORDS $DOMAINS"
  KEYWORDS=$(printf '%s' "$KEYWORDS" | tr ' ' '\n' | awk '!seen[$0]++' | tr '\n' ' ')
fi

# --- Single-pass frontmatter parsers (BSD awk compatible) ---
# Uses FNR==1 to detect file boundaries instead of BEGINFILE/ENDFILE
# Output: TSV lines per file, one awk process per directory

AWK_MISTAKES='
{ gsub(/\r/, "") }
FNR == 1 {
  if (prev_file != "") {
    printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", \
      prev_file, status, project, recurrence, injected, severity, root_cause, prevention, domains, keywords, body
  }
  in_fm = 0; fm_end_count = 0
  status = "_empty_"; project = "_empty_"; recurrence = "0"; injected = "0000-00-00"
  severity = "_empty_"; root_cause = "_empty_"; prevention = "_empty_"; domains = "_none_"; keywords = "_none_"
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
in_fm && /^domains:/ { sub(/^domains: *\[?/, ""); sub(/\].*/, ""); gsub(/, */, " "); domains = $0; next }
in_fm && /^keywords:/ { sub(/^keywords: *\[?/, ""); sub(/\].*/, ""); gsub(/, */, " "); keywords = $0; next }
past_fm && !/^$/ { gsub(/\t/, " ", $0); body = body $0 "\x1e" }
END {
  if (prev_file != "") {
    gsub(/\t/, " ", root_cause); gsub(/\t/, " ", prevention)
    printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", \
      prev_file, status, project, recurrence, injected, severity, root_cause, prevention, domains, keywords, body
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
# Input: file path
# Output: space-separated "target:type" pairs (stdout)
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
# Returns 0 (true) if file domains intersect with active DOMAINS, or if file has "general" domain
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

# --- Keyword signal strength (used by throttles below) ---
KEYWORD_COUNT=$(printf '%s' "$KEYWORDS" | wc -w | tr -d ' ')

# --- Collect mistakes ---

INJECTED_FILES=()

collect_mistakes() {
  _MISTAKES_RESULT=""
  local global_count=0
  local project_count=0

  if [ -d "$MEMORY_DIR/mistakes" ]; then
    local MISTAKE_FILES=()
    while IFS= read -r f; do
      MISTAKE_FILES+=("$f")
    done < <(find "$MEMORY_DIR/mistakes" -name "*.md" -type f 2>/dev/null)

    if [ ${#MISTAKE_FILES[@]} -gt 0 ]; then
      # Add keyword scoring to mistakes: keyword_hits*3 as primary sort, then recurrence desc
      local mistakes_scored_out
      mistakes_scored_out=$(awk "$AWK_MISTAKES" "${MISTAKE_FILES[@]}" 2>/dev/null | \
        awk -F'\t' -v kw="$KEYWORDS" '
        BEGIN { n=split(kw, kws, " ") }
        {
          score=0
          fkw=$10
          if (fkw != "_none_" && n > 0) {
            split(fkw, fks, " ")
            for (i in fks) for (j in kws) if (fks[i] == kws[j]) score += 3
          }
          printf "%d\t%s\n", score, $0
        }' | sort -t$'\t' -k1,1rn -k5,5rn -k6,6r)

      # Zero-score throttle for mistakes: if rich keywords AND scored mistakes exist,
      # limit unscored mistakes to max 2 (keeps high-recurrence generals like wrong-root-cause)
      local m_has_scored=0 m_zero_count=0 m_max_zero=99
      if [ "${KEYWORD_COUNT:-0}" -ge 10 ] && [ -n "$mistakes_scored_out" ]; then
        local m_top_score
        m_top_score=$(echo "$mistakes_scored_out" | head -1 | cut -f1)
        [ "${m_top_score:-0}" -gt 0 ] && m_has_scored=1 && m_max_zero=2
      fi

      while IFS=$'\t' read -r kscore file status file_project recurrence injected severity root_cause prevention file_domains file_keywords body; do
        [ -z "$file" ] && continue
        body=$(printf '%s' "$body" | tr $'\x1e' '\n')
        [ "$status" != "active" ] && [ "$status" != "pinned" ] && continue

        # Recurrence threshold: skip unconfirmed mistakes (rec=0) unless pinned
        [ "$recurrence" = "0" ] && [ "$status" != "pinned" ] && continue

        # Domain filtering: skip if domains don't match active context
        domain_matches "$file_domains" || continue

        # Zero-score throttle: limit unscored mistakes when keyword signal is strong
        if [ "$m_has_scored" -eq 1 ] && [ "${kscore:-0}" -eq 0 ]; then
          # Always allow high-recurrence mistakes (>=3) regardless of score
          if [ "${recurrence:-0}" -lt 3 ]; then
            [ $m_zero_count -ge $m_max_zero ] && continue
            m_zero_count=$((m_zero_count + 1))
          fi
        fi

        if [ "$file_project" = "global" ]; then
          [ $global_count -ge $MAX_GLOBAL_MISTAKES ] && continue
          global_count=$((global_count + 1))
        elif [ -n "$PROJECT" ] && [ "$file_project" = "$PROJECT" ]; then
          [ $project_count -ge $MAX_PROJECT_MISTAKES ] && continue
          project_count=$((project_count + 1))
        else
          continue
        fi

        local filename
        filename=$(basename "$file" .md)

        # If body is whitespace-only, use root-cause and prevention
        local clean_body
        clean_body=$(printf '%s' "$body" | tr -d '[:space:]')
        if [ -z "$clean_body" ]; then
          body=""
          [ -n "$root_cause" ] && [ "$root_cause" != "_empty_" ] && body="Root cause: ${root_cause}"
          [ -n "$prevention" ] && [ "$prevention" != "_empty_" ] && body="${body}
Prevention: ${prevention}"
        fi

        _MISTAKES_RESULT="${_MISTAKES_RESULT}
### ${filename} [${severity}, x${recurrence}]
${body}
"
        INJECTED_FILES+=("$file")
      done <<< "$mistakes_scored_out"
    fi
  fi
}

collect_mistakes
MISTAKES_MD="$_MISTAKES_RESULT"

# --- WAL-based spaced repetition scoring ---
# Formula: spread × decay(recency) × effectiveness
# spread = unique days with inject in 30 days (min 2 injects to count)
# decay = 1 / (1 + days_since_last / 7)
# effectiveness = Bayesian (positive + 1) / (positive + negative + 2)
WAL_FILE="$MEMORY_DIR/.wal"
WAL_SCORES=""
if [ -f "$WAL_FILE" ]; then
  if [[ "$OSTYPE" == darwin* ]]; then
    CUTOFF_30D=$(date -v-30d +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
  else
    CUTOFF_30D=$(date -d "30 days ago" +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
  fi
  # Output: "name:score;name:score;..." (semicolon-separated, no newlines)
  # NOTE: BSD awk — no multidimensional arrays. Use SUBSEP for days tracking.
  WAL_SCORES=$(awk -F'|' -v cutoff="$CUTOFF_30D" -v today="$TODAY" '
    $1 >= cutoff && $2 == "inject" {
      count[$3]++
      days[$3, $1] = 1
      if ($1 > last_day[$3] || last_day[$3] == "") last_day[$3] = $1
    }
    $1 >= cutoff && $2 == "inject-agg" {
      count[$3] += $4
      days[$3, $1] = 1
      if ($1 > last_day[$3] || last_day[$3] == "") last_day[$3] = $1
    }
    $1 >= cutoff && $2 == "outcome-positive" {
      pos_count[$3]++
    }
    $1 >= cutoff && $2 == "outcome-negative" {
      neg_count[$3]++
    }
    $1 >= cutoff && $2 == "trigger-match" {
      pos_count[$3]++
    }
    $1 >= cutoff && $2 == "strategy-used" {
      strat_used[$3]++
    }
    function jdn(y, m, d) {
      if (m <= 2) { y--; m += 12 }
      return int(365.25*(y+4716)) + int(30.6001*(m+1)) + d - 1524
    }
    function days_between(d1, d2) {
      split(d1, a, "-"); split(d2, b, "-")
      return jdn(b[1]+0, b[2]+0, b[3]+0) - jdn(a[1]+0, a[2]+0, a[3]+0)
    }
    END {
      first = 1
      for (name in count) {
        if (count[name] < 2) continue

        # spread = unique days (count SUBSEP keys for this name)
        spread = 0
        for (key in days) {
          split(key, kp, SUBSEP)
          if (kp[1] == name) spread++
        }

        # decay
        ds = days_between(last_day[name], today)
        if (ds < 0) ds = 0
        decay = 1.0 / (1.0 + ds / 7.0)

        # Bayesian effectiveness
        pc = 0; nc = 0
        if (name in pos_count) pc = pos_count[name]
        if (name in neg_count) nc = neg_count[name]
        effectiveness = (pc + 1) / (pc + nc + 2)

        raw = spread * decay * effectiveness
        if (raw > 10) raw = 10

        su = 0
        if (name in strat_used) su = strat_used[name]
        if (!first) printf ";"
        printf "%s:%.1f:%d", name, raw, su
        first = 0
      }
    }
  ' "$WAL_FILE" 2>/dev/null || true)
fi

# --- TF-IDF index scoring ---
TFIDF_INDEX="$MEMORY_DIR/.tfidf-index"
TFIDF_SCORES=""
if [ -f "$TFIDF_INDEX" ]; then
  # Check freshness: skip if older than 7 days
  if [[ "$OSTYPE" == darwin* ]]; then
    idx_mtime=$(stat -f %m "$TFIDF_INDEX" 2>/dev/null || echo 0)
  else
    idx_mtime=$(stat -c %Y "$TFIDF_INDEX" 2>/dev/null || echo 0)
  fi
  idx_age=$(( ($(date +%s) - idx_mtime) / 86400 ))
  if [ "$idx_age" -le 7 ]; then
    if [ -n "$KEYWORDS" ]; then
      TFIDF_SCORES=$(awk -F'\t' -v kw="$KEYWORDS" '
        BEGIN { n=split(kw, kws, " ") }
        {
          term=$1
          for (i=1; i<=n; i++) {
            if (term == kws[i]) {
              for (j=2; j<=NF; j++) {
                split($j, fp, ":")
                scores[fp[1]] += fp[2]+0
              }
            }
          }
        }
        END {
          first=1
          for (f in scores) {
            if (!first) printf ";"
            printf "%s:%.2f", f, scores[f]
            first=0
          }
        }
      ' "$TFIDF_INDEX")
    fi
  else
    wal_append "$TODAY|index-stale|tfidf|$SESSION_ID" "index-stale|tfidf|$SESSION_ID"
  fi
fi

# Auto-rebuild TF-IDF if missing or stale
_tfidf_needs_rebuild=0
if [ ! -f "$TFIDF_INDEX" ]; then
  _tfidf_needs_rebuild=1
elif [ "${idx_age:-999}" -gt 7 ]; then
  _tfidf_needs_rebuild=1
fi
if [ "$_tfidf_needs_rebuild" -eq 1 ]; then
  CLAUDE_MEMORY_DIR="$MEMORY_DIR" bash "$(dirname "$0")/memory-index.sh" 2>/dev/null &
  disown 2>/dev/null || true
fi

# --- Load noise candidates from analytics report ---
NOISE_CANDIDATES=""
ANALYTICS_REPORT="$MEMORY_DIR/.analytics-report"
if [ -f "$ANALYTICS_REPORT" ]; then
  if [[ "$OSTYPE" == darwin* ]]; then
    ar_mtime=$(stat -f %m "$ANALYTICS_REPORT" 2>/dev/null || echo 0)
  else
    ar_mtime=$(stat -c %Y "$ANALYTICS_REPORT" 2>/dev/null || echo 0)
  fi
  ar_age=$(( ($(date +%s) - ar_mtime) / 86400 ))
  if [ "$ar_age" -le 7 ]; then
    NOISE_CANDIDATES=$(awk '
      /^## Noise candidates/ { in_noise=1; next }
      in_noise && /^## / { exit }
      in_noise && /^- / { sub(/^- /, ""); sub(/ .*/, ""); print }
    ' "$ANALYTICS_REPORT" 2>/dev/null | tr '\n' ' ')
  fi
fi

# --- Generic scored collector (DRY: replaces 3 copy-paste blocks) ---
# Usage: collect_scored <dir> <cap>
# Sets _COLLECT_RESULT with markdown output, appends to INJECTED_FILES
# Uses _SCORED_USED (shared counter across types) for adaptive budget
collect_scored() {
  local dir="$1" cap="$2"
  _COLLECT_RESULT=""
  local type_count=0

  [ -d "$MEMORY_DIR/$dir" ] || return 0

  local files=()
  while IFS= read -r f; do
    files+=("$f")
  done < <(find "$MEMORY_DIR/$dir" -name "*.md" -type f 2>/dev/null)

  [ ${#files[@]} -gt 0 ] || return 0

  # Parse files with keyword scoring via awk
  # Pass KEYWORDS to awk for inline scoring (avoids shell loop overhead)
  local awk_out
  # Composite scoring: keyword_hits * 3 + project_boost + wal + tfidf
  awk_out=$(awk "$AWK_SCORED" "${files[@]}" 2>/dev/null | \
    awk -F'\t' -v kw="$KEYWORDS" -v wal="$WAL_SCORES" -v tfidf="$TFIDF_SCORES" -v proj="$PROJECT" -v noise="$NOISE_CANDIDATES" '
    BEGIN {
      n=split(kw, kws, " ")
      # Parse WAL scores: "name:count;name:count;..."
      m=split(wal, wentries, ";")
      for (i=1; i<=m; i++) {
        split(wentries[i], wp, ":")
        if (wp[1] != "") {
          wal_count[wp[1]] = wp[2]+0
          strat_used_count[wp[1]] = wp[3]+0
        }
      }
      # Parse TF-IDF scores: "name:score;name:score;..."
      t=split(tfidf, tentries, ";")
      for (i=1; i<=t; i++) {
        split(tentries[i], tp, ":")
        if (tp[1] != "") tfidf_score[tp[1]] = tp[2]+0
      }
      # Parse noise candidates
      nn=split(noise, ncands, " ")
      for (i=1; i<=nn; i++) if (ncands[i] != "") is_noise[ncands[i]]=1
    }
    {
      score=0
      # Keyword matching: +3 per keyword hit
      fkw=$6
      if (fkw != "_none_" && n > 0) {
        split(fkw, fks, " ")
        for (i in fks) for (j in kws) if (fks[i] == kws[j]) score += 3
      }
      # Project boost: +1 tiebreaker for project-scoped records
      if (proj != "" && $3 == proj) score += 1
      # WAL spaced repetition score (already normalized 0-10)
      fname=$1; sub(/.*\//, "", fname); sub(/\.md$/, "", fname)
      wc = wal_count[fname]+0
      score += wc
      # Strategy bonus: min(strategy_used_count * 2, 6)
      su = strat_used_count[fname]+0
      if (su > 3) su = 3
      score += su * 2
      # Noise penalty from analytics report
      if (fname in is_noise) score -= 3
      # TF-IDF bonus: only for records WITHOUT keywords field (+2 per score unit, cap 6)
      if (fkw == "_none_") {
        ts = tfidf_score[fname]+0
        if (ts > 3) ts = 3
        score += ts * 2
      }
      printf "%d\t%s\n", score, $0
    }' | sort -t$'\t' -k1,1rn -k5,5r)

  # Unified pass: score-first, project as tiebreaker (+1 for project match)
  # Records sorted by: keyword score desc, then project-match desc, then referenced desc
  # Zero-score throttle: if ANY record has score > 0, limit score=0 records to 2 slots
  # This prevents noise when keyword signal is available, but preserves fallback when it's not
  local has_scored=0 zero_count=0 max_zero=$cap
  if [ -n "$awk_out" ]; then
    local top_score
    top_score=$(echo "$awk_out" | head -1 | cut -f1)
    [ "${top_score:-0}" -gt 0 ] && has_scored=1 && max_zero=2
  fi

  while IFS=$'\t' read -r kscore file status file_project referenced file_domains file_keywords body; do
    [ -z "$file" ] && continue
    body=$(printf '%s' "$body" | tr $'\x1e' '\n')
    [ "$status" != "active" ] && [ "$status" != "pinned" ] && continue
    domain_matches "$file_domains" || continue

    # Must be either project-scoped or global
    if [ -n "$PROJECT" ] && [ "$file_project" = "$PROJECT" ]; then
      : # project match — ok
    elif [ "$file_project" = "global" ]; then
      : # global — ok
    else
      continue # wrong project
    fi

    [ $type_count -ge $cap ] && continue
    [ $_SCORED_USED -ge $MAX_SCORED_TOTAL ] && continue

    # Zero-score throttle: limit unscored records when keyword signal exists
    if [ "$has_scored" -eq 1 ] && [ "${kscore:-0}" -eq 0 ]; then
      [ $zero_count -ge $max_zero ] && continue
      zero_count=$((zero_count + 1))
    fi

    local clean_body
    clean_body=$(printf '%s' "$body" | tr -d '[:space:]')
    [ -z "$clean_body" ] && continue

    local filename
    filename=$(basename "$file" .md)
    _COLLECT_RESULT="${_COLLECT_RESULT}
### ${filename}
${body}
"
    INJECTED_FILES+=("$file")
    type_count=$((type_count + 1))
    _SCORED_USED=$((_SCORED_USED + 1))
  done <<< "$awk_out"
}

# --- Collect feedback, knowledge, strategies (adaptive quotas) ---
# Adaptive budget: rich keyword signal → tighter budget (less noise)
if [ "${KEYWORD_COUNT:-0}" -ge 10 ]; then
  # Rich git context (commit messages extracted) — reduce noise
  MAX_SCORED_TOTAL=8
elif [ "${KEYWORD_COUNT:-0}" -ge 5 ]; then
  MAX_SCORED_TOTAL=10
fi
# else keep default 12

_SCORED_USED=0

collect_scored "feedback" "$CAP_FEEDBACK"
FEEDBACK_MD="$_COLLECT_RESULT"

collect_scored "knowledge" "$CAP_KNOWLEDGE"
KNOWLEDGE_MD="$_COLLECT_RESULT"

# Notes before strategies — notes have most files and smallest cap,
# placing them after everything else starves them when budget is tight
collect_scored "notes" "$CAP_NOTES"
NOTES_MD="$_COLLECT_RESULT"

collect_scored "strategies" "$CAP_STRATEGIES"
STRATEGIES_MD="$_COLLECT_RESULT"

collect_scored "decisions" "$CAP_DECISIONS"
DECISIONS_MD="$_COLLECT_RESULT"

# --- Cluster activation pass (v0.4) ---
# Load related files that weren't picked up by the main pass
CLUSTER_MD=""
CONTRADICTS_WARNINGS=""
_PRIORITY_SLUGS=" "

if [ ${#INJECTED_FILES[@]} -gt 0 ]; then
  # Build space-separated list of injected slugs
  INJECTED_SLUGS=" "
  for _cf in "${INJECTED_FILES[@]}"; do
    _cs=$(basename "$_cf" .md)
    INJECTED_SLUGS="${INJECTED_SLUGS}${_cs} "
  done

  # Candidate list: "slug|source_slug|rel_type|priority" lines (new files only)
  # Priority: contradicts=0 reinforces=1 instance_of=2 supersedes=3
  CLUSTER_CANDIDATES=""
  # Provenance links: "target|source|type" for all related (including already-injected)
  CLUSTER_PROVENANCE=""

  # --- Forward scan: for each injected file, parse related targets ---
  for _cf in "${INJECTED_FILES[@]}"; do
    _source_slug=$(basename "$_cf" .md)
    _rels=$(parse_related "$_cf")
    [ -z "$_rels" ] && continue
    for _rel in $_rels; do
      _target=$(printf '%s' "$_rel" | cut -d: -f1)
      _rtype=$(printf '%s' "$_rel" | cut -d: -f2)
      [ -z "$_target" ] && continue
      # Record provenance for all valid links (including already-injected)
      CLUSTER_PROVENANCE="${CLUSTER_PROVENANCE}
${_target}|${_source_slug}|${_rtype}"
      # Skip if already injected (provenance recorded above)
      case "$INJECTED_SLUGS" in
        *" ${_target} "*) continue ;;
      esac
      # Skip if already a candidate
      case "$CLUSTER_CANDIDATES" in
        *"|${_target}|"*) continue ;;
      esac
      # Find the file
      _tpath=$(find_memory_file "$_target") || continue
      # Check status: active or pinned only
      _tstatus=$(awk '/^---$/{n++} n==1 && /^status:/{sub(/^status: */,""); print; exit}' "$_tpath" 2>/dev/null)
      case "$_tstatus" in active|pinned) ;; *) continue ;; esac
      # Score check: keyword OR WAL OR TF-IDF > 0
      _qualifies=0
      # Check keywords
      _fkw=$(awk '/^---$/{n++} n==1 && /^keywords:/{sub(/^keywords: *\[?/,""); sub(/\].*/,""); gsub(/, */," "); print; exit}' "$_tpath" 2>/dev/null)
      if [ -n "$_fkw" ] && [ -n "$KEYWORDS" ]; then
        for _k in $_fkw; do
          for _sk in $KEYWORDS; do
            [ "$_k" = "$_sk" ] && _qualifies=1 && break 2
          done
        done
      fi
      # Check WAL
      if [ "$_qualifies" -eq 0 ] && [ -n "$WAL_SCORES" ]; then
        case ";${WAL_SCORES};" in *";${_target}:"*) _qualifies=1 ;; esac
      fi
      # Check TF-IDF
      if [ "$_qualifies" -eq 0 ] && [ -n "$TFIDF_SCORES" ]; then
        case ";${TFIDF_SCORES};" in *";${_target}:"*) _qualifies=1 ;; esac
      fi
      [ "$_qualifies" -eq 0 ] && continue
      # Assign priority by relationship type
      case "$_rtype" in
        contradicts) _prio=0 ;;
        reinforces)  _prio=1 ;;
        instance_of) _prio=2 ;;
        supersedes)  _prio=3 ;;
        *)           _prio=2 ;;
      esac
      CLUSTER_CANDIDATES="${CLUSTER_CANDIDATES}
${_prio}|${_target}|${_source_slug}|${_rtype}"
    done
  done

  # --- Reverse scan: for each non-injected file, check if it links to an injected slug ---
  for _rdir in mistakes feedback strategies knowledge notes decisions; do
    [ -d "$MEMORY_DIR/$_rdir" ] || continue
    for _rf in "$MEMORY_DIR/$_rdir"/*.md; do
      [ -f "$_rf" ] || continue
      _rslug=$(basename "$_rf" .md)
      # Skip if already injected
      case "$INJECTED_SLUGS" in *" ${_rslug} "*) continue ;; esac
      # Skip if already a candidate
      case "$CLUSTER_CANDIDATES" in *"|${_rslug}|"*) continue ;; esac
      _rrels=$(parse_related "$_rf")
      [ -z "$_rrels" ] && continue
      for _rrel in $_rrels; do
        _rtarget=$(printf '%s' "$_rrel" | cut -d: -f1)
        _rrtype=$(printf '%s' "$_rrel" | cut -d: -f2)
        # Check if target is in injected set
        case "$INJECTED_SLUGS" in
          *" ${_rtarget} "*)
            # Check status
            _rstatus=$(awk '/^---$/{n++} n==1 && /^status:/{sub(/^status: */,""); print; exit}' "$_rf" 2>/dev/null)
            case "$_rstatus" in active|pinned) ;; *) continue ;; esac
            # Score check
            _rqualifies=0
            _rfkw=$(awk '/^---$/{n++} n==1 && /^keywords:/{sub(/^keywords: *\[?/,""); sub(/\].*/,""); gsub(/, */," "); print; exit}' "$_rf" 2>/dev/null)
            if [ -n "$_rfkw" ] && [ -n "$KEYWORDS" ]; then
              for _k in $_rfkw; do
                for _sk in $KEYWORDS; do
                  [ "$_k" = "$_sk" ] && _rqualifies=1 && break 2
                done
              done
            fi
            if [ "$_rqualifies" -eq 0 ] && [ -n "$WAL_SCORES" ]; then
              case ";${WAL_SCORES};" in *";${_rslug}:"*) _rqualifies=1 ;; esac
            fi
            if [ "$_rqualifies" -eq 0 ] && [ -n "$TFIDF_SCORES" ]; then
              case ";${TFIDF_SCORES};" in *";${_rslug}:"*) _rqualifies=1 ;; esac
            fi
            [ "$_rqualifies" -eq 0 ] && continue
            case "$_rrtype" in
              contradicts) _rprio=0 ;;
              reinforces)  _rprio=1 ;;
              instance_of) _rprio=2 ;;
              supersedes)  _rprio=3 ;;
              *)           _rprio=2 ;;
            esac
            CLUSTER_CANDIDATES="${CLUSTER_CANDIDATES}
${_rprio}|${_rslug}|${_rtarget}|${_rrtype}"
            break  # one match is enough for reverse scan
            ;;
        esac
      done
    done
  done

  # --- Also scan injected files for cross-relationships (contradicts between injected files) ---
  for _cf in "${INJECTED_FILES[@]}"; do
    _source_slug=$(basename "$_cf" .md)
    _rels=$(parse_related "$_cf")
    [ -z "$_rels" ] && continue
    for _rel in $_rels; do
      _target=$(printf '%s' "$_rel" | cut -d: -f1)
      _rtype=$(printf '%s' "$_rel" | cut -d: -f2)
      [ "$_rtype" = "contradicts" ] || continue
      # Check if target is also injected
      case "$INJECTED_SLUGS" in
        *" ${_target} "*)
          # Both are injected — handle contradicts
          # Determine which is newer by referenced date
          _src_ref=$(awk '/^---$/{n++} n==1 && /^referenced:/{sub(/^referenced: */,""); print; exit}' "$_cf" 2>/dev/null)
          _tgt_path=$(find_memory_file "$_target") || continue
          _tgt_ref=$(awk '/^---$/{n++} n==1 && /^referenced:/{sub(/^referenced: */,""); print; exit}' "$_tgt_path" 2>/dev/null)
          [ -z "$_src_ref" ] && _src_ref="0000-00-00"
          [ -z "$_tgt_ref" ] && _tgt_ref="0000-00-00"
          if [ "$_tgt_ref" ">" "$_src_ref" ] || [ "$_tgt_ref" = "$_src_ref" ]; then
            _newer="$_target"
            _older="$_source_slug"
          else
            _newer="$_source_slug"
            _older="$_target"
          fi
          CONTRADICTS_WARNINGS="${CONTRADICTS_WARNINGS}
> ⚠ Конфликт: \`${_older}\` contradicts \`${_newer}\` — проверь актуальность"
          _PRIORITY_SLUGS="${_PRIORITY_SLUGS}${_newer} "
          ;;
      esac
    done
  done

  # --- Sort candidates by priority, take up to MAX_CLUSTER ---
  if [ -n "$CLUSTER_CANDIDATES" ]; then
    _cluster_count=0
    _cluster_loaded_slugs=" "
    while IFS='|' read -r _prio _slug _source _rtype; do
      [ -z "$_slug" ] && continue
      [ "$_cluster_count" -ge "$MAX_CLUSTER" ] && break
      # Dedup
      case "$_cluster_loaded_slugs" in *" ${_slug} "*) continue ;; esac
      # Find and load the file
      _cpath=$(find_memory_file "$_slug") || continue
      _cbody=$(sed '1,/^---$/d; 1,/^---$/d' "$_cpath" 2>/dev/null | sed '/^$/d')
      [ -z "$(printf '%s' "$_cbody" | tr -d '[:space:]')" ] && continue

      # For contradicts: determine newer before writing header so we can tag it
      _slug_label="${_slug}"
      if [ "$_rtype" = "contradicts" ]; then
        _src_path=$(find_memory_file "$_source") || true
        if [ -n "$_src_path" ] && [ -f "$_src_path" ]; then
          _src_ref=$(awk '/^---$/{n++} n==1 && /^referenced:/{sub(/^referenced: */,""); print; exit}' "$_src_path" 2>/dev/null)
          _tgt_ref=$(awk '/^---$/{n++} n==1 && /^referenced:/{sub(/^referenced: */,""); print; exit}' "$_cpath" 2>/dev/null)
          [ -z "$_src_ref" ] && _src_ref="0000-00-00"
          [ -z "$_tgt_ref" ] && _tgt_ref="0000-00-00"
          if [ "$_tgt_ref" ">" "$_src_ref" ] || [ "$_tgt_ref" = "$_src_ref" ]; then
            _newer="$_slug"
          else
            _newer="$_source"
          fi
          # Tag the header if this loaded slug is the newer record
          if [ "$_newer" = "$_slug" ]; then
            _slug_label="${_slug} [ПРИОРИТЕТ]"
          else
            # Source is newer — mark it for post-processing of already-injected sections
            _PRIORITY_SLUGS="${_PRIORITY_SLUGS}${_newer} "
          fi
          CONTRADICTS_WARNINGS="${CONTRADICTS_WARNINGS}
> ⚠ Конфликт: \`${_source}\` contradicts \`${_slug}\` — проверь актуальность"
        fi
      fi

      CLUSTER_MD="${CLUSTER_MD}
### ${_slug_label} (via ${_source} → ${_rtype})
${_cbody}
"
      INJECTED_FILES+=("$_cpath")
      _cluster_loaded_slugs="${_cluster_loaded_slugs}${_slug} "
      _cluster_count=$((_cluster_count + 1))

      # WAL: log cluster-load event
      wal_append "$TODAY|cluster-load|$_slug|${SESSION_ID//|/_}" "cluster-load|$_slug|${SESSION_ID//|/_}"
    done < <(printf '%s\n' "$CLUSTER_CANDIDATES" | sort -t'|' -k1,1n | grep -v '^$')
  fi

  # --- Provenance annotations for already-injected related files ---
  # Share cluster budget: cluster-loaded + provenance together capped at MAX_CLUSTER
  _prov_count=${_cluster_count:-0}
  if [ -n "$CLUSTER_PROVENANCE" ]; then
    _prov_shown=""
    while IFS='|' read -r _ptarget _psource _ptype; do
      [ -z "$_ptarget" ] && continue
      [ "$_prov_count" -ge "$MAX_CLUSTER" ] && break
      # Only show provenance for already-injected targets (not cluster-loaded, those have inline provenance)
      case "$INJECTED_SLUGS" in *" ${_ptarget} "*) ;; *) continue ;; esac
      # Dedup provenance display
      case "$_prov_shown" in *"|${_ptarget}:${_ptype}|"*) continue ;; esac
      _prov_shown="${_prov_shown}|${_ptarget}:${_ptype}|"
      CLUSTER_MD="${CLUSTER_MD}
- **${_ptarget}** (via ${_psource} → ${_ptype})
"
      # WAL: log cluster-load for provenance-linked files
      wal_append "$TODAY|cluster-load|$_ptarget|${SESSION_ID//|/_}" "cluster-load|$_ptarget|${SESSION_ID//|/_}"
      _prov_count=$((_prov_count + 1))
    done <<< "$CLUSTER_PROVENANCE"
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

# --- Post-process: inject [ПРИОРИТЕТ] into headers of newer contradicts records ---
# These are already-injected files (cross-injected contradicts) whose headers are in section vars
if [ "$_PRIORITY_SLUGS" != " " ]; then
  for _ps in $_PRIORITY_SLUGS; do
    [ -z "$_ps" ] && continue
    # Replace "### slug\n" with "### slug [ПРИОРИТЕТ]\n" in section variables (exact match, not already tagged)
    MISTAKES_MD=$(printf '%s' "$MISTAKES_MD" | perl -pe "s/^(### ${_ps})(\\s|$)/\$1 [ПРИОРИТЕТ]\$2/")
    FEEDBACK_MD=$(printf '%s' "$FEEDBACK_MD" | perl -pe "s/^(### ${_ps})(\\s|$)/\$1 [ПРИОРИТЕТ]\$2/")
    KNOWLEDGE_MD=$(printf '%s' "$KNOWLEDGE_MD" | perl -pe "s/^(### ${_ps})(\\s|$)/\$1 [ПРИОРИТЕТ]\$2/")
    STRATEGIES_MD=$(printf '%s' "$STRATEGIES_MD" | perl -pe "s/^(### ${_ps})(\\s|$)/\$1 [ПРИОРИТЕТ]\$2/")
    DECISIONS_MD=$(printf '%s' "$DECISIONS_MD" | perl -pe "s/^(### ${_ps})(\\s|$)/\$1 [ПРИОРИТЕТ]\$2/")
    NOTES_MD=$(printf '%s' "$NOTES_MD" | perl -pe "s/^(### ${_ps})(\\s|$)/\$1 [ПРИОРИТЕТ]\$2/")
  done
fi

# --- Build output ---

CONTEXT=""
# Continuity first — most relevant context
[ -n "$CONTINUITY_MD" ] && CONTEXT="${CONTINUITY_MD}"
[ -n "$PROJECT_MD" ] && CONTEXT="${CONTEXT}${PROJECT_MD}"
if [ -n "$DOMAINS" ]; then
  CONTEXT="${CONTEXT}
**Active domains:** ${DOMAINS}
"
fi
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
if [ -n "$DECISIONS_MD" ]; then
  CONTEXT="${CONTEXT}
## Decisions
${DECISIONS_MD}"
fi
if [ -n "$NOTES_MD" ]; then
  CONTEXT="${CONTEXT}
## Notes
${NOTES_MD}"
fi
if [ -n "$CLUSTER_MD" ]; then
  CONTEXT="${CONTEXT}
## Related
${CLUSTER_MD}"
fi
if [ -n "$CONTRADICTS_WARNINGS" ]; then
  CONTEXT="${CONTEXT}
${CONTRADICTS_WARNINGS}"
fi

# --- Cascade-review display (v0.4) ---
# Scan WAL for cascade-review events < 14 days old, inject [REVIEW] markers into section headers
if [ -f "$WAL_FILE" ]; then
  if [[ "$OSTYPE" == darwin* ]]; then
    _CASCADE_CUTOFF=$(date -v-14d +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
  else
    _CASCADE_CUTOFF=$(date -d "14 days ago" +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
  fi
  # Build review map: "slug|parent|date" lines (most recent per slug)
  _CASCADE_MAP=$(awk -F'|' -v cutoff="$_CASCADE_CUTOFF" '
    $1 >= cutoff && $2 == "cascade-review" {
      slug = $3
      # $4 format: "parent:parent-slug"
      parent = $4
      sub(/^parent:/, "", parent)
      date = $1
      if (!(slug in best) || date > best_date[slug]) {
        best[slug] = parent
        best_date[slug] = date
      }
    }
    END {
      for (s in best) {
        printf "%s|%s|%s\n", s, best[s], best_date[s]
      }
    }
  ' "$WAL_FILE" 2>/dev/null)

  if [ -n "$_CASCADE_MAP" ]; then
    while IFS='|' read -r _cr_slug _cr_parent _cr_date; do
      [ -z "$_cr_slug" ] && continue
      # Replace "### slug" with "### slug [REVIEW: parent updated YYYY-MM-DD]" in CONTEXT
      _cr_marker="[REVIEW: ${_cr_parent} updated ${_cr_date}]"
      CONTEXT=$(printf '%s' "$CONTEXT" | perl -pe "s/^(### ${_cr_slug})( |$)/\$1 ${_cr_marker}\$2/")
    done <<< "$_CASCADE_MAP"
  fi
fi

# v0.5: always write dedup list (even if empty) so user-prompt-submit knows session started
SAFE_MARKER_ID_V05="${SESSION_ID//\//_}"
RUNTIME_DIR_V05="$MEMORY_DIR/.runtime"
DEDUP_FILE_V05="$RUNTIME_DIR_V05/injected-${SAFE_MARKER_ID_V05}.list"
mkdir -p "$RUNTIME_DIR_V05" 2>/dev/null || true
{
  for f in "${INJECTED_FILES[@]}"; do
    basename "$f" .md
  done
} > "$DEDUP_FILE_V05" 2>/dev/null || true

# Nothing to inject — but don't exit yet; health warnings (broken hooks,
# schema errors) must surface even when no memories match the session.
_EARLY_EMPTY_CONTEXT=0
[ -z "$CONTEXT" ] && _EARLY_EMPTY_CONTEXT=1

# Cap total injected files (cluster files are ABOVE MAX_FILES, up to MAX_FILES + MAX_CLUSTER)
_TOTAL_CAP=$((MAX_FILES + MAX_CLUSTER))
if [ ${#INJECTED_FILES[@]} -gt $_TOTAL_CAP ]; then
  INJECTED_FILES=("${INJECTED_FILES[@]:0:$_TOTAL_CAP}")
fi

# Update accessed dates — single perl call for all files (13x faster than grep+sed+rm loop)
# Only modify within frontmatter block (between first and second ---)
if [ ${#INJECTED_FILES[@]} -gt 0 ]; then
  perl -pi -e '
    $in_fm = 1 if /^---$/ && !$seen_fm++;
    $in_fm = 0 if /^---$/ && $seen_fm > 1;
    if ($in_fm) {
      s/^referenced: .*/referenced: '"$TODAY"'/;
      s/^ref_count: (\d+)/"ref_count: " . ($1 + 1)/e;
    }
    if (eof) { $seen_fm = 0; $in_fm = 0; }
  ' "${INJECTED_FILES[@]}" 2>/dev/null || true
fi

# WAL: append injection log (Hebbian — tracks access frequency)
# Sanitize session_id: replace | with _ to prevent WAL field corruption
SAFE_SESSION_ID="${SESSION_ID//|/_}"
WAL_FILE="$MEMORY_DIR/.wal"

# shellcheck source=lib/wal-lock.sh
. "$(dirname "$0")/lib/wal-lock.sh" 2>/dev/null || true

_INJECT_LINES=""
for f in "${INJECTED_FILES[@]}"; do
  _INJECT_LINES="${_INJECT_LINES}${TODAY}|inject|$(basename "$f" .md)|${SAFE_SESSION_ID}
"
done
if [ -n "$_INJECT_LINES" ]; then
  wal_run_locked bash -c 'printf "%s" "$1" >> "$2"' _ "$_INJECT_LINES" "$WAL_FILE" 2>/dev/null || true
fi

# Rotate WAL: smart compaction (aggregate old entries, preserve spread data)
if [ -f "$WAL_FILE" ] && [ "$(wc -l < "$WAL_FILE" 2>/dev/null)" -gt 1200 ]; then
  CLAUDE_MEMORY_DIR="$MEMORY_DIR" bash "$(dirname "$0")/wal-compact.sh" 2>/dev/null || \
    { tail -1000 "$WAL_FILE" > "$WAL_FILE.tmp" && mv "$WAL_FILE.tmp" "$WAL_FILE"; } 2>/dev/null || true
fi

# Session marker already created early (after project detection)

# Generate compact agent context file
AGENT_CTX="$MEMORY_DIR/_agent_context.md"
{
  echo "# Memory Context (compact, for subagents)"
  echo ""
  [ -n "$PROJECT" ] && echo "**Project:** $PROJECT"
  [ -n "$DOMAINS" ] && echo "**Domains:** $DOMAINS"
  echo ""
  # User profile (stable, from user-profile.md)
  USER_PROFILE="$MEMORY_DIR/user-profile.md"
  if [ -f "$USER_PROFILE" ]; then
    echo "## User"
    sed -n '/^---$/,/^---$/d; /^#/d; /^$/d; p' "$USER_PROFILE" | \
      grep -E "^- .*(специалист|стек|stack|русский|лаконич|конкретн)" | head -3
    echo ""
  fi
  if [ -n "$MISTAKES_MD" ]; then
    echo "## Key Mistakes"
    printf '%s\n' "$MISTAKES_MD" | grep -E "^### |^Root cause:|^Prevention:" | head -15
    echo ""
  fi
  if [ -n "$FEEDBACK_MD" ]; then
    echo "## Feedback"
    # Capture ALL body lines until **Why:** (previous impl kept only first, losing multi-rule feedback like code-approach).
    printf '%s\n' "$FEEDBACK_MD" | awk '
      /^### / {
        if (name) {
          print name
          if (body) print body
          if (why)  print why
        }
        name = $0; body = ""; why = ""; next
      }
      /^\*\*Why:\*\*/ { why = $0; next }
      /^[^#*]/ && !/^$/ {
        if (body == "") body = $0
        else            body = body "\n" $0
        next
      }
      END {
        if (name) {
          print name
          if (body) print body
          if (why)  print why
        }
      }
    ' | head -40
    echo ""
  fi
  if [ -n "$STRATEGIES_MD" ]; then
    echo "## Strategies"
    printf '%s\n' "$STRATEGIES_MD" | awk '/^### /{if(name)print name": "line; name=$0; line=""; next} line=="" && /^[^#]/ && !/^$/{line=$0} END{if(name)print name": "line}' | head -3
    echo ""
  fi
  if [ -n "$DECISIONS_MD" ]; then
    echo "## Decisions"
    printf '%s\n' "$DECISIONS_MD" | awk '/^### /{if(name)print name": "line; name=$0; line=""; next} line=="" && /^[^#]/ && !/^$/{line=$0} END{if(name)print name": "line}' | head -3
    echo ""
  fi
  echo "Full context: \`~/.claude/memory/\`"
} > "$AGENT_CTX" 2>/dev/null || true

# Health check: detect silent failures.
HEALTH_WARNING=""

# 1. Broken symlinks / missing companion scripts (C3 risk: hooks are symlinks
#    to dev folder; a rename would make hooks fail silently).
_hook_dir="$(dirname "$0")"
_broken=""
for _req in "$_hook_dir/lib/wal-lock.sh" "$_hook_dir/wal-compact.sh" "$_hook_dir/regen-memory-index.sh"; do
  [ -f "$_req" ] || _broken="${_broken}${_broken:+, }$(basename "$_req")"
done
if [ -n "$_broken" ]; then
  HEALTH_WARNING="⚠ Memory hooks broken — missing: $_broken. Likely cause: symlink target moved. Re-run hypomnema/install.sh."
fi

# 2. UserPromptSubmit hook silent-fail signal: 50+ injects but 0 trigger-match.
# NOTE: grep -c outputs "0" with exit 1 when no matches, so || fallback would duplicate the count.
# $(...) strips trailing newline; numeric comparison works directly.
if [ -z "$HEALTH_WARNING" ] && [ -f "$WAL_FILE" ]; then
  _wal_tail=$(tail -200 "$WAL_FILE" 2>/dev/null)
  _inj=$(printf '%s\n' "$_wal_tail" | grep -c '|inject|')
  _trig=$(printf '%s\n' "$_wal_tail" | grep -c '|trigger-match|')
  if [ "${_inj:-0}" -gt 50 ] && [ "${_trig:-0}" -eq 0 ]; then
    HEALTH_WARNING="⚠ Memory health: 0 trigger-match events in last 200 WAL entries (${_inj} injects). UserPromptSubmit hook may be silently failing — check settings.json timeout and ~/.claude/hooks/lib/wal-lock.sh presence."
  fi
fi

# 3. Schema errors accumulated — surface to user.
if [ -z "$HEALTH_WARNING" ] && [ -f "$WAL_FILE" ]; then
  _schema_errs=$(tail -200 "$WAL_FILE" 2>/dev/null | grep -c '|schema-error|')
  if [ "${_schema_errs:-0}" -gt 0 ]; then
    _err_slugs=$(tail -200 "$WAL_FILE" 2>/dev/null | awk -F'|' '$2=="schema-error"{print $3}' | sort -u | head -5 | tr '\n' ' ')
    HEALTH_WARNING="⚠ Memory schema: ${_schema_errs} malformed frontmatter entries recently (${_err_slugs}). Fix closing --- in these files."
  fi
fi

# Nothing to inject AND no health warnings → silent exit.
if [ "$_EARLY_EMPTY_CONTEXT" = "1" ] && [ -z "$HEALTH_WARNING" ]; then
  exit 0
fi

# Output for Claude Code
MARKDOWN="# Memory Context
${CONTEXT}${HEALTH_WARNING:+

## System Health
${HEALTH_WARNING}}"

cat <<EOF
{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":$(printf '%s\n' "$MARKDOWN" | jq -Rs .)}}
EOF

exit 0
