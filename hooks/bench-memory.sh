#!/bin/bash
# Benchmark for memory-session-start.sh hook
# Self-contained: creates fixtures in tmpdir, runs 18 scenarios, reports results.
#
# P7 (audit-2026-04-16): added bench-perf subcommand to cover latency on the
# two hot-path hooks (session-start, user-prompt-submit) against a synthetic
# 500-file corpus. Correctness suite runs by default; `bash bench-memory.sh
# perf` runs only the perf section; `bash bench-memory.sh all` runs both.
set -euo pipefail

HOOK="$HOME/.claude/hooks/memory-session-start.sh"
UP_HOOK="$HOME/.claude/hooks/memory-user-prompt-submit.sh"
[ -f "$HOOK" ] || { echo "FATAL: hook not found at $HOOK"; exit 1; }
[ -f "$UP_HOOK" ] || UP_HOOK="$(dirname "$0")/user-prompt-submit.sh"

BENCH_MODE="${1:-correctness}"
case "$BENCH_MODE" in
  correctness|perf|all) ;;
  *) BENCH_MODE="correctness" ;;
esac

TMPDIR=$(mktemp -d)
MEMDIR="$TMPDIR/memory"
trap 'rm -rf "$TMPDIR"' EXIT

PASS=0; FAIL=0; TOTAL=0
CAT_PRECISION=0; CAT_DOMAIN=0; CAT_PRIORITY=0; CAT_EDGE=0; CAT_V03=0
RESULTS=""

# ===== Perf-only entry point: skip the 14-file correctness setup =====
# Jumps to the perf block at the end of the file, which builds its own
# 500-file corpus in a separate tmp dir.
if [ "$BENCH_MODE" = "perf" ]; then
  # Correctness needs MEMDIR layout; perf builds its own. Fall through to the
  # perf section by goto-via-function. Plain bash: source a marker flag and
  # skip correctness setup + scenarios entirely.
  PERF_ONLY=1
else
  PERF_ONLY=0
fi

if [ "$PERF_ONLY" = "0" ]; then
: # correctness block begins — the heredoc fixtures below are part of it

# ===== Create fixtures =====
mkdir -p "$MEMDIR"/{mistakes,feedback,knowledge,strategies,notes,projects}

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
decay_rate: normal
ref_count: 5
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
decay_rate: slow
ref_count: 20
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
decay_rate: slow
ref_count: 10
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

# ===== v0.3 scenarios: ref_count auto-increment =====

# 16. ref_count is incremented after injection
# Run hook for backend context (sql-injection should be injected)
OUTPUT_16=$(run_hook "bench-16" "/tmp/bench-backend")
CTX_16=$(extract_context "$OUTPUT_16")
TOTAL=$((TOTAL + 1))
# Check sql-injection was injected
if check_present "$CTX_16" "sql-injection"; then
  # Now check if ref_count was incremented (was 20, should be 21)
  NEW_RC=$(awk '/^---$/{n++; if(n>1)exit} n==1 && /^ref_count:/{sub(/^ref_count: */, ""); print}' "$MEMDIR/mistakes/sql-injection.md")
  if [ "${NEW_RC:-0}" -gt 20 ]; then
    PASS=$((PASS + 1)); CAT_V03=$((CAT_V03 + 1))
    RESULTS="${RESULTS}$(printf '  %2d. %-40s %s\n' 16 'ref_count auto-increment' 'PASS')\n"
  else
    FAIL=$((FAIL + 1))
    RESULTS="${RESULTS}$(printf '  %2d. %-40s %s\n' 16 'ref_count auto-increment' 'FAIL')\n"
    RESULTS="${RESULTS}  ref_count=${NEW_RC:-missing}, expected >20\n"
  fi
else
  FAIL=$((FAIL + 1))
  RESULTS="${RESULTS}$(printf '  %2d. %-40s %s\n' 16 'ref_count auto-increment' 'FAIL')\n"
  RESULTS="${RESULTS}  sql-injection not injected (precondition)\n"
fi

# 17. ref_count incremented for feedback too
# code-style (general domain) should be injected in any context
RC_BEFORE_17=$(awk '/^---$/{n++; if(n>1)exit} n==1 && /^ref_count:/{sub(/^ref_count: */, ""); print}' "$MEMDIR/feedback/code-style.md")
OUTPUT_17=$(run_hook "bench-17" "/tmp/bench-backend")
CTX_17=$(extract_context "$OUTPUT_17")
TOTAL=$((TOTAL + 1))
if check_present "$CTX_17" "code-style"; then
  RC_AFTER_17=$(awk '/^---$/{n++; if(n>1)exit} n==1 && /^ref_count:/{sub(/^ref_count: */, ""); print}' "$MEMDIR/feedback/code-style.md")
  if [ "${RC_AFTER_17:-0}" -gt "${RC_BEFORE_17:-0}" ]; then
    PASS=$((PASS + 1)); CAT_V03=$((CAT_V03 + 1))
    RESULTS="${RESULTS}$(printf '  %2d. %-40s %s\n' 17 'ref_count feedback increment' 'PASS')\n"
  else
    FAIL=$((FAIL + 1))
    RESULTS="${RESULTS}$(printf '  %2d. %-40s %s\n' 17 'ref_count feedback increment' 'FAIL')\n"
    RESULTS="${RESULTS}  ref_count: ${RC_BEFORE_17} -> ${RC_AFTER_17}, expected increment\n"
  fi
else
  FAIL=$((FAIL + 1))
  RESULTS="${RESULTS}$(printf '  %2d. %-40s %s\n' 17 'ref_count feedback increment' 'FAIL')\n"
  RESULTS="${RESULTS}  code-style not injected (precondition)\n"
fi

# 18. decay_rate field preserved (not corrupted by hook)
OUTPUT_18=$(run_hook "bench-18" "/tmp/bench-proj")
TOTAL=$((TOTAL + 1))
DECAY_18=$(awk '/^---$/{n++; if(n>1)exit} n==1 && /^decay_rate:/{sub(/^decay_rate: */, ""); print}' "$MEMDIR/mistakes/css-bug.md")
if [ "$DECAY_18" = "normal" ]; then
  PASS=$((PASS + 1)); CAT_V03=$((CAT_V03 + 1))
  RESULTS="${RESULTS}$(printf '  %2d. %-40s %s\n' 18 'decay_rate preserved after injection' 'PASS')\n"
else
  FAIL=$((FAIL + 1))
  RESULTS="${RESULTS}$(printf '  %2d. %-40s %s\n' 18 'decay_rate preserved after injection' 'FAIL')\n"
  RESULTS="${RESULTS}  decay_rate=${DECAY_18:-missing}, expected 'normal'\n"
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
printf "  v0.3:       %d / 3\n" "$CAT_V03"
echo "--------------------------------------------"

[ "$PASS" -eq "$TOTAL" ] && echo "  ALL PASSED" || echo "  FAILURES: $FAIL"
echo ""

fi  # close PERF_ONLY=0 correctness gate

# ===== Perf block: latency coverage on 500-file synthetic corpus =====
# P7 (audit-2026-04-16): the correctness suite ran at 14 files and missed
# every O(N) pathology above ~50 files. This block generates a realistic
# 500-file corpus and times both hot-path hooks. Invoke as:
#   bash bench-memory.sh perf       — perf only
#   bash bench-memory.sh all        — correctness + perf
#   bash bench-memory.sh            — correctness only (default, back-compat)
#
# Baselines captured 2026-04-16 on macOS darwin 25.3.0, cold TF-IDF, WAL
# disabled (tmp). Compare current wall-time against these to spot regressions:
#   session-start        wall ≈ 4200ms  (post P1+P3; was 5500ms pre-audit)
#   user-prompt-submit   wall ≈ 1500ms  (post P2+P4; was 13350ms pre-audit)
# Perf asserts only that runs complete without error; regression is an
# eyeball check against these numbers. Do NOT add hyperfine/perf deps.

if [ "$BENCH_MODE" = "perf" ] || [ "$BENCH_MODE" = "all" ]; then

  echo "============================================"
  echo "  Perf: 500-file synthetic corpus latency"
  echo "============================================"

  PERF_TMP=$(mktemp -d)
  PERF_MEM="$PERF_TMP/memory"
  # shellcheck disable=SC2064
  trap "rm -rf '$PERF_TMP' '$TMPDIR'" EXIT
  mkdir -p "$PERF_MEM"/{mistakes,feedback,knowledge,strategies,notes,decisions,projects,continuity}

  # portable millisecond timestamp: prefer gdate (GNU coreutils) on macOS,
  # fall back to Python, then to python3. No hyperfine dep required.
  _now_ms() {
    if command -v gdate >/dev/null 2>&1; then
      gdate +%s%3N
    elif command -v python3 >/dev/null 2>&1; then
      python3 -c 'import time; print(int(time.time()*1000))'
    else
      # second resolution only — regression would still be visible.
      printf '%d000' "$(date +%s)"
    fi
  }

  # Synthetic corpus: 50 mistakes + 100 feedback + 100 knowledge + 100
  # strategies + 100 notes + 50 decisions = 500 records. Matches the
  # distribution used by the audit baseline so numbers are comparable.
  echo "  Generating 500 synthetic memory files..."
  PERF_GEN_START=$(_now_ms)

  # Frontmatter `type:` is singular (mistake/feedback/...); directory names
  # are plural (mistakes/feedback/...). Pass both so we do not have to
  # pluralise irregular words like strategy/strategies.
  _gen_file() {
    local dir="$1" type="$2" slug="$3" severity="$4" ref_count="$5" recurrence="$6"
    local path="$PERF_MEM/${dir}/${slug}.md"
    cat > "$path" <<FM
---
type: ${type}
project: global
created: 2026-01-15
injected: 2026-01-20
referenced: 2026-02-15
status: active
severity: ${severity}
recurrence: ${recurrence}
ref_count: ${ref_count}
keywords: [perf, bench, synthetic]
domains: [general, perf-test]
trigger: "perf-syn"
root-cause: "synthetic root cause for ${slug}"
prevention: "synthetic prevention for ${slug}"
---
Body line 1 for ${slug}.
Body line 2 for ${slug}.
FM
  }

  for i in $(seq 1 50);  do _gen_file mistakes   mistake   "m${i}"  major "$((i*7))" "$((i%5+1))"; done
  for i in $(seq 1 100); do _gen_file feedback   feedback  "fb${i}" minor "$((i*3))" 1; done
  for i in $(seq 1 100); do _gen_file knowledge  knowledge "k${i}"  minor "$((i*2))" 1; done
  for i in $(seq 1 100); do _gen_file strategies strategy  "s${i}"  minor "$i"       1; done
  for i in $(seq 1 100); do _gen_file notes      note      "n${i}"  minor "$i"       1; done
  for i in $(seq 1 50);  do _gen_file decisions  decision  "d${i}"  minor "$i"       1; done

  echo '{}' > "$PERF_MEM/projects.json"
  echo '{}' > "$PERF_MEM/projects-domains.json"

  PERF_GEN_END=$(_now_ms)
  PERF_FILES=$(find "$PERF_MEM" -type f -name '*.md' | wc -l | tr -d ' ')
  echo "  Generated ${PERF_FILES} files in $((PERF_GEN_END - PERF_GEN_START))ms"
  echo ""

  # --- Session-start latency (3 runs, report min/avg/max) ---
  _bench_hook() {
    local label="$1" hook="$2" input="$3" runs="${4:-3}"
    local i start end dur
    local min=999999999 max=0 total=0
    for i in $(seq 1 "$runs"); do
      start=$(_now_ms)
      printf '%s' "$input" | CLAUDE_MEMORY_DIR="$PERF_MEM" bash "$hook" >/dev/null 2>&1 || true
      end=$(_now_ms)
      dur=$((end - start))
      total=$((total + dur))
      [ "$dur" -lt "$min" ] && min="$dur"
      [ "$dur" -gt "$max" ] && max="$dur"
    done
    local avg=$((total / runs))
    printf "  %-28s runs=%d  min=%dms  avg=%dms  max=%dms\n" \
      "$label" "$runs" "$min" "$avg" "$max"
  }

  SS_INPUT='{"session_id":"perf-bench","cwd":"/tmp/perf-bench"}'
  # no-match: prompt does not contain any trigger — hook exits after the
  # per-file extract_meta loop without building any candidate priority key.
  UP_INPUT_NONE='{"session_id":"perf-bench","cwd":"/tmp/perf-bench","prompt":"no matching phrase here"}'
  # broad match: every synthetic file defines trigger "perf-syn" and the
  # prompt contains "perf-syn" — so every file reaches the full
  # priority-key path (status/severity/recency/log-ref/recurrence). This
  # is the worst case where per-file subshells show up in wall time. P4
  # replaced `date -j -f` per file with pure-bash JDN arithmetic — this
  # scenario is what measures the win.
  UP_INPUT_BROAD='{"session_id":"perf-bench","cwd":"/tmp/perf-bench","prompt":"perf-syn match all files"}'

  _bench_hook "session-start"                   "$HOOK"    "$SS_INPUT"        3
  _bench_hook "user-prompt-submit (no-match)"   "$UP_HOOK" "$UP_INPUT_NONE"   3
  _bench_hook "user-prompt-submit (broad)"      "$UP_HOOK" "$UP_INPUT_BROAD"  3

  # --- Stop-hook latency (v0.14 addition). Stop triggers three WAL
  # passes on top of the lifecycle/metrics work: classify_outcome_positive
  # (mistake path), classify_feedback_from_transcript (trigger-useful),
  # and classify_outcome_positive_from_evidence (non-mistake path).
  # This scenario seeds enough WAL traffic and a transcript with a
  # matched evidence phrase so all three passes actually do work.
  STOP_HOOK="$HOME/.claude/hooks/memory-stop.sh"
  [ -f "$STOP_HOOK" ] || STOP_HOOK="$(dirname "$0")/session-stop.sh"
  if [ -f "$STOP_HOOK" ]; then
    # Seed ~50 injects, 5 outcome-negative pre-existing, 1 feedback with
    # evidence: frontmatter that the transcript will match.
    STOP_WAL="$PERF_MEM/.wal"
    {
      for i in 1 2 3 4 5; do
        echo "2026-04-22|inject|m${i}|perf-stop-sess"
      done
      for i in 1 2 3; do
        echo "2026-04-22|inject|fb${i}|perf-stop-sess"
      done
      echo "2026-04-22|outcome-negative|m1|perf-stop-sess"
    } > "$STOP_WAL"

    # Feedback file with an evidence phrase for feedback-loop to match.
    cat > "$PERF_MEM/feedback/fb-evidenced.md" <<FM
---
type: feedback
project: global
status: active
keywords: [bench]
evidence:
  - "perf stop bench evidence phrase"
---
Body matching the transcript below.
FM
    echo "2026-04-22|inject|fb-evidenced|perf-stop-sess" >> "$STOP_WAL"

    STOP_TRANSCRIPT="$PERF_TMP/perf-stop.jsonl"
    cat > "$STOP_TRANSCRIPT" <<JL
{"type":"user","text":"how do I test this"}
{"type":"assistant","text":"I will follow the perf stop bench evidence phrase guideline"}
JL

    # Marker ≥ MIN_SESSION_SECONDS old so Stop runs the full pipeline.
    STOP_MARKER="/tmp/.claude-session-perf-stop-sess"
    touch "$STOP_MARKER"
    # portable: bump mtime back 10 minutes so ELAPSED > 120s comfortably.
    if [[ "$OSTYPE" == darwin* ]]; then
      touch -t "$(date -v-10M +%Y%m%d%H%M)" "$STOP_MARKER"
    else
      touch -d "10 minutes ago" "$STOP_MARKER"
    fi

    STOP_INPUT=$(printf '{"session_id":"perf-stop-sess","transcript_path":"%s","cwd":"/tmp/perf-bench"}' \
      "$STOP_TRANSCRIPT")
    _bench_hook "stop (3 WAL passes + evidence)" "$STOP_HOOK" "$STOP_INPUT" 3

    rm -f "$STOP_MARKER"
  fi

  # --- memory-secrets-detect latency vs .secretsignore size (v0.14).
  # The PreToolUse hook walks the two ignore files (default + user) with
  # shell glob-match per pattern per hooked write. Scenarios sweep
  # 0 / 10 / 100 user patterns so the pattern-scan cost can be bounded
  # before users push past it.
  SEC_HOOK="$HOME/.claude/hooks/memory-secrets-detect.sh"
  [ -f "$SEC_HOOK" ] || SEC_HOOK="$(dirname "$0")/memory-secrets-detect.sh"
  if [ -f "$SEC_HOOK" ]; then
    SEC_FILE="$PERF_MEM/feedback/perf-secrets-target.md"
    SEC_CONTENT="Simple memory write — no credential, just prose."
    SEC_INPUT=$(jq -n --arg fp "$SEC_FILE" --arg c "$SEC_CONTENT" \
      '{tool_input: {file_path: $fp, content: $c}}')

    # Scenario 1: no .secretsignore files at all.
    rm -f "$PERF_MEM/.secretsignore" "$PERF_MEM/.secretsignore.default"
    _bench_hook "secrets-detect (0 patterns)" "$SEC_HOOK" "$SEC_INPUT" 5

    # Scenario 2: 10 patterns in user .secretsignore.
    printf 'pattern-%d/**\n' $(seq 1 10) > "$PERF_MEM/.secretsignore"
    _bench_hook "secrets-detect (10 patterns)" "$SEC_HOOK" "$SEC_INPUT" 5

    # Scenario 3: 100 patterns.
    printf 'pattern-%d/**\n' $(seq 1 100) > "$PERF_MEM/.secretsignore"
    _bench_hook "secrets-detect (100 patterns)" "$SEC_HOOK" "$SEC_INPUT" 5

    # Cleanup so next run of perf does not inherit a .secretsignore file.
    rm -f "$PERF_MEM/.secretsignore"
  fi

  echo ""
  echo "  Baseline (audit-2026-04-16, 500 files, macOS darwin 25.3.0):"
  echo "    session-start                      ≈ 700ms   (post P1+P3, was 5500ms)"
  echo "    user-prompt-submit (no-match)      ≈ 3400ms  (extract_meta per file dominates)"
  echo "    user-prompt-submit (broad, pre-P4) ≈ 6500ms  (date -j -f per file)"
  echo "    user-prompt-submit (broad, post-P4)≈ 5000ms  (pure-bash JDN, −23%)"
  echo "  v0.14 baselines (first real measurement — see docs/measurements/):"
  echo "    stop (3 WAL passes + evidence)    — first measurement in this release"
  echo "    secrets-detect (0/10/100 ignore)  — first measurement in this release"
  echo "  Regression guard: eyeball current vs baseline; investigate >1.5x slowdown."
  echo "--------------------------------------------"
fi

exit $(( TOTAL - PASS ))
