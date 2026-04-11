# Meta-analytics & Positive Patterns Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add outcome-positive tracking, Bayesian effectiveness scoring, batch analytics, and strategies pipeline to hypomnema memory system.

**Architecture:** Three phases — WAL events (foundation), scoring v2 + strategies pipeline, batch analytics. Each phase is independently testable. All changes are additive — old WAL without new events degrades gracefully to scoring v1 behavior.

**Tech Stack:** Bash (BSD awk, jq, perl), existing hook infrastructure. No new dependencies.

**Spec:** `docs/specs/2026-04-11-meta-analytics-positive-patterns-design.md`

**Test runner:** `bash hooks/test-memory-hooks.sh` (add new tests at the end)

**Bench runner:** `bash hooks/bench-memory.sh` (validate scoring changes don't regress)

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `hooks/session-stop.sh` | Modify | Add outcome-positive, session-metrics, clean-session, strategy-used, strategy-gap, analytics trigger, strategies reminder |
| `hooks/session-start.sh` | Modify | Bayesian effectiveness in WAL scoring, strategy bonus, noise penalty |
| `hooks/memory-precompact.sh` | Modify | Add strategies prompt to reminder |
| `hooks/wal-compact.sh` | Modify | Preserve outcome/metrics events, aggregate old session-metrics |
| `hooks/memory-analytics.sh` | Create | Batch WAL analysis, generates .analytics-report |
| `hooks/test-memory-hooks.sh` | Modify | Tests for all new functionality |

---

## Phase 1: WAL Events (Foundation)

### Task 1: Outcome-positive detection in Stop hook

**Files:**
- Modify: `hooks/session-stop.sh` (after transcript error detection block, ~line 261)
- Modify: `hooks/test-memory-hooks.sh` (append new tests)

- [ ] **Step 1: Write the failing test for outcome-positive**

Append to `hooks/test-memory-hooks.sh` before the cleanup/summary section:

```bash
# Test: outcome-positive — injected mistake not repeated → positive
OP_DIR=$(mktemp -d)
OP_MEM="$OP_DIR/memory"
mkdir -p "$OP_MEM"/{mistakes,projects}
cat > "$OP_MEM/projects.json" << 'EOF'
{}
EOF
cat > "$OP_MEM/mistakes/css-var-bug.md" << 'EOF'
---
type: mistake
project: global
status: active
severity: major
recurrence: 3
domains: [css, frontend]
root-cause: "CSS var not applied"
prevention: "Check computed styles"
---
EOF
# WAL: mistake was injected in this session, no outcome-negative
printf '2026-04-11|inject|css-var-bug|op-test-session\n' > "$OP_MEM/.wal"
# Session marker (old enough to pass MIN_SESSION_SECONDS)
OP_MARKER="/tmp/.claude-session-op-test-session"
touch -t 202601010000 "$OP_MARKER"
# Transcript with tool calls but no errors
cat > "$OP_DIR/transcript.jsonl" << 'OPEOF'
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"test.css"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"OK"}]}}
OPEOF
echo '{"session_id":"op-test-session","cwd":"/tmp","transcript_path":"'"$OP_DIR"'/transcript.jsonl"}' | CLAUDE_MEMORY_DIR="$OP_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null
assert "Outcome-positive — written to WAL" 'grep -q "outcome-positive|css-var-bug" "$OP_MEM/.wal"'
rm -rf "$OP_DIR" "$OP_MARKER"

# Test: outcome-positive — skipped when outcome-negative exists
ON_DIR=$(mktemp -d)
ON_MEM="$ON_DIR/memory"
mkdir -p "$ON_MEM"/{mistakes,projects}
cat > "$ON_MEM/projects.json" << 'EOF'
{}
EOF
cat > "$ON_MEM/mistakes/repeated-bug.md" << 'EOF'
---
type: mistake
project: global
status: active
severity: major
recurrence: 3
domains: [general]
root-cause: "Repeated bug"
prevention: "Stop repeating"
---
EOF
# WAL: injected AND outcome-negative in same session
printf '2026-04-11|inject|repeated-bug|on-test-session\n2026-04-11|outcome-negative|repeated-bug|on-test-session\n' > "$ON_MEM/.wal"
ON_MARKER="/tmp/.claude-session-on-test-session"
touch -t 202601010000 "$ON_MARKER"
cat > "$ON_DIR/transcript.jsonl" << 'ONEOF'
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"echo test"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"test"}]}}
ONEOF
echo '{"session_id":"on-test-session","cwd":"/tmp","transcript_path":"'"$ON_DIR"'/transcript.jsonl"}' | CLAUDE_MEMORY_DIR="$ON_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null
assert "Outcome-positive — not written when negative exists" '! grep -q "outcome-positive|repeated-bug" "$ON_MEM/.wal"'
rm -rf "$ON_DIR" "$ON_MARKER"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: 2 FAIL — "Outcome-positive — written to WAL" and "Outcome-positive — not written when negative exists"

- [ ] **Step 3: Implement outcome-positive detection in session-stop.sh**

Add after the transcript error detection block (after `fi` closing `if [ -n "$ERROR_SUMMARY" ]`, before `REMINDER` output section). Insert before `if [ -n "$REMINDER" ]; then`:

```bash
# --- Outcome-positive detection ---
# Injected mistakes that were NOT repeated → positive signal
if [ -f "$WAL_FILE" ] && [ -n "$SAFE_SESSION_ID" ]; then
  # Get all mistakes injected in this session
  INJECTED_MISTAKES=$(awk -F'|' -v sid="$SAFE_SESSION_ID" '
    $4 == sid && $2 == "inject" { print $3 }
  ' "$WAL_FILE" 2>/dev/null)

  # Get mistakes with outcome-negative in this session
  NEGATIVE_MISTAKES=$(awk -F'|' -v sid="$SAFE_SESSION_ID" '
    $4 == sid && $2 == "outcome-negative" { print $3 }
  ' "$WAL_FILE" 2>/dev/null)

  if [ -n "$INJECTED_MISTAKES" ]; then
    # Detect session domains for domain filtering
    SESSION_DOMAINS=""
    if [ -n "$CWD" ]; then
      # Reuse detect_domains if available, otherwise use simple heuristic
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

    while IFS= read -r mistake_name; do
      [ -z "$mistake_name" ] && continue

      # Skip if outcome-negative already recorded
      if printf '%s\n' "$NEGATIVE_MISTAKES" | grep -qx "$mistake_name" 2>/dev/null; then
        continue
      fi

      # Only count as positive if it's a mistake file
      MISTAKE_FILE="$MEMORY_DIR/mistakes/${mistake_name}.md"
      [ -f "$MISTAKE_FILE" ] || continue

      # Domain filter: skip if mistake domains don't intersect with session domains
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

      printf '%s|outcome-positive|%s|%s\n' "$TODAY" "$mistake_name" "$SAFE_SESSION_ID" >> "$WAL_FILE" 2>/dev/null
    done <<< "$INJECTED_MISTAKES"
  fi
fi
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add hooks/session-stop.sh hooks/test-memory-hooks.sh
git commit -m "feat: outcome-positive detection in Stop hook"
```

---

### Task 2: Session metrics and clean-session events

**Files:**
- Modify: `hooks/session-stop.sh` (after outcome-positive block)
- Modify: `hooks/test-memory-hooks.sh` (append tests)

- [ ] **Step 1: Write failing tests**

Append to `hooks/test-memory-hooks.sh`:

```bash
# Test: session-metrics written to WAL
SM_DIR=$(mktemp -d)
SM_MEM="$SM_DIR/memory"
mkdir -p "$SM_MEM/projects"
cat > "$SM_MEM/projects.json" << 'EOF'
{}
EOF
SM_MARKER="/tmp/.claude-session-sm-test-session"
touch -t 202601010000 "$SM_MARKER"
# Transcript with 3 tool_use calls and 1 error
cat > "$SM_DIR/transcript.jsonl" << 'SMEOF'
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"npm run build"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"Error: fail","is_error":true}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":"x.ts"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"OK"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"npm run build"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t3","content":"OK"}]}}
SMEOF
echo '{"session_id":"sm-test-session","cwd":"/tmp","transcript_path":"'"$SM_DIR"'/transcript.jsonl"}' | CLAUDE_MEMORY_DIR="$SM_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null
assert "Session metrics — written to WAL" 'grep -q "session-metrics" "$SM_MEM/.wal" 2>/dev/null'
assert "Session metrics — has error_count" 'grep "session-metrics" "$SM_MEM/.wal" | grep -q "error_count:1"'
assert "Session metrics — has tool_calls" 'grep "session-metrics" "$SM_MEM/.wal" | grep -q "tool_calls:3"'
assert "Session metrics — not clean (has errors)" '! grep -q "clean-session" "$SM_MEM/.wal"'
rm -rf "$SM_DIR" "$SM_MARKER"

# Test: clean-session when 0 errors
CS_DIR=$(mktemp -d)
CS_MEM="$CS_DIR/memory"
mkdir -p "$CS_MEM/projects"
cat > "$CS_MEM/projects.json" << 'EOF'
{}
EOF
CS_MARKER="/tmp/.claude-session-cs-test-session"
touch -t 202601010000 "$CS_MARKER"
cat > "$CS_DIR/transcript.jsonl" << 'CSEOF'
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"x.ts"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"OK"}]}}
CSEOF
echo '{"session_id":"cs-test-session","cwd":"/tmp","transcript_path":"'"$CS_DIR"'/transcript.jsonl"}' | CLAUDE_MEMORY_DIR="$CS_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null
assert "Clean session — written to WAL" 'grep -q "clean-session" "$CS_MEM/.wal"'
rm -rf "$CS_DIR" "$CS_MARKER"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: 5 FAIL

- [ ] **Step 3: Implement session-metrics and clean-session in session-stop.sh**

Add after the outcome-positive block, before `REMINDER` output:

```bash
# --- Session metrics ---
if [ -n "$TRANSCRIPT_PATH" ] && [ -f "$TRANSCRIPT_PATH" ]; then
  TOOL_CALLS=$(grep -c '"tool_use"' "$TRANSCRIPT_PATH" 2>/dev/null || echo 0)
  # error_count: reuse ERROR_SUMMARY from transcript error detection
  METRIC_ERROR_COUNT=$(printf '%s\n' "$ERROR_SUMMARY" | grep -c '.' 2>/dev/null || echo 0)
  METRIC_DURATION=$ELAPSED

  # Detect session domains (reuse SESSION_DOMAINS if set, else compute)
  METRIC_DOMAINS="${SESSION_DOMAINS:-unknown}"
  [ -z "$METRIC_DOMAINS" ] && METRIC_DOMAINS="unknown"
  METRIC_DOMAINS=$(printf '%s' "$METRIC_DOMAINS" | tr ' ' ',')
  [ -z "$METRIC_DOMAINS" ] && METRIC_DOMAINS="unknown"

  printf '%s|session-metrics|%s|error_count:%s,tool_calls:%s,duration:%ss\n' \
    "$TODAY" "$METRIC_DOMAINS" "$METRIC_ERROR_COUNT" "$TOOL_CALLS" "$METRIC_DURATION" \
    >> "$WAL_FILE" 2>/dev/null

  # Clean session: 0 errors and sufficient duration
  if [ "${METRIC_ERROR_COUNT:-0}" -eq 0 ]; then
    printf '%s|clean-session|%s|%s\n' "$TODAY" "$METRIC_DOMAINS" "$SAFE_SESSION_ID" \
      >> "$WAL_FILE" 2>/dev/null
  fi
fi
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add hooks/session-stop.sh hooks/test-memory-hooks.sh
git commit -m "feat: session-metrics and clean-session WAL events"
```

---

### Task 3: Update wal-compact.sh for new events

**Files:**
- Modify: `hooks/wal-compact.sh`
- Modify: `hooks/test-memory-hooks.sh` (append test)

- [ ] **Step 1: Write failing test**

Append to `hooks/test-memory-hooks.sh`:

```bash
# Test: WAL compaction preserves outcome and metrics events
WALC2_DIR=$(mktemp -d)
WALC2_MEM="$WALC2_DIR/memory"
mkdir -p "$WALC2_MEM"
{
  # Old entries (>14 days ago) — inject should aggregate, outcomes should preserve
  for day in $(seq 20 50); do
    if [[ "$OSTYPE" == darwin* ]]; then
      d=$(date -v-${day}d +%Y-%m-%d 2>/dev/null)
    else
      d=$(date -d "${day} days ago" +%Y-%m-%d)
    fi
    for i in $(seq 1 30); do
      echo "${d}|inject|test-file|sess-${day}-${i}"
    done
    echo "${d}|outcome-positive|test-file|sess-${day}-1"
    echo "${d}|outcome-negative|other-file|sess-${day}-2"
    echo "${d}|session-metrics|css,frontend|error_count:1,tool_calls:5,duration:300s"
    echo "${d}|clean-session|backend|sess-${day}-3"
    echo "${d}|strategy-used|my-strat|sess-${day}-4"
    echo "${d}|strategy-gap|frontend|sess-${day}-5"
  done
} > "$WALC2_MEM/.wal"
CLAUDE_MEMORY_DIR="$WALC2_MEM" bash "$HOME/.claude/hooks/wal-compact.sh" 2>/dev/null
assert "Compact v2 — outcome-positive preserved" 'grep -q "outcome-positive" "$WALC2_MEM/.wal"'
assert "Compact v2 — outcome-negative preserved" 'grep -q "outcome-negative" "$WALC2_MEM/.wal"'
assert "Compact v2 — clean-session preserved" 'grep -q "clean-session" "$WALC2_MEM/.wal"'
assert "Compact v2 — strategy-used preserved" 'grep -q "strategy-used" "$WALC2_MEM/.wal"'
assert "Compact v2 — inject aggregated" 'grep -q "inject-agg" "$WALC2_MEM/.wal"'
assert "Compact v2 — session-metrics aggregated" 'grep -q "metrics-agg" "$WALC2_MEM/.wal"'
rm -rf "$WALC2_DIR"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: FAIL on "session-metrics aggregated" (and possibly others since compact doesn't know about new events yet — but they may pass since old_other catch-all preserves unknowns)

- [ ] **Step 3: Update wal-compact.sh**

Replace the entire awk block in `hooks/wal-compact.sh`:

```bash
# Single-pass: split into recent (raw) and old (aggregated)
# Old inject entries -> aggregated counts
# Old session-metrics -> monthly aggregates (avg error rate, total sessions)
# All other old events (outcomes, clean-session, strategy-*) -> preserved as-is
awk -F'|' -v cutoff="$CUTOFF" '
  $1 >= cutoff { recent[++rn] = $0; next }
  $2 == "inject" { agg[$1 "|" $3]++; next }
  $2 == "inject-agg" { agg[$1 "|" $3] += $4; next }
  $2 == "session-metrics" {
    # Aggregate to monthly: YYYY-MM
    month = substr($1, 1, 7)
    metrics_count[month]++
    # Parse error_count from field 4
    ec = $4; sub(/.*error_count:/, "", ec); sub(/,.*/, "", ec)
    metrics_errors[month] += ec + 0
    # Parse tool_calls
    tc = $4; sub(/.*tool_calls:/, "", tc); sub(/,.*/, "", tc)
    metrics_tools[month] += tc + 0
    # Parse duration
    dur = $4; sub(/.*duration:/, "", dur); sub(/s.*/, "", dur)
    metrics_duration[month] += dur + 0
    next
  }
  { old_other[++on] = $0 }
  END {
    # Print aggregated old injects
    for (key in agg) {
      split(key, k, "|")
      printf "%s|inject-agg|%s|%d\n", k[1], k[2], agg[key]
    }
    # Print aggregated session-metrics (monthly)
    for (month in metrics_count) {
      n = metrics_count[month]
      avg_err = metrics_errors[month] / n
      avg_tools = metrics_tools[month] / n
      total_dur = metrics_duration[month]
      printf "%s-01|metrics-agg|sessions:%d,avg_errors:%.1f,avg_tools:%.1f,total_duration:%ds\n", \
        month, n, avg_err, avg_tools, total_dur
    }
    # Print preserved old non-inject entries (outcomes, clean-session, strategy-*)
    for (i = 1; i <= on; i++) print old_other[i]
    # Print recent entries
    for (i = 1; i <= rn; i++) print recent[i]
  }
' "$WAL_FILE" | sort -t'|' -k1,1 > "$WAL_FILE.tmp" 2>/dev/null
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add hooks/wal-compact.sh hooks/test-memory-hooks.sh
git commit -m "feat: wal-compact handles outcome/metrics events"
```

---

## Phase 2: Scoring v2 + Strategies Pipeline

### Task 4: Bayesian effectiveness in session-start.sh scoring

**Files:**
- Modify: `hooks/session-start.sh` (WAL scoring awk block, ~line 394-447)
- Modify: `hooks/test-memory-hooks.sh` (append test)

- [ ] **Step 1: Write failing test**

Append to `hooks/test-memory-hooks.sh`:

```bash
# Test: Bayesian effectiveness — positive outcomes boost score
BAYES_DIR=$(mktemp -d)
BAYES_MEM="$BAYES_DIR/memory"
mkdir -p "$BAYES_MEM"/{feedback,projects}
cat > "$BAYES_MEM/projects.json" << 'EOF'
{}
EOF
cat > "$BAYES_MEM/projects-domains.json" << 'EOF'
{}
EOF
cat > "$BAYES_MEM/feedback/proven-good.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2026-04-01
---
Proven effective feedback.
EOF
cat > "$BAYES_MEM/feedback/proven-bad.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2026-04-01
---
Proven ineffective feedback.
EOF
# WAL: proven-good has 3 positive outcomes, proven-bad has 3 negative
{
  echo "2026-04-05|inject|proven-good|s1"
  echo "2026-04-06|inject|proven-good|s2"
  echo "2026-04-07|inject|proven-good|s3"
  echo "2026-04-05|outcome-positive|proven-good|s1"
  echo "2026-04-06|outcome-positive|proven-good|s2"
  echo "2026-04-07|outcome-positive|proven-good|s3"
  echo "2026-04-05|inject|proven-bad|s1"
  echo "2026-04-06|inject|proven-bad|s2"
  echo "2026-04-07|inject|proven-bad|s3"
  echo "2026-04-05|outcome-negative|proven-bad|s1"
  echo "2026-04-06|outcome-negative|proven-bad|s2"
  echo "2026-04-07|outcome-negative|proven-bad|s3"
} > "$BAYES_MEM/.wal"
BAYES_OUT=$(printf '{"session_id":"bayes-test","cwd":"/tmp"}' | CLAUDE_MEMORY_DIR="$BAYES_MEM" bash "$HOOK" 2>/dev/null)
BAYES_CTX=$(printf '%s' "$BAYES_OUT" | jq -r '.hookSpecificOutput.additionalContext')
BAYES_POS_GOOD=$(printf '%s\n' "$BAYES_CTX" | grep -n "proven-good" | head -1 | cut -d: -f1)
BAYES_POS_BAD=$(printf '%s\n' "$BAYES_CTX" | grep -n "proven-bad" | head -1 | cut -d: -f1)
assert "Bayesian — proven-good appears" '[ -n "$BAYES_POS_GOOD" ]'
assert "Bayesian — proven-good before proven-bad" '[ -n "$BAYES_POS_GOOD" ] && [ -n "$BAYES_POS_BAD" ] && [ "$BAYES_POS_GOOD" -lt "$BAYES_POS_BAD" ]'
rm -rf "$BAYES_DIR"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: FAIL (both files have identical spread/decay, so ordering is arbitrary without effectiveness signal)

- [ ] **Step 3: Update WAL scoring awk in session-start.sh**

In the WAL scoring awk block (~line 394), add tracking of `outcome-positive` events alongside the existing `outcome-negative` tracking. Replace the awk script assigned to `WAL_SCORES`:

```awk
  # In the main body, add after the outcome-negative tracking:
  $1 >= cutoff && $2 == "outcome-positive" {
    pos_count[$3]++
  }

  # In the END block, replace the negative ratio calculation:
  # OLD: nr = 0
  #      if (name in neg_count) nr = neg_count[name] / count[name]
  #      if (nr > 1) nr = 1
  #      raw = spread * decay * (1.0 - nr)
  # NEW:
        pc = 0; nc = 0
        if (name in pos_count) pc = pos_count[name]
        if (name in neg_count) nc = neg_count[name]
        effectiveness = (pc + 1) / (pc + nc + 2)

        raw = spread * decay * effectiveness
```

The full replacement for the WAL_SCORES awk block in session-start.sh — find the awk script starting at `WAL_SCORES=$(awk -F'|'` and replace the content:

```bash
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
    $1 >= cutoff && $2 == "outcome-positive" {
      pos_count[$3]++
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

        # spread = unique days
        spread = 0
        for (key in days) {
          split(key, kp, SUBSEP)
          if (kp[1] == name) spread++
        }

        # decay
        ds = days_between(last_day[name], today)
        if (ds < 0) ds = 0
        decay = 1.0 / (1.0 + ds / 7.0)

        # Bayesian effectiveness (replaces negative_ratio)
        pc = 0; nc = 0
        if (name in pos_count) pc = pos_count[name]
        if (name in neg_count) nc = neg_count[name]
        effectiveness = (pc + 1) / (pc + nc + 2)

        raw = spread * decay * effectiveness
        if (raw > 10) raw = 10

        if (!first) printf ";"
        printf "%s:%.1f", name, raw
        first = 0
      }
    }
  ' "$WAL_FILE" 2>/dev/null || true)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: all PASS

- [ ] **Step 5: Run bench to check no regressions**

Run: `bash hooks/bench-memory.sh 2>&1 | tail -10`

Expected: all existing benchmarks PASS (old WAL without outcome-positive events → effectiveness defaults to 0.5 for all, preserving relative ordering)

- [ ] **Step 6: Commit**

```bash
git add hooks/session-start.sh hooks/test-memory-hooks.sh
git commit -m "feat: Bayesian effectiveness scoring in session-start"
```

---

### Task 5: Strategy bonus in scoring

**Files:**
- Modify: `hooks/session-start.sh` (WAL scoring + collect_scored for strategies)
- Modify: `hooks/test-memory-hooks.sh` (append test)

- [ ] **Step 1: Write failing test**

Append to `hooks/test-memory-hooks.sh`:

```bash
# Test: strategy-used bonus — used strategy ranks higher
SBONUS_DIR=$(mktemp -d)
SBONUS_MEM="$SBONUS_DIR/memory"
mkdir -p "$SBONUS_MEM"/{strategies,feedback,projects}
cat > "$SBONUS_MEM/projects.json" << 'EOF'
{}
EOF
cat > "$SBONUS_MEM/projects-domains.json" << 'EOF'
{}
EOF
cat > "$SBONUS_MEM/strategies/used-strat.md" << 'EOF'
---
type: strategy
project: global
status: active
referenced: 2026-04-01
---
Strategy with confirmed usage.
EOF
cat > "$SBONUS_MEM/strategies/unused-strat.md" << 'EOF'
---
type: strategy
project: global
status: active
referenced: 2026-04-01
---
Strategy never confirmed.
EOF
# WAL: both injected equally, but used-strat has strategy-used events
{
  echo "2026-04-05|inject|used-strat|s1"
  echo "2026-04-06|inject|used-strat|s2"
  echo "2026-04-07|inject|used-strat|s3"
  echo "2026-04-05|strategy-used|used-strat|s1"
  echo "2026-04-06|strategy-used|used-strat|s2"
  echo "2026-04-07|strategy-used|used-strat|s3"
  echo "2026-04-05|inject|unused-strat|s1"
  echo "2026-04-06|inject|unused-strat|s2"
  echo "2026-04-07|inject|unused-strat|s3"
} > "$SBONUS_MEM/.wal"
SBONUS_OUT=$(printf '{"session_id":"sbonus-test","cwd":"/tmp"}' | CLAUDE_MEMORY_DIR="$SBONUS_MEM" bash "$HOOK" 2>/dev/null)
SBONUS_CTX=$(printf '%s' "$SBONUS_OUT" | jq -r '.hookSpecificOutput.additionalContext')
SBONUS_POS_USED=$(printf '%s\n' "$SBONUS_CTX" | grep -n "used-strat" | head -1 | cut -d: -f1)
SBONUS_POS_UNUSED=$(printf '%s\n' "$SBONUS_CTX" | grep -n "unused-strat" | head -1 | cut -d: -f1)
assert "Strategy bonus — used-strat appears" '[ -n "$SBONUS_POS_USED" ]'
assert "Strategy bonus — used before unused" '[ -n "$SBONUS_POS_USED" ] && [ -n "$SBONUS_POS_UNUSED" ] && [ "$SBONUS_POS_USED" -lt "$SBONUS_POS_UNUSED" ]'
rm -rf "$SBONUS_DIR"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: FAIL (both strategies have same WAL score, alphabetical order wins)

- [ ] **Step 3: Add strategy-used parsing to WAL scoring and strategy bonus to collect_scored**

In session-start.sh WAL scoring awk block, add `strategy-used` tracking:

```awk
    $1 >= cutoff && $2 == "strategy-used" {
      strat_used[$3]++
    }
```

And in the END block, output strategy-used counts alongside scores:

```awk
        # Also output strategy-used count (separate from main score)
        su = 0
        if (name in strat_used) su = strat_used[name]
        if (!first) printf ";"
        printf "%s:%.1f:%d", name, raw, su
        first = 0
```

Then update the WAL_SCORES parsing in collect_scored to extract the third field (strategy-used count). In the `collect_scored` function's awk block, update the WAL parsing in `BEGIN`:

```awk
      # Parse WAL scores: "name:score:strat_used;name:score:strat_used;..."
      m=split(wal, wentries, ";")
      for (i=1; i<=m; i++) {
        split(wentries[i], wp, ":")
        if (wp[1] != "") {
          wal_count[wp[1]] = wp[2]+0
          strat_used_count[wp[1]] = wp[3]+0
        }
      }
```

And add the strategy bonus to the score calculation (after `score += wc`):

```awk
      # Strategy bonus: min(strategy_used_count * 2, 6)
      su = strat_used_count[fname]+0
      if (su > 3) su = 3
      score += su * 2
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: all PASS

- [ ] **Step 5: Run bench**

Run: `bash hooks/bench-memory.sh 2>&1 | tail -10`

Expected: all PASS (no strategy-used events in bench fixtures → bonus=0, no change)

- [ ] **Step 6: Commit**

```bash
git add hooks/session-start.sh hooks/test-memory-hooks.sh
git commit -m "feat: strategy-used bonus in scoring"
```

---

### Task 6: Strategy-used, strategy-gap, and strategies reminder in Stop hook

**Files:**
- Modify: `hooks/session-stop.sh`
- Modify: `hooks/test-memory-hooks.sh`

- [ ] **Step 1: Write failing tests**

Append to `hooks/test-memory-hooks.sh`:

```bash
# Test: strategy-used — clean session + injected strategy → strategy-used event
SU_DIR=$(mktemp -d)
SU_MEM="$SU_DIR/memory"
mkdir -p "$SU_MEM"/{strategies,projects}
cat > "$SU_MEM/projects.json" << 'EOF'
{}
EOF
cat > "$SU_MEM/strategies/debug-strat.md" << 'EOF'
---
type: strategy
project: global
status: active
domains: [general]
---
Debug strategy steps.
EOF
printf '2026-04-11|inject|debug-strat|su-test-session\n' > "$SU_MEM/.wal"
SU_MARKER="/tmp/.claude-session-su-test-session"
touch -t 202601010000 "$SU_MARKER"
# Clean transcript (no errors)
cat > "$SU_DIR/transcript.jsonl" << 'SUEOF'
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"x.py"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"OK"}]}}
SUEOF
echo '{"session_id":"su-test-session","cwd":"/tmp","transcript_path":"'"$SU_DIR"'/transcript.jsonl"}' | CLAUDE_MEMORY_DIR="$SU_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null
assert "Strategy-used — written to WAL" 'grep -q "strategy-used|debug-strat" "$SU_MEM/.wal"'
rm -rf "$SU_DIR" "$SU_MARKER"

# Test: strategy-gap — clean session, no strategies injected
SG_DIR=$(mktemp -d)
SG_MEM="$SG_DIR/memory"
mkdir -p "$SG_MEM/projects"
cat > "$SG_MEM/projects.json" << 'EOF'
{}
EOF
# WAL: some injections but no strategies
printf '2026-04-11|inject|code-approach|sg-test-session\n' > "$SG_MEM/.wal"
SG_MARKER="/tmp/.claude-session-sg-test-session"
touch -t 202601010000 "$SG_MARKER"
cat > "$SG_DIR/transcript.jsonl" << 'SGEOF'
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"x.py"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"OK"}]}}
SGEOF
echo '{"session_id":"sg-test-session","cwd":"/tmp","transcript_path":"'"$SG_DIR"'/transcript.jsonl"}' | CLAUDE_MEMORY_DIR="$SG_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null
assert "Strategy-gap — written to WAL" 'grep -q "strategy-gap" "$SG_MEM/.wal"'
rm -rf "$SG_DIR" "$SG_MARKER"

# Test: strategies reminder on long clean session
SR2_DIR=$(mktemp -d)
SR2_MEM="$SR2_DIR/memory"
mkdir -p "$SR2_MEM/projects"
cat > "$SR2_MEM/projects.json" << 'EOF'
{}
EOF
SR2_MARKER="/tmp/.claude-session-sr2-test-session"
# Set marker to 15 minutes ago (>600s)
if [[ "$OSTYPE" == darwin* ]]; then
  touch -t $(date -v-15M +%Y%m%d%H%M) "$SR2_MARKER"
else
  touch -d "15 minutes ago" "$SR2_MARKER"
fi
cat > "$SR2_DIR/transcript.jsonl" << 'SR2EOF'
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"x.py"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"OK"}]}}
SR2EOF
SR2_OUT=$(echo '{"session_id":"sr2-test-session","cwd":"/tmp","transcript_path":"'"$SR2_DIR"'/transcript.jsonl"}' | CLAUDE_MEMORY_DIR="$SR2_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null)
SR2_CTX=$(printf '%s' "$SR2_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""' 2>/dev/null)
assert "Strategies reminder — shown on long clean session" 'printf "%s" "$SR2_CTX" | grep -qi "strateg"'
rm -rf "$SR2_DIR" "$SR2_MARKER"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: 4 FAIL

- [ ] **Step 3: Implement strategy-used, strategy-gap, and reminder in session-stop.sh**

Add after the clean-session block:

```bash
# --- Strategy tracking ---
if [ -f "$WAL_FILE" ] && [ -n "$SAFE_SESSION_ID" ]; then
  # Check if any strategies were injected in this session
  INJECTED_STRATEGIES=$(awk -F'|' -v sid="$SAFE_SESSION_ID" '
    $4 == sid && $2 == "inject" { print $3 }
  ' "$WAL_FILE" 2>/dev/null)

  HAS_STRATEGIES=0
  if [ -n "$INJECTED_STRATEGIES" ]; then
    while IFS= read -r strat_name; do
      [ -z "$strat_name" ] && continue
      [ -f "$MEMORY_DIR/strategies/${strat_name}.md" ] || continue
      HAS_STRATEGIES=1

      # Strategy-used: clean session + strategy injected
      if [ "${METRIC_ERROR_COUNT:-0}" -eq 0 ]; then
        printf '%s|strategy-used|%s|%s\n' "$TODAY" "$strat_name" "$SAFE_SESSION_ID" \
          >> "$WAL_FILE" 2>/dev/null
      fi
    done <<< "$INJECTED_STRATEGIES"
  fi

  # Strategy-gap: clean session without any injected strategies
  if [ "${METRIC_ERROR_COUNT:-0}" -eq 0 ] && [ "$HAS_STRATEGIES" -eq 0 ]; then
    METRIC_DOMAINS_GAP="${SESSION_DOMAINS:-unknown}"
    [ -z "$METRIC_DOMAINS_GAP" ] && METRIC_DOMAINS_GAP="unknown"
    METRIC_DOMAINS_GAP=$(printf '%s' "$METRIC_DOMAINS_GAP" | tr ' ' ',')
    [ -z "$METRIC_DOMAINS_GAP" ] && METRIC_DOMAINS_GAP="unknown"
    printf '%s|strategy-gap|%s|%s\n' "$TODAY" "$METRIC_DOMAINS_GAP" "$SAFE_SESSION_ID" \
      >> "$WAL_FILE" 2>/dev/null
  fi
fi

# Strategies reminder: long clean session → suggest recording approach
if [ "${METRIC_ERROR_COUNT:-0}" -eq 0 ] && [ "${ELAPSED:-0}" -gt 600 ]; then
  REMINDER="${REMINDER}${REMINDER:+
}[STRATEGIES] Сессия чистая (0 ошибок, ${ELAPSED}с). Если задача решена с первого подхода — запиши в strategies/:
trigger, steps (3-5), outcome."
fi
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add hooks/session-stop.sh hooks/test-memory-hooks.sh
git commit -m "feat: strategy-used, strategy-gap events and strategies reminder"
```

---

### Task 7: Update memory-precompact.sh with strategies prompt

**Files:**
- Modify: `hooks/memory-precompact.sh`

- [ ] **Step 1: Update the reminder message**

In `hooks/memory-precompact.sh`, find the MSG assignment and add strategies prompt:

```bash
MSG="⚠ Context compression. Если есть незаписанные инсайты, ошибки или решения — запиши в memory/ сейчас."
if [ "${SAVED:-0}" -eq 0 ]; then
  MSG="${MSG}
В этой сессии ничего не записано в memory/. Проверь: были ли ошибки, решения, паттерны или стратегии, которые стоит сохранить?
Если задача решена с первого подхода — запиши подход в strategies/ (trigger, steps, outcome)."
fi
```

- [ ] **Step 2: Verify syntax**

Run: `bash -n hooks/memory-precompact.sh && echo "OK"`

Expected: OK

- [ ] **Step 3: Commit**

```bash
git add hooks/memory-precompact.sh
git commit -m "feat: strategies prompt in precompact reminder"
```

---

## Phase 3: Analytics

### Task 8: Create memory-analytics.sh

**Files:**
- Create: `hooks/memory-analytics.sh`
- Modify: `hooks/test-memory-hooks.sh`

- [ ] **Step 1: Write failing test**

Append to `hooks/test-memory-hooks.sh`:

```bash
# Test: memory-analytics.sh generates report
AN_DIR=$(mktemp -d)
AN_MEM="$AN_DIR/memory"
mkdir -p "$AN_MEM"/{strategies,mistakes}
cat > "$AN_MEM/strategies/css-debug.md" << 'EOF'
---
type: strategy
project: global
status: active
domains: [css]
---
CSS debug strategy.
EOF
# WAL with varied data over 30 days
{
  for day in 1 3 5 7 9 11 13 15 17 19 21; do
    if [[ "$OSTYPE" == darwin* ]]; then
      d=$(date -v-${day}d +%Y-%m-%d)
    else
      d=$(date -d "${day} days ago" +%Y-%m-%d)
    fi
    echo "${d}|inject|winner-file|s${day}"
    echo "${d}|outcome-positive|winner-file|s${day}"
    echo "${d}|inject|noise-file|s${day}"
    echo "${d}|session-metrics|backend|error_count:0,tool_calls:5,duration:300s"
    echo "${d}|clean-session|backend|s${day}"
  done
  for day in 2 4 6 8 10 12; do
    if [[ "$OSTYPE" == darwin* ]]; then
      d=$(date -v-${day}d +%Y-%m-%d)
    else
      d=$(date -d "${day} days ago" +%Y-%m-%d)
    fi
    echo "${d}|inject|noise-file|sn${day}"
    echo "${d}|outcome-negative|noise-file|sn${day}"
    echo "${d}|session-metrics|frontend|error_count:2,tool_calls:8,duration:600s"
  done
} > "$AN_MEM/.wal"
CLAUDE_MEMORY_DIR="$AN_MEM" bash "$(dirname "$HOOK")/memory-analytics.sh" 2>/dev/null
assert "Analytics — report created" '[ -f "$AN_MEM/.analytics-report" ]'
assert "Analytics — has winners" 'grep -q "winner-file" "$AN_MEM/.analytics-report"'
assert "Analytics — has noise" 'grep -q "noise-file" "$AN_MEM/.analytics-report"'
assert "Analytics — has clean_ratio" 'grep -q "clean_ratio" "$AN_MEM/.analytics-report"'
assert "Analytics — has strategy gaps" 'grep -qi "gap\|backend" "$AN_MEM/.analytics-report"'
rm -rf "$AN_DIR"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: FAIL (memory-analytics.sh doesn't exist yet)

- [ ] **Step 3: Create hooks/memory-analytics.sh**

```bash
#!/bin/bash
# Batch analytics for hypomnema memory system
# Analyzes WAL over 30 days, generates .analytics-report
# Run: manually, or auto-triggered by session-stop when report is stale

set -o pipefail

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
WAL_FILE="$MEMORY_DIR/.wal"
REPORT_FILE="$MEMORY_DIR/.analytics-report"
TODAY=$(date +%Y-%m-%d)

[ -f "$WAL_FILE" ] || exit 0

if [[ "$OSTYPE" == darwin* ]]; then
  CUTOFF=$(date -v-30d +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
else
  CUTOFF=$(date -d "30 days ago" +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
fi

# Single-pass awk: compute all analytics from WAL
ANALYTICS=$(awk -F'|' -v cutoff="$CUTOFF" '
  $1 < cutoff { next }

  $2 == "inject" || $2 == "inject-agg" {
    if ($2 == "inject-agg") injects[$3] += $4
    else injects[$3]++
  }
  $2 == "outcome-positive" { pos[$3]++ }
  $2 == "outcome-negative" { neg[$3]++ }
  $2 == "clean-session" {
    total_clean++
    # Parse domains
    n = split($3, doms, ",")
    for (i = 1; i <= n; i++) clean_domains[doms[i]]++
  }
  $2 == "session-metrics" {
    total_sessions++
    # Parse error_count
    ec = $4; sub(/.*error_count:/, "", ec); sub(/,.*/, "", ec)
    total_errors += ec + 0
    if (ec + 0 > 0) {
      n = split($3, doms, ",")
      for (i = 1; i <= n; i++) error_domains[doms[i]]++
    }
  }
  $2 == "strategy-used" { strat_used[$3]++ }
  $2 == "strategy-gap" {
    n = split($3, doms, ",")
    for (i = 1; i <= n; i++) gap_domains[doms[i]]++
  }

  END {
    # Header
    printf "date: %s\n", cutoff
    if (total_sessions > 0) {
      printf "sessions: %d\n", total_sessions
      cr = (total_clean + 0) / total_sessions
      printf "clean_ratio: %.2f\n", cr
      avg_err = total_errors / total_sessions
      printf "avg_errors: %.1f\n", avg_err
    } else {
      printf "sessions: 0\nclean_ratio: 0\navg_errors: 0\n"
    }

    # Per-file effectiveness
    winner_count = 0; noise_count = 0; unproven_count = 0
    for (name in injects) {
      ic = injects[name]
      pc = 0; nc = 0
      if (name in pos) pc = pos[name]
      if (name in neg) nc = neg[name]
      eff = (pc + 1) / (pc + nc + 2)

      if (eff > 0.7 && ic >= 3) {
        winners[++winner_count] = sprintf("- %s (eff: %.2f, injects: %d, +%d/-%d)", name, eff, ic, pc, nc)
      }
      if (eff < 0.3 && ic >= 5) {
        noise[++noise_count] = sprintf("- %s (eff: %.2f, injects: %d, +%d/-%d)", name, eff, ic, pc, nc)
      }
      if (ic >= 5 && pc == 0 && nc == 0) {
        unproven[++unproven_count] = sprintf("- %s (injects: %d, 0 outcomes)", name, ic)
      }
    }

    if (winner_count > 0) {
      printf "\n## Winners\n"
      for (i = 1; i <= winner_count; i++) print winners[i]
    }
    if (noise_count > 0) {
      printf "\n## Noise candidates\n"
      for (i = 1; i <= noise_count; i++) print noise[i]
    }

    # Strategy gaps: domains with clean sessions but no strategies
    gap_count = 0
    for (dom in clean_domains) {
      if (clean_domains[dom] >= 3) {
        gaps[++gap_count] = sprintf("- %s: %d clean sessions", dom, clean_domains[dom])
      }
    }
    if (gap_count > 0) {
      printf "\n## Strategy gaps\n"
      for (i = 1; i <= gap_count; i++) print gaps[i]
    }

    if (unproven_count > 0) {
      printf "\n## Unproven\n"
      for (i = 1; i <= unproven_count; i++) print unproven[i]
    }
  }
' "$WAL_FILE" 2>/dev/null)

[ -z "$ANALYTICS" ] && exit 0

# Cross-reference strategy gaps with existing strategies
STRATEGY_DIR="$MEMORY_DIR/strategies"
if [ -d "$STRATEGY_DIR" ]; then
  EXISTING_DOMAINS=$(awk '
    /^---$/ { n++ }
    n==1 && /^domains:/ { sub(/^domains: *\[?/, ""); sub(/\].*/, ""); gsub(/, */, "\n"); print }
  ' "$STRATEGY_DIR"/*.md 2>/dev/null | sort -u | tr '\n' ',' | sed 's/,$//')
  # Filter: only show gaps for domains without strategies
  if [ -n "$EXISTING_DOMAINS" ]; then
    ANALYTICS=$(printf '%s\n' "$ANALYTICS" | awk -v covered="$EXISTING_DOMAINS" '
      BEGIN { n = split(covered, c, ","); for (i=1;i<=n;i++) has[c[i]]=1 }
      /^## Strategy gaps/ { in_gaps=1; print; next }
      in_gaps && /^- / {
        dom = $2; sub(/:$/, "", dom)
        if (!(dom in has)) print
        next
      }
      in_gaps && /^## / { in_gaps=0 }
      { print }
    ')
  fi
fi

printf '%s\n' "$ANALYTICS" > "$REPORT_FILE" 2>/dev/null
exit 0
```

- [ ] **Step 4: Make executable**

Run: `chmod +x hooks/memory-analytics.sh`

- [ ] **Step 5: Run test to verify it passes**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add hooks/memory-analytics.sh hooks/test-memory-hooks.sh
git commit -m "feat: batch analytics script"
```

---

### Task 9: Noise penalty in session-start.sh

**Files:**
- Modify: `hooks/session-start.sh` (collect_scored function)
- Modify: `hooks/test-memory-hooks.sh`

- [ ] **Step 1: Write failing test**

Append to `hooks/test-memory-hooks.sh`:

```bash
# Test: noise penalty — records in .analytics-report noise list get demoted
NP_DIR=$(mktemp -d)
NP_MEM="$NP_DIR/memory"
mkdir -p "$NP_MEM"/{feedback,projects}
cat > "$NP_MEM/projects.json" << 'EOF'
{}
EOF
cat > "$NP_MEM/projects-domains.json" << 'EOF'
{}
EOF
cat > "$NP_MEM/feedback/noisy-fb.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2026-04-01
---
Noisy feedback that never helps.
EOF
cat > "$NP_MEM/feedback/good-fb.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2026-04-01
---
Good feedback that always helps.
EOF
# Both have identical WAL history
{
  echo "2026-04-05|inject|noisy-fb|s1"
  echo "2026-04-06|inject|noisy-fb|s2"
  echo "2026-04-07|inject|noisy-fb|s3"
  echo "2026-04-05|inject|good-fb|s1"
  echo "2026-04-06|inject|good-fb|s2"
  echo "2026-04-07|inject|good-fb|s3"
} > "$NP_MEM/.wal"
# Analytics report marks noisy-fb as noise
cat > "$NP_MEM/.analytics-report" << 'EOF'
date: 2026-04-10
sessions: 20
clean_ratio: 0.70

## Noise candidates
- noisy-fb (eff: 0.20, injects: 8, +0/-3)
EOF
NP_OUT=$(printf '{"session_id":"np-test","cwd":"/tmp"}' | CLAUDE_MEMORY_DIR="$NP_MEM" bash "$HOOK" 2>/dev/null)
NP_CTX=$(printf '%s' "$NP_OUT" | jq -r '.hookSpecificOutput.additionalContext')
NP_POS_GOOD=$(printf '%s\n' "$NP_CTX" | grep -n "good-fb" | head -1 | cut -d: -f1)
NP_POS_NOISY=$(printf '%s\n' "$NP_CTX" | grep -n "noisy-fb" | head -1 | cut -d: -f1)
assert "Noise penalty — good-fb appears" '[ -n "$NP_POS_GOOD" ]'
assert "Noise penalty — good before noisy" '[ -n "$NP_POS_GOOD" ] && [ -n "$NP_POS_NOISY" ] && [ "$NP_POS_GOOD" -lt "$NP_POS_NOISY" ]'
rm -rf "$NP_DIR"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: FAIL (both feedbacks have identical scores without penalty)

- [ ] **Step 3: Add noise penalty loading and application in session-start.sh**

After the TF-IDF index section (after `fi` closing auto-rebuild block, before `collect_scored` function), add noise candidates loading:

```bash
# --- Load noise candidates from analytics report ---
NOISE_CANDIDATES=""
ANALYTICS_REPORT="$MEMORY_DIR/.analytics-report"
if [ -f "$ANALYTICS_REPORT" ]; then
  # Check freshness: skip if older than 7 days
  if [[ "$OSTYPE" == darwin* ]]; then
    ar_mtime=$(stat -f %m "$ANALYTICS_REPORT" 2>/dev/null || echo 0)
  else
    ar_mtime=$(stat -c %Y "$ANALYTICS_REPORT" 2>/dev/null || echo 0)
  fi
  ar_age=$(( ($(date +%s) - ar_mtime) / 86400 ))
  if [ "$ar_age" -le 7 ]; then
    # Extract filenames from "## Noise candidates" section
    NOISE_CANDIDATES=$(awk '
      /^## Noise candidates/ { in_noise=1; next }
      in_noise && /^## / { exit }
      in_noise && /^- / { sub(/^- /, ""); sub(/ .*/, ""); print }
    ' "$ANALYTICS_REPORT" 2>/dev/null | tr '\n' ' ')
  fi
fi
```

Then in the `collect_scored` function's awk scoring block, add noise penalty. Pass `NOISE_CANDIDATES` as a variable and apply `-3` penalty:

In the awk call inside collect_scored, add `-v noise="$NOISE_CANDIDATES"` and in BEGIN:

```awk
      # Parse noise candidates
      nn=split(noise, ncands, " ")
      for (i=1; i<=nn; i++) if (ncands[i] != "") is_noise[ncands[i]]=1
```

And after the score calculation (after `score += su * 2`):

```awk
      # Noise penalty from analytics report
      if (fname in is_noise) score -= 3
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: all PASS

- [ ] **Step 5: Run bench**

Run: `bash hooks/bench-memory.sh 2>&1 | tail -10`

Expected: all PASS (no .analytics-report in bench fixtures → no penalty applied)

- [ ] **Step 6: Commit**

```bash
git add hooks/session-start.sh hooks/test-memory-hooks.sh
git commit -m "feat: noise penalty from analytics report"
```

---

### Task 10: Analytics trigger in Stop hook + install

**Files:**
- Modify: `hooks/session-stop.sh`
- Modify: `hooks/test-memory-hooks.sh`

- [ ] **Step 1: Write failing test**

Append to `hooks/test-memory-hooks.sh`:

```bash
# Test: analytics trigger — stale report triggers rebuild
AT_DIR=$(mktemp -d)
AT_MEM="$AT_DIR/memory"
mkdir -p "$AT_MEM"
# Create stale report (10 days old)
echo "date: 2026-03-01" > "$AT_MEM/.analytics-report"
if [[ "$OSTYPE" == darwin* ]]; then
  touch -t $(date -v-10d +%Y%m%d0000) "$AT_MEM/.analytics-report"
else
  touch -d "10 days ago" "$AT_MEM/.analytics-report"
fi
# WAL with some data
{
  echo "2026-04-10|inject|test-file|at-session"
  echo "2026-04-10|session-metrics|backend|error_count:0,tool_calls:3,duration:300s"
  echo "2026-04-10|clean-session|backend|at-session"
} > "$AT_MEM/.wal"
AT_MARKER="/tmp/.claude-session-at-test-session"
touch -t 202601010000 "$AT_MARKER"
cat > "$AT_DIR/transcript.jsonl" << 'ATEOF'
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"echo ok"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}
ATEOF
echo '{"session_id":"at-test-session","cwd":"/tmp","transcript_path":"'"$AT_DIR"'/transcript.jsonl"}' | CLAUDE_MEMORY_DIR="$AT_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null
sleep 1  # async analytics needs a moment
assert "Analytics trigger — report refreshed" '[ -f "$AT_MEM/.analytics-report" ]'
# Check report is newer than 1 second ago (was regenerated)
if [[ "$OSTYPE" == darwin* ]]; then
  ar_mtime=$(stat -f %m "$AT_MEM/.analytics-report" 2>/dev/null || echo 0)
else
  ar_mtime=$(stat -c %Y "$AT_MEM/.analytics-report" 2>/dev/null || echo 0)
fi
now=$(date +%s)
ar_age=$(( now - ar_mtime ))
assert "Analytics trigger — report is fresh" '[ "$ar_age" -lt 5 ]'
rm -rf "$AT_DIR" "$AT_MARKER"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: FAIL

- [ ] **Step 3: Add analytics trigger to session-stop.sh**

Add after the TF-IDF rebuild block (after `disown` for memory-index.sh), before the auto-continuity section:

```bash
# Rebuild analytics report if missing or stale (>7 days)
ANALYTICS_REPORT="$MEMORY_DIR/.analytics-report"
ANALYTICS_STALE=0
if [ ! -f "$ANALYTICS_REPORT" ]; then
  ANALYTICS_STALE=1
elif [ -f "$WAL_FILE" ]; then
  if [[ "$OSTYPE" == darwin* ]]; then
    ar_mtime=$(stat -f %m "$ANALYTICS_REPORT" 2>/dev/null || echo 0)
  else
    ar_mtime=$(stat -c %Y "$ANALYTICS_REPORT" 2>/dev/null || echo 0)
  fi
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bash hooks/test-memory-hooks.sh 2>&1 | tail -5`

Expected: all PASS

- [ ] **Step 5: Create symlink for analytics hook**

Run: `ln -sf /Users/akamash/Development/hypomnema/hooks/memory-analytics.sh /Users/akamash/.claude/hooks/memory-analytics.sh`

- [ ] **Step 6: Commit**

```bash
git add hooks/session-stop.sh hooks/test-memory-hooks.sh
git commit -m "feat: analytics trigger in Stop hook"
```

---

### Task 11: Final integration test + bench

**Files:**
- Modify: `hooks/test-memory-hooks.sh` (full pipeline test)

- [ ] **Step 1: Write integration test**

Append to `hooks/test-memory-hooks.sh`:

```bash
# Test: full pipeline — session-start injects, session-stop writes outcomes, analytics reports
FULL_DIR=$(mktemp -d)
FULL_MEM="$FULL_DIR/memory"
mkdir -p "$FULL_MEM"/{mistakes,feedback,strategies,projects}
cat > "$FULL_MEM/projects.json" << 'EOF'
{"/tmp/full-test": "full-proj"}
EOF
cat > "$FULL_MEM/projects-domains.json" << 'EOF'
{"full-proj": ["backend"]}
EOF
cat > "$FULL_MEM/mistakes/test-mistake.md" << 'EOF'
---
type: mistake
project: global
status: active
severity: major
recurrence: 3
domains: [backend]
root-cause: "Test root cause"
prevention: "Test prevention"
---
EOF
cat > "$FULL_MEM/strategies/test-strat.md" << 'EOF'
---
type: strategy
project: global
status: active
domains: [backend]
---
Test strategy steps.
EOF
# Step 1: session-start injects records
echo '{"session_id":"full-test","cwd":"/tmp/full-test"}' | CLAUDE_MEMORY_DIR="$FULL_MEM" bash "$HOOK" 2>/dev/null
assert "Full pipeline — WAL has inject entries" 'grep -q "inject|test-mistake" "$FULL_MEM/.wal"'
assert "Full pipeline — WAL has strategy inject" 'grep -q "inject|test-strat" "$FULL_MEM/.wal"'
# Step 2: session-stop with clean session
FULL_MARKER="/tmp/.claude-session-full-test"
touch -t 202601010000 "$FULL_MARKER"
cat > "$FULL_DIR/transcript.jsonl" << 'FULLEOF'
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"x.py"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"OK"}]}}
FULLEOF
echo '{"session_id":"full-test","cwd":"/tmp/full-test","transcript_path":"'"$FULL_DIR"'/transcript.jsonl"}' | CLAUDE_MEMORY_DIR="$FULL_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null
assert "Full pipeline — outcome-positive written" 'grep -q "outcome-positive|test-mistake" "$FULL_MEM/.wal"'
assert "Full pipeline — strategy-used written" 'grep -q "strategy-used|test-strat" "$FULL_MEM/.wal"'
assert "Full pipeline — clean-session written" 'grep -q "clean-session" "$FULL_MEM/.wal"'
assert "Full pipeline — session-metrics written" 'grep -q "session-metrics" "$FULL_MEM/.wal"'
rm -rf "$FULL_DIR" "$FULL_MARKER"
```

- [ ] **Step 2: Run full test suite**

Run: `bash hooks/test-memory-hooks.sh 2>&1`

Expected: all PASS, 0 FAIL

- [ ] **Step 3: Run bench suite**

Run: `bash hooks/bench-memory.sh 2>&1`

Expected: all PASS (no regressions)

- [ ] **Step 4: Commit**

```bash
git add hooks/test-memory-hooks.sh
git commit -m "test: full pipeline integration test"
```

---

### Task 12: Update strategies template

**Files:**
- Modify: `templates/strategy.md` (if exists, else create)

- [ ] **Step 1: Check if template exists**

Run: `ls templates/`

- [ ] **Step 2: Create or update strategy template**

Create `templates/strategy.md`:

```markdown
---
type: strategy
project: global
created: YYYY-MM-DD
status: active
domains: []
keywords: []
trigger: "описание ситуации когда применять"
success_count: 1
---

# Название стратегии

## Steps
1. 
2. 
3. 

## Outcome
Что даёт этот подход.
```

- [ ] **Step 3: Commit**

```bash
git add templates/strategy.md
git commit -m "docs: structured strategy template"
```
