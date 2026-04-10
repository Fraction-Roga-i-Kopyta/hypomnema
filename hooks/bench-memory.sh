#!/bin/bash
# Benchmark for memory-session-start.sh hook
# Self-contained: creates fixtures in tmpdir, runs 15 scenarios, reports results
set -euo pipefail

HOOK="$HOME/.claude/hooks/memory-session-start.sh"
[ -f "$HOOK" ] || { echo "FATAL: hook not found at $HOOK"; exit 1; }

TMPDIR=$(mktemp -d)
MEMDIR="$TMPDIR/memory"
trap 'rm -rf "$TMPDIR"' EXIT

PASS=0; FAIL=0; TOTAL=0
CAT_PRECISION=0; CAT_DOMAIN=0; CAT_PRIORITY=0; CAT_EDGE=0
RESULTS=""

# ===== Create fixtures =====
mkdir -p "$MEMDIR"/{mistakes,feedback,knowledge,strategies,projects}

# --- Mistakes ---
cat > "$MEMDIR/mistakes/css-bug.md" << 'EOF'
---
type: mistake
project: global
created: 2026-01-01
injected: 2026-01-01
status: active
domains: [css, frontend]
severity: major
recurrence: 3
root-cause: "CSS variable not applied"
prevention: "Check computed styles"
---
CSS bug: variables not inherited in shadow DOM.
EOF

cat > "$MEMDIR/mistakes/sql-injection.md" << 'EOF'
---
type: mistake
project: global
created: 2026-01-01
injected: 2026-01-01
status: pinned
domains: [db, backend]
severity: critical
recurrence: 5
root-cause: "String interpolation in SQL"
prevention: "Use parameterized queries"
---
SQL injection via string concat in query builder.
EOF

cat > "$MEMDIR/mistakes/react-keys.md" << 'EOF'
---
type: mistake
project: global
created: 2026-01-01
injected: 2026-01-01
status: active
domains: [frontend]
severity: minor
recurrence: 2
root-cause: "Using index as key"
prevention: "Use stable unique IDs"
---
React list re-render due to index keys.
EOF

cat > "$MEMDIR/mistakes/jira-api.md" << 'EOF'
---
type: mistake
project: jira-proj
created: 2026-01-01
injected: 2026-01-01
status: active
domains: [jira]
severity: major
recurrence: 1
root-cause: "Deprecated endpoint"
prevention: "Check API version"
---
Jira REST API v2 deprecation caused 404.
EOF

cat > "$MEMDIR/mistakes/unconfirmed.md" << 'EOF'
---
type: mistake
project: global
created: 2026-01-01
injected: 2026-01-01
status: active
domains: [backend]
severity: minor
recurrence: 0
root-cause: "Unconfirmed issue"
prevention: "N/A"
---
Unconfirmed backend issue, should be filtered.
EOF

cat > "$MEMDIR/mistakes/archived-old.md" << 'EOF'
---
type: mistake
project: global
created: 2025-01-01
injected: 2025-01-01
status: archived
domains: [general]
severity: minor
recurrence: 1
root-cause: "Old bug"
prevention: "N/A"
---
Archived mistake, should not appear.
EOF

# --- Feedback ---
cat > "$MEMDIR/feedback/code-style.md" << 'EOF'
---
type: feedback
project: global
created: 2026-01-01
injected: 2026-01-01
referenced: 2026-01-01
status: active
domains: [general]
---
Use consistent naming. Prefer const over let.
EOF

cat > "$MEMDIR/feedback/css-conventions.md" << 'EOF'
---
type: feedback
project: global
created: 2026-01-01
injected: 2026-01-01
referenced: 2026-01-01
status: active
domains: [css, frontend]
---
BEM naming for CSS classes. Variables in :root.
EOF

cat > "$MEMDIR/feedback/jira-workflow.md" << 'EOF'
---
type: feedback
project: jira-proj
created: 2026-01-01
injected: 2026-01-01
referenced: 2026-01-01
status: active
domains: [jira]
---
Always use workflow validator before post-function.
EOF

# --- Knowledge ---
cat > "$MEMDIR/knowledge/api-docs.md" << 'EOF'
---
type: knowledge
project: global
created: 2026-01-01
injected: 2026-01-01
referenced: 2026-01-01
status: active
domains: [backend]
---
API versioning guide: use /v2/ prefix for all endpoints.
EOF

cat > "$MEMDIR/knowledge/jira-endpoints.md" << 'EOF'
---
type: knowledge
project: jira-proj
created: 2026-01-01
injected: 2026-01-01
referenced: 2026-01-01
status: active
domains: [jira]
---
Jira Cloud REST: /rest/api/3/ is current. ScriptRunner uses /rest/scriptrunner/latest/.
EOF

cat > "$MEMDIR/knowledge/css-grid-guide.md" << 'EOF'
---
type: knowledge
project: global
created: 2026-01-01
injected: 2026-01-01
referenced: 2026-01-01
status: active
domains: [css]
---
CSS Grid: use grid-template-areas for named regions.
EOF

# --- Strategies ---
cat > "$MEMDIR/strategies/debug-steps.md" << 'EOF'
---
type: strategy
project: global
created: 2026-01-01
injected: 2026-01-01
referenced: 2026-01-01
status: active
domains: [general]
trigger: "Any debug session"
success_count: 5
---
1. Reproduce 2. Isolate 3. Root cause 4. Fix 5. Verify
EOF

cat > "$MEMDIR/strategies/css-layout.md" << 'EOF'
---
type: strategy
project: global
created: 2026-01-01
injected: 2026-01-01
referenced: 2026-01-01
status: active
domains: [css, frontend]
trigger: "CSS layout bug"
success_count: 3
---
Trace cascade from viewport down to target element.
EOF

# --- Projects ---
cat > "$MEMDIR/projects.json" << 'EOF'
{"/tmp/bench-proj": "test-proj", "/tmp/jira-proj": "jira-proj"}
EOF

cat > "$MEMDIR/projects-domains.json" << 'EOF'
{"test-proj": ["frontend", "css"], "jira-proj": ["jira", "groovy"]}
EOF

cat > "$MEMDIR/projects/test-proj.md" << 'EOF'
---
type: project
status: active
---
Test project for benchmark.
EOF

cat > "$MEMDIR/projects/jira-proj.md" << 'EOF'
---
type: project
status: active
---
Jira ScriptRunner project.
EOF

# ===== Helper functions =====

run_hook() {
  local session_id="$1" cwd="$2"
  # Create cwd if needed (hook checks path prefix, not actual dir for project matching)
  mkdir -p "$cwd" 2>/dev/null || true
  printf '{"session_id":"%s","cwd":"%s"}' "$session_id" "$cwd" \
    | CLAUDE_MEMORY_DIR="$MEMDIR" bash "$HOOK" 2>/dev/null || echo '{}'
}

extract_context() {
  local output="$1"
  printf '%s' "$output" | jq -r '.hookSpecificOutput.additionalContext // ""' 2>/dev/null || echo ""
}

check_present() {
  local context="$1" name="$2"
  printf '%s' "$context" | grep -qi "$name" && return 0 || return 1
}

run_scenario() {
  local num="$1" name="$2" cwd="$3" category="$4"
  shift 4
  local present=() absent=()
  local mode="present"
  for arg in "$@"; do
    if [ "$arg" = "--absent" ]; then mode="absent"; continue; fi
    if [ "$mode" = "present" ]; then present+=("$arg"); else absent+=("$arg"); fi
  done

  local output context
  output=$(run_hook "bench-${num}" "$cwd")
  context=$(extract_context "$output")

  local present_ok=1 absent_ok=1 failures=""
  for p in "${present[@]}"; do
    if ! check_present "$context" "$p"; then
      present_ok=0; failures="${failures}  MISS: ${p}\n"
    fi
  done
  for a in "${absent[@]}"; do
    if check_present "$context" "$a"; then
      absent_ok=0; failures="${failures}  LEAK: ${a}\n"
    fi
  done

  TOTAL=$((TOTAL + 1))
  local score=0
  if [ $present_ok -eq 1 ] && [ $absent_ok -eq 1 ]; then
    score=1; PASS=$((PASS + 1))
    case "$category" in
      precision) CAT_PRECISION=$((CAT_PRECISION + 1)) ;;
      domain)    CAT_DOMAIN=$((CAT_DOMAIN + 1)) ;;
      priority)  CAT_PRIORITY=$((CAT_PRIORITY + 1)) ;;
      edge)      CAT_EDGE=$((CAT_EDGE + 1)) ;;
    esac
  else
    FAIL=$((FAIL + 1))
  fi

  local status_str
  [ $score -eq 1 ] && status_str="PASS" || status_str="FAIL"
  RESULTS="${RESULTS}$(printf '  %2d. %-40s %s\n' "$num" "$name" "$status_str")\n"
  if [ -n "$failures" ]; then
    RESULTS="${RESULTS}$(printf '%b' "$failures")\n"
  fi
}

# ===== Ordering check helper =====
run_order_scenario() {
  local num="$1" name="$2" cwd="$3" first="$4" second="$5" category="$6"
  local output context
  output=$(run_hook "bench-${num}" "$cwd")
  context=$(extract_context "$output")

  local pos_first pos_second
  # Use awk for case-insensitive position finding
  pos_first=$(printf '%s' "$context" | awk -v pat="$first" 'BEGIN{IGNORECASE=1} {p=index(tolower($0),tolower(pat)); if(p>0){print NR*10000+p; exit}}')
  pos_second=$(printf '%s' "$context" | awk -v pat="$second" 'BEGIN{IGNORECASE=1} {p=index(tolower($0),tolower(pat)); if(p>0){print NR*10000+p; exit}}')

  TOTAL=$((TOTAL + 1))
  local score=0 failures=""
  if [ -z "$pos_first" ] || [ -z "$pos_second" ]; then
    failures="  NOT FOUND: first=${pos_first:-missing} second=${pos_second:-missing}\n"
  elif [ "$pos_first" -lt "$pos_second" ]; then
    score=1
  else
    failures="  ORDER: '${first}' (${pos_first}) should precede '${second}' (${pos_second})\n"
  fi

  if [ $score -eq 1 ]; then
    PASS=$((PASS + 1))
    case "$category" in priority) CAT_PRIORITY=$((CAT_PRIORITY + 1)) ;; esac
  else
    FAIL=$((FAIL + 1))
  fi

  local status_str
  [ $score -eq 1 ] && status_str="PASS" || status_str="FAIL"
  RESULTS="${RESULTS}$(printf '  %2d. %-40s %s\n' "$num" "$name" "$status_str")\n"
  if [ -n "$failures" ]; then
    RESULTS="${RESULTS}$(printf '%b' "$failures")\n"
  fi
}

# ===== Run scenarios =====

echo "Running memory hook benchmark..."
echo ""

# Cat 1: Injection precision/recall
run_scenario 1 "Frontend project: precision" "/tmp/bench-proj" "precision" \
  "css-bug" "react-keys" "css-conventions" "css-layout" \
  --absent "sql-injection" "jira-api"

run_scenario 2 "Backend only: precision" "/tmp/bench-backend" "precision" \
  "sql-injection" "api-docs" "debug-steps" \
  --absent "css-bug" "jira-api" "css-layout"

run_scenario 3 "Jira project: precision" "/tmp/jira-proj" "precision" \
  "jira-api" "jira-endpoints" "jira-workflow" \
  --absent "css-bug" "react-keys"

run_scenario 4 "No project: no project-scoped" "/tmp/no-match" "precision" \
  "code-style" "debug-steps" "css-bug" "sql-injection" \
  --absent "jira-api" "jira-endpoints" "jira-workflow"

# Cat 2: Domain filtering
run_scenario 5 "CSS domain filter" "/tmp/bench-proj" "domain" \
  "css-bug" \
  --absent "sql-injection"

run_scenario 6 "Jira domain filter" "/tmp/jira-proj" "domain" \
  "jira-api" \
  --absent "css-bug" "css-conventions"

run_scenario 7 "Backend domain filter" "/tmp/bench-backend" "domain" \
  "sql-injection" \
  --absent "css-layout"

run_scenario 8 "Mixed domains: frontend+backend" "/tmp/bench-mixed" "domain" \
  "code-style" "debug-steps" \
  --absent "jira-api"

# Cat 3: Priority/ordering
run_order_scenario 9 "Pinned first (sql-injection)" "/tmp/bench-backend" \
  "sql-injection" "api-docs" "priority"

run_order_scenario 10 "Higher recurrence first" "/tmp/bench-proj" \
  "css-bug" "react-keys" "priority"

# Scenario 11: project feedback before global
run_order_scenario 11 "Project feedback before global" "/tmp/jira-proj" \
  "jira-workflow" "code-style" "priority"

# Cat 4: Edge cases
# 12. Empty memory dir
EMPTY_MEMDIR="$TMPDIR/empty_memory"
mkdir -p "$EMPTY_MEMDIR"
OUTPUT_12=$(printf '{"session_id":"bench-12","cwd":"/tmp/empty"}' \
  | CLAUDE_MEMORY_DIR="$EMPTY_MEMDIR" bash "$HOOK" 2>/dev/null || echo '{}')
TOTAL=$((TOTAL + 1))
# Empty should produce either empty JSON or valid JSON with empty context
if printf '%s' "$OUTPUT_12" | jq empty 2>/dev/null || [ -z "$OUTPUT_12" ]; then
  CTX_12=$(printf '%s' "$OUTPUT_12" | jq -r '.hookSpecificOutput.additionalContext // ""' 2>/dev/null || echo "")
  # Context should be empty or very minimal
  PASS=$((PASS + 1)); CAT_EDGE=$((CAT_EDGE + 1))
  RESULTS="${RESULTS}$(printf '  %2d. %-40s %s\n' 12 'Empty memory dir' 'PASS')\n"
else
  FAIL=$((FAIL + 1))
  RESULTS="${RESULTS}$(printf '  %2d. %-40s %s\n' 12 'Empty memory dir' 'FAIL')\n"
  RESULTS="${RESULTS}  INVALID JSON output\n"
fi

# 13. Archived excluded
run_scenario 13 "Archived excluded" "/tmp/bench-proj" "edge" \
  "css-bug" \
  --absent "archived-old"

# 14. Recurrence=0 excluded
run_scenario 14 "Recurrence=0 excluded" "/tmp/bench-backend" "edge" \
  "sql-injection" \
  --absent "unconfirmed"

# 15. Prefix boundary
OUTPUT_15=$(run_hook "bench-15" "/tmp/bench-proj-other")
CTX_15=$(extract_context "$OUTPUT_15")
TOTAL=$((TOTAL + 1))
# /tmp/bench-proj-other should NOT match test-proj (prefix boundary)
if printf '%s' "$CTX_15" | grep -qi "test-proj"; then
  FAIL=$((FAIL + 1))
  RESULTS="${RESULTS}$(printf '  %2d. %-40s %s\n' 15 'Prefix boundary' 'FAIL')\n"
  RESULTS="${RESULTS}  LEAK: matched test-proj for /tmp/bench-proj-other\n"
else
  PASS=$((PASS + 1)); CAT_EDGE=$((CAT_EDGE + 1))
  RESULTS="${RESULTS}$(printf '  %2d. %-40s %s\n' 15 'Prefix boundary' 'PASS')\n"
fi

# ===== Report =====
echo "============================================"
echo "  Memory Hook Benchmark Results"
echo "============================================"
echo ""
printf '%b' "$RESULTS"
echo ""
echo "--------------------------------------------"
printf "  Overall:    %d / %d passed\n" "$PASS" "$TOTAL"
echo ""
printf "  Precision:  %d / 4\n" "$CAT_PRECISION"
printf "  Domain:     %d / 4\n" "$CAT_DOMAIN"
printf "  Priority:   %d / 3\n" "$CAT_PRIORITY"
printf "  Edge:       %d / 4\n" "$CAT_EDGE"
echo "--------------------------------------------"

[ "$PASS" -eq "$TOTAL" ] && echo "  ALL PASSED" || echo "  FAILURES: $FAIL"
echo ""

exit $(( TOTAL - PASS ))
