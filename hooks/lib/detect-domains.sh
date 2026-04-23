# hooks/lib/detect-domains.sh
# shellcheck shell=bash
# Determine active domains for a session at SessionStart time.
#
# Four-source cascade, deduped:
#   1. projects-domains.json mapping (if project known).
#   2. CWD path heuristics (jira/infra/frontend/backend/docs).
#   3. git diff extensions over last 3 commits (css/frontend/backend/
#      groovy/db/infra/devops/docs).
#   4. Signature-file fallback (Dockerfile, package.json, requirements.txt,
#      migrations/, *.groovy, src/*.css) when git signal is absent.
#
# Consumer (hooks/session-start.sh) passes the returned space-separated
# list to the scoring pipeline as one of the signals.
#
# Depends on caller having set $MEMORY_DIR.

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
