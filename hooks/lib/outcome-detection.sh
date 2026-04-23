# hooks/lib/outcome-detection.sh
# shellcheck shell=bash
# Post-session outcome classification for injected mistakes.
#
# A mistake that was injected into the session and NOT subsequently
# flagged as repeated (via `outcome-negative` from transcript analysis)
# earns an `outcome-positive` — the Bayesian effectiveness numerator.
# Domain filtering prevents cross-domain mistakes from getting credit
# on unrelated sessions.
#
# Depends on caller having already sourced:
#   - lib/wal-lock.sh     (wal_append, wal_run_locked)
# and set:
#   $MEMORY_DIR, $SAFE_SESSION_ID, $CWD, $TODAY, $WAL_FILE

# classify_outcome_positive — walk this session's `inject` WAL events,
# emit `outcome-positive` for every injected mistake that isn't also
# tagged `outcome-negative` in the same session. Skips mistakes whose
# declared `domains:` don't intersect with the session's git-derived
# domain set (css/frontend/backend/db/groovy).
classify_outcome_positive() {
  [ -f "$WAL_FILE" ] || return 0
  [ -n "$SAFE_SESSION_ID" ] || return 0

  local INJECTED_MISTAKES NEGATIVE_MISTAKES
  INJECTED_MISTAKES=$(wal_run_locked awk -F'|' -v sid="$SAFE_SESSION_ID" '
    $4 == sid && $2 == "inject" { print $3 }
  ' "$WAL_FILE" 2>/dev/null)

  NEGATIVE_MISTAKES=$(wal_run_locked awk -F'|' -v sid="$SAFE_SESSION_ID" '
    $4 == sid && $2 == "outcome-negative" { print $3 }
  ' "$WAL_FILE" 2>/dev/null)

  [ -n "$INJECTED_MISTAKES" ] || return 0

  # Domain detection: classify changed files in the git working tree
  # under $CWD into a small set of domains. Used below as a filter so a
  # pythone-specific mistake doesn't get positive credit from a
  # css-only session.
  local SESSION_DOMAINS=""
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

  local mistake_name MISTAKE_FILE MISTAKE_DOMAINS DOMAIN_MATCH md sd
  while IFS= read -r mistake_name; do
    [ -z "$mistake_name" ] && continue
    if printf '%s\n' "$NEGATIVE_MISTAKES" | grep -qx "$mistake_name" 2>/dev/null; then
      continue
    fi
    MISTAKE_FILE="$MEMORY_DIR/mistakes/${mistake_name}.md"
    [ -f "$MISTAKE_FILE" ] || continue

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
}
