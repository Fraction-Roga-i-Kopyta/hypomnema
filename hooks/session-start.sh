#!/bin/bash
# SessionStart hook — inject relevant memory context
# v2: single-pass awk for all frontmatter parsing (O(1) process spawns per dir)
# Uses BSD awk (macOS) — no BEGINFILE/ENDFILE, uses FNR==1 for file boundary detection
# Dependencies: jq, awk, find, sed
# Graceful degradation: any error → exit 0

set -o pipefail

# LC_ALL=C keeps awk/perl printf float output using '.' as decimal separator.
# Without this, ru_RU/de_DE/fr_FR locales produce commas that downstream
# awk +0 coercion silently truncates (audit-2026-04-16 R1).
export LC_ALL=C

# S5 (audit-2026-04-16): WAL and runtime files are session-private; create
# them mode 0600 so they are not world-readable on multi-user systems.
umask 077

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"

# User-overridable runtime config (templates/.config.sh.example)
# SECURITY: do NOT source .config.sh as shell — it is LLM-writable.
# Parse only whitelisted integer keys via load_memory_config (see load-config.sh).
# shellcheck source=lib/load-config.sh
. "$(dirname "$0")/lib/load-config.sh" 2>/dev/null || true
load_memory_config "$MEMORY_DIR/.config.sh"

MAX_GLOBAL_MISTAKES=${MAX_GLOBAL_MISTAKES:-3}
MAX_PROJECT_MISTAKES=${MAX_PROJECT_MISTAKES:-3}
# Adaptive quotas: shared flex pool across scored types
# Total budget for scored types (feedback+knowledge+strategies+notes): 12 slots
MAX_SCORED_TOTAL=${MAX_SCORED_TOTAL:-12}
# Per-type caps (prevent any single type from dominating)
CAP_FEEDBACK=${CAP_FEEDBACK:-6}
CAP_KNOWLEDGE=${CAP_KNOWLEDGE:-4}
CAP_STRATEGIES=${CAP_STRATEGIES:-4}
CAP_NOTES=${CAP_NOTES:-2}
CAP_DECISIONS=${CAP_DECISIONS:-3}
MAX_FILES=${MAX_FILES:-22}
MAX_CLUSTER=${MAX_CLUSTER:-4}
TODAY="${HYPOMNEMA_TODAY:-$(date +%Y-%m-%d)}"

# shellcheck source=lib/wal-lock.sh
. "$(dirname "$0")/lib/wal-lock.sh" 2>/dev/null || true
# shellcheck source=lib/stat-helpers.sh
. "$(dirname "$0")/lib/stat-helpers.sh" 2>/dev/null || true
# shellcheck source=lib/detect-project.sh
. "$(dirname "$0")/lib/detect-project.sh" 2>/dev/null || true
# shellcheck source=lib/parse-memory.sh
. "$(dirname "$0")/lib/parse-memory.sh" 2>/dev/null || true
# shellcheck source=lib/score-records.sh
. "$(dirname "$0")/lib/score-records.sh" 2>/dev/null || true
# shellcheck source=lib/build-context.sh
. "$(dirname "$0")/lib/build-context.sh" 2>/dev/null || true

# Escape a string for safe interpolation inside a perl regex pattern.
# Used wherever user-supplied slugs/names land in `perl -pe "s/.../"` patterns.
_perl_re_escape() {
  printf '%s' "$1" | perl -ne 'chomp; print quotemeta'
}

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
# Forward CWD so the index lands in the correct auto-memory project slug.
REGEN_SCRIPT="$(dirname "$0")/regen-memory-index.sh"
if [ -x "$REGEN_SCRIPT" ]; then
  CLAUDE_MEMORY_DIR="$MEMORY_DIR" CLAUDE_SESSION_CWD="$CWD" bash "$REGEN_SCRIPT" 2>/dev/null &
  disown 2>/dev/null || true
fi

# --- Detect project ---

PROJECTS_JSON="$MEMORY_DIR/projects.json"
PROJECT=$(detect_project "$CWD")

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
    # R10: cap files at 512KB. Larger files (usually misuse — dumps of
    # logs or test output) blow past the 15s SessionStart timeout through
    # AWK parsing. (audit-2026-04-16 R10)
    # R15: `-L` follows symlinks so a memory file symlinked into the dir
    # is included; `-type f` still filters out broken links (target not a
    # regular file). (audit-2026-04-16 R15)
    done < <(find -L "$MEMORY_DIR/mistakes" -name "*.md" -type f -size -512k 2>/dev/null)

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

      while IFS=$'\t' read -r kscore file status file_project recurrence injected severity root_cause prevention file_domains file_keywords file_scope body; do
        [ -z "$file" ] && continue
        # P3: decode body only AFTER cheap filters — most candidates get
        # dropped on status/recurrence/scope/domain/project.
        # (audit-2026-04-16 P3)
        [ "$status" != "active" ] && [ "$status" != "pinned" ] && continue

        # Recurrence threshold: skip unconfirmed mistakes (rec=0) unless pinned
        [ "$recurrence" = "0" ] && [ "$status" != "pinned" ] && continue

        # Scope filter: narrow mistakes inject only via explicit keyword match (not domain alone)
        # Pinned bypasses scope — always inject.
        if [ "$file_scope" = "narrow" ] && [ "${kscore:-0}" -eq 0 ] && [ "$status" != "pinned" ]; then
          continue
        fi

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
        filename="${file##*/}"
        filename="${filename%.md}"

        # Body decode only here — after every filter. (P3/P5)
        body=$(printf '%s' "$body" | tr $'\x1e' '\n')

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

# --- Compute scoring signals (lib/score-records.sh) ---
WAL_FILE="$MEMORY_DIR/.wal"
WAL_SCORES=$(compute_wal_scores "$WAL_FILE" "$TODAY")

TFIDF_INDEX="$MEMORY_DIR/.tfidf-index"
TFIDF_SCORES=""
idx_age=999
if [ -f "$TFIDF_INDEX" ]; then
  idx_mtime=$(_stat_mtime "$TFIDF_INDEX")
  idx_age=$(( ($(date +%s) - idx_mtime) / 86400 ))
  if [ "$idx_age" -le 7 ]; then
    TFIDF_SCORES=$(compute_tfidf_scores "$TFIDF_INDEX" "$KEYWORDS")
  else
    wal_append "$TODAY|index-stale|tfidf|$SESSION_ID" "index-stale|tfidf|$SESSION_ID"
  fi
fi

# Auto-rebuild TF-IDF if missing or stale (background, non-blocking)
if [ ! -f "$TFIDF_INDEX" ] || [ "$idx_age" -gt 7 ]; then
  CLAUDE_MEMORY_DIR="$MEMORY_DIR" bash "$(dirname "$0")/memory-index.sh" 2>/dev/null &
  disown 2>/dev/null || true
fi

# Noise candidates from analytics report (skip if older than 7 days)
NOISE_CANDIDATES=""
ANALYTICS_REPORT="$MEMORY_DIR/.analytics-report"
if [ -f "$ANALYTICS_REPORT" ]; then
  ar_mtime=$(_stat_mtime "$ANALYTICS_REPORT")
  ar_age=$(( ($(date +%s) - ar_mtime) / 86400 ))
  if [ "$ar_age" -le 7 ]; then
    NOISE_CANDIDATES=$(load_noise_candidates "$ANALYTICS_REPORT")
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
  # R15: same symlink-following policy as collect_mistakes above.
  done < <(find -L "$MEMORY_DIR/$dir" -name "*.md" -type f -size -512k 2>/dev/null)

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
      # WAL score is a float 0-10; the other components are integers.
      # `printf "%d"` truncated the fractional part, so any record whose
      # only signal was WAL (raw < 1) collapsed to 0 and lost to hash-order
      # ties in sort. Scale by 100 to preserve two decimal digits of
      # resolution — relative proportions between all score components are
      # preserved, and downstream bash `-gt 0` / `-eq 0` tests stay integer.
      printf "%d\t%s\n", int(score * 100 + 0.5), $0
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
    # P3: run the body tr-decode only AFTER all cheap filters. Previously every
    # candidate paid the decode; ~90% of them get filtered out at status/domain/
    # project checks, so the work was wasted. (audit-2026-04-16 P3)
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

    body=$(printf '%s' "$body" | tr $'\x1e' '\n')
    local clean_body
    clean_body=$(printf '%s' "$body" | tr -d '[:space:]')
    [ -z "$clean_body" ] && continue

    local filename
    filename="${file##*/}"
    filename="${filename%.md}"
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

# --- Cluster activation (lib/build-context.sh) ---
expand_clusters

# --- Collect project overview ---

PROJECT_MD=""
if [ -n "$PROJECT" ]; then
  overview="$MEMORY_DIR/projects/${PROJECT}.md"
  if [ -f "$overview" ]; then
    body=$(sed '/^---$/,/^---$/d' "$overview" 2>/dev/null)
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
    cont_body=$(sed '/^---$/,/^---$/d' "$cont" 2>/dev/null | sed '/^$/d')
    if [ -n "$(printf '%s' "$cont_body" | tr -d '[:space:]')" ]; then
      CONTINUITY_MD="
## Last Session
${cont_body}
"
      INJECTED_FILES+=("$cont")
    fi
  fi
fi

# --- Post-process + assemble + cascade markers (lib/build-context.sh) ---
apply_priority_markers
assemble_context
apply_cascade_markers

# v0.5: always write dedup list (even if empty) so user-prompt-submit knows session started
SAFE_MARKER_ID_V05="${SESSION_ID//\//_}"
RUNTIME_DIR_V05="$MEMORY_DIR/.runtime"
DEDUP_FILE_V05="$RUNTIME_DIR_V05/injected-${SAFE_MARKER_ID_V05}.list"
mkdir -p "$RUNTIME_DIR_V05" 2>/dev/null || true
{
  for f in "${INJECTED_FILES[@]}"; do
    _b="${f##*/}"
    printf '%s\n' "${_b%.md}"
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
  _b="${f##*/}"; _b="${_b%.md}"
  # S6 (audit-2026-04-16): basenames containing '|' would split the WAL
  # field boundary and let an LLM forge an outcome-positive event by
  # naming a file 'slug|outcome-positive|other.md'. Sanitize here to
  # match the session_id sanitization already applied above.
  _b="${_b//|/_}"
  _INJECT_LINES="${_INJECT_LINES}${TODAY}|inject|${_b}|${SAFE_SESSION_ID}
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
      grep -E "^- .*(специалист|стек|stack|русский|лаконич|конкретн|role|expertise|language|concise|english|writes in)" | head -3
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

# Output for Claude Code.
# SECURITY (audit-2026-04-16 S4, CWE-116): memory bodies are LLM-authored
# and may contain Claude Code structural markers (`</system-reminder>`,
# `<tool_use>`, `<invoke>`, etc.). jq -Rs escapes for JSON transport but
# once decoded as `additionalContext`, the raw markers could be interpreted
# as structure. Entity-encode the leading `<` on the known marker list so
# the text renders visibly but cannot close an outer tag.
MARKDOWN="# Memory Context
${CONTEXT}${HEALTH_WARNING:+

## System Health
${HEALTH_WARNING}}"

MARKDOWN_SAFE=$(printf '%s' "$MARKDOWN" | perl -pe 's{<(/?(?:system-reminder|system|function_calls|invoke|parameter|tool_use|tool_result|user-prompt-submit-hook))(?=[\s/>])}{\&lt;$1}g' 2>/dev/null)
[ -z "$MARKDOWN_SAFE" ] && MARKDOWN_SAFE="$MARKDOWN"

cat <<EOF
{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":$(printf '%s\n' "$MARKDOWN_SAFE" | jq -Rs .)}}
EOF

exit 0
