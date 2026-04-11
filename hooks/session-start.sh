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
  local mistakes_md=""
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

        mistakes_md="${mistakes_md}
### ${filename} [${severity}, x${recurrence}]
${body}
"
        INJECTED_FILES+=("$file")
      done <<< "$mistakes_scored_out"
    fi
  fi

  printf '%s' "$mistakes_md"
}

MISTAKES_MD=$(collect_mistakes)

# --- WAL-based spaced repetition scoring ---
# Formula: spread × decay(recency) × (1 - negative_ratio)
# spread = unique days with inject in 30 days (min 2 injects to count)
# decay = 1 / (1 + days_since_last / 7)
# negative_ratio = outcome-negative count / total injects
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
    $1 >= cutoff && $2 == "outcome-negative" {
      neg_count[$3]++
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

        # negative ratio
        nr = 0
        if (name in neg_count) nr = neg_count[name] / count[name]
        if (nr > 1) nr = 1

        raw = spread * decay * (1.0 - nr)
        if (raw > 10) raw = 10

        if (!first) printf ";"
        printf "%s:%.1f", name, raw
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
    printf '%s|index-stale|tfidf|%s\n' "$TODAY" "$SESSION_ID" >> "$MEMORY_DIR/.wal" 2>/dev/null || true
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
    awk -F'\t' -v kw="$KEYWORDS" -v wal="$WAL_SCORES" -v tfidf="$TFIDF_SCORES" -v proj="$PROJECT" '
    BEGIN {
      n=split(kw, kws, " ")
      # Parse WAL scores: "name:count;name:count;..."
      m=split(wal, wentries, ";")
      for (i=1; i<=m; i++) {
        split(wentries[i], wp, ":")
        if (wp[1] != "") wal_count[wp[1]] = wp[2]+0
      }
      # Parse TF-IDF scores: "name:score;name:score;..."
      t=split(tfidf, tentries, ";")
      for (i=1; i<=t; i++) {
        split(tentries[i], tp, ":")
        if (tp[1] != "") tfidf_score[tp[1]] = tp[2]+0
      }
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

# Nothing to inject
[ -z "$CONTEXT" ] && exit 0

# Cap total injected files
if [ ${#INJECTED_FILES[@]} -gt $MAX_FILES ]; then
  INJECTED_FILES=("${INJECTED_FILES[@]:0:$MAX_FILES}")
fi

# Update accessed dates — single perl call for all files (13x faster than grep+sed+rm loop)
# Only modify within frontmatter block (between first and second ---)
if [ ${#INJECTED_FILES[@]} -gt 0 ]; then
  perl -pi -e '
    $in_fm = 1 if /^---$/ && !$seen_fm++;
    $in_fm = 0 if /^---$/ && $seen_fm > 1;
    if ($in_fm) {
      s/^referenced: .*/referenced: '"$TODAY"'/;
    }
    if (eof) { $seen_fm = 0; $in_fm = 0; }
  ' "${INJECTED_FILES[@]}" 2>/dev/null || true
fi

# WAL: append injection log (Hebbian — tracks access frequency)
# Sanitize session_id: replace | with _ to prevent WAL field corruption
SAFE_SESSION_ID="${SESSION_ID//|/_}"
WAL_FILE="$MEMORY_DIR/.wal"
{
  for f in "${INJECTED_FILES[@]}"; do
    printf '%s|inject|%s|%s\n' "$TODAY" "$(basename "$f" .md)" "$SAFE_SESSION_ID"
  done
} >> "$WAL_FILE" 2>/dev/null || true
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
    printf '%s\n' "$FEEDBACK_MD" | awk '/^### /{if(name){print name; if(body)print body; if(why)print why}; name=$0; body=""; why=""; next} body=="" && /^[^#*]/ && !/^$/{body=$0; next} /^\*\*Why:\*\*/{why=$0} END{if(name){print name; if(body)print body; if(why)print why}}' | head -20
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

# Output for Claude Code
MARKDOWN="# Memory Context
${CONTEXT}"

cat <<EOF
{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":$(printf '%s\n' "$MARKDOWN" | jq -Rs .)}}
EOF

exit 0
