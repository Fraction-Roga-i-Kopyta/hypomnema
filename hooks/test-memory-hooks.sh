#!/bin/bash
# Smoke tests for hypomnema memory hooks
# Usage: bash test-memory-hooks.sh

set -e
PASS=0
FAIL=0
TESTS=()

assert() {
  local name="$1" condition="$2"
  if eval "$condition"; then
    PASS=$((PASS + 1))
    TESTS+=("✓ $name")
  else
    FAIL=$((FAIL + 1))
    TESTS+=("✗ $name")
  fi
}

# --- Setup test fixtures ---
TEST_DIR=$(mktemp -d)
FIXTURE="$TEST_DIR/memory"
mkdir -p "$FIXTURE"/{mistakes,feedback,knowledge,strategies,projects,continuity}

# Create projects.json
cat > "$FIXTURE/projects.json" << 'PJSON'
{"/tmp/test-project": "test-proj", "/tmp/other": "other-proj"}
PJSON

# Mistakes: 2 global, 2 project
cat > "$FIXTURE/mistakes/global-bug.md" << 'EOF'
---
type: mistake
project: global
status: active
severity: major
recurrence: 5
injected: 2026-04-01
referenced: 2026-04-09
root-cause: "Global bug root cause"
prevention: "Global bug prevention"
---
EOF

cat > "$FIXTURE/mistakes/project-bug.md" << 'EOF'
---
type: mistake
project: test-proj
status: active
severity: minor
recurrence: 2
injected: 2026-04-01
referenced: 2026-04-08
root-cause: "Project-specific bug"
prevention: "Check the thing"
---
EOF

cat > "$FIXTURE/mistakes/pinned-bug.md" << 'EOF'
---
type: mistake
project: global
status: pinned
severity: critical
recurrence: 10
injected: 2026-01-01
referenced: 2026-01-01
root-cause: "Old but pinned"
prevention: "Always do this"
---
EOF

cat > "$FIXTURE/mistakes/archived-bug.md" << 'EOF'
---
type: mistake
project: global
status: archived
severity: minor
recurrence: 1
injected: 2025-01-01
root-cause: "Should not appear"
prevention: "Nope"
---
EOF

# Mistake with multiline body and tabs
cat > "$FIXTURE/mistakes/multiline-body.md" << 'MLEOF'
---
type: mistake
project: global
status: active
severity: minor
recurrence: 1
injected: 2026-04-01
referenced: 2026-04-05
root-cause: "Multiline test"
prevention: "Test prevention"
---

This is a multiline body.
	It has a tab character here.
And another line.
MLEOF

# Feedback: 1 global, 1 project
cat > "$FIXTURE/feedback/global-fb.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2026-04-09
---

Use parameterized queries always.
EOF

cat > "$FIXTURE/feedback/proj-fb.md" << 'EOF'
---
type: feedback
project: test-proj
status: active
referenced: 2026-04-10
---

Project-specific feedback here.
EOF

# Knowledge: 1 global, 1 project
cat > "$FIXTURE/knowledge/global-k.md" << 'EOF'
---
type: knowledge
project: global
status: active
referenced: 2026-04-07
---

Some global knowledge.
EOF

cat > "$FIXTURE/knowledge/proj-k.md" << 'EOF'
---
type: knowledge
project: test-proj
status: active
referenced: 2026-04-10
---

Project knowledge item.
EOF

# Strategy
cat > "$FIXTURE/strategies/strat1.md" << 'EOF'
---
type: strategy
project: global
status: active
referenced: 2026-04-05
---

1. Step one
2. Step two
EOF

# Project overview
cat > "$FIXTURE/projects/test-proj.md" << 'EOF'
---
type: project
project: test-proj
---

Test project overview content.
EOF

# --- Run tests ---

HOOK="$HOME/.claude/hooks/memory-session-start.sh"

# Test 1: Basic injection works
OUTPUT=$(echo '{"session_id":"t1","cwd":"/tmp/test-project"}' | CLAUDE_MEMORY_DIR="$FIXTURE" bash "$HOOK" 2>/dev/null)
CONTEXT=$(printf '%s' "$OUTPUT" | jq -r '.hookSpecificOutput.additionalContext' 2>/dev/null)
assert "Hook produces valid JSON" '[ -n "$OUTPUT" ] && printf "%s" "$OUTPUT" | jq empty 2>/dev/null'
assert "Context is not empty" '[ -n "$CONTEXT" ]'

# Test 2: Project detected
assert "Project detected" 'printf "%s" "$CONTEXT" | grep -q "Project: test-proj"'

# Test 3: Global mistakes injected
assert "Global mistake injected" 'printf "%s" "$CONTEXT" | grep -q "global-bug"'
assert "Pinned mistake injected" 'printf "%s" "$CONTEXT" | grep -q "pinned-bug"'

# Test 4: Archived mistakes NOT injected
assert "Archived mistake excluded" '! printf "%s" "$CONTEXT" | grep -q "archived-bug"'

# Test 5: Project mistakes injected
assert "Project mistake injected" 'printf "%s" "$CONTEXT" | grep -q "project-bug"'

# Test 6: Multiline body with tabs doesn't break parsing
assert "Multiline body parsed" 'printf "%s" "$CONTEXT" | grep -q "multiline-body"'
assert "Other records after multiline OK" 'printf "%s" "$CONTEXT" | grep -q "global-fb"'

# Test 7: Split quotas — project feedback gets priority
assert "Project feedback injected" 'printf "%s" "$CONTEXT" | grep -q "proj-fb"'
assert "Global feedback injected" 'printf "%s" "$CONTEXT" | grep -q "global-fb"'

# Test 8: Project knowledge gets priority
assert "Project knowledge injected" 'printf "%s" "$CONTEXT" | grep -q "proj-k"'
assert "Global knowledge injected" 'printf "%s" "$CONTEXT" | grep -q "global-k"'

# Test 9: Prevention in context
assert "Prevention text present" 'printf "%s" "$CONTEXT" | grep -q "Prevention:"'
assert "Root cause text present" 'printf "%s" "$CONTEXT" | grep -q "Root cause:"'

# Test 10: Agent context has prevention
AGENT_CTX=$(cat "$FIXTURE/_agent_context.md" 2>/dev/null)
assert "Agent context exists" '[ -f "$FIXTURE/_agent_context.md" ]'
assert "Agent context has prevention" 'printf "%s" "$AGENT_CTX" | grep -q "Prevention:"'

# Test 11: WAL file created
assert "WAL file created" '[ -f "$FIXTURE/.wal" ]'
assert "WAL has entries" '[ "$(wc -l < "$FIXTURE/.wal")" -gt 0 ]'

# Test 12: Empty memory dir doesn't crash
EMPTY_DIR=$(mktemp -d)
mkdir -p "$EMPTY_DIR"
EMPTY_EXIT=0
EMPTY_OUT=$(echo '{"session_id":"t2","cwd":"/tmp"}' | CLAUDE_MEMORY_DIR="$EMPTY_DIR" bash "$HOOK" 2>/dev/null) || EMPTY_EXIT=$?
assert "Empty memory dir — no crash" '[ $EMPTY_EXIT -eq 0 ]'
assert "Empty memory dir — valid output" '[ -z "$EMPTY_OUT" ] || printf "%s" "$EMPTY_OUT" | jq empty 2>/dev/null'
rm -rf "$EMPTY_DIR"

# Test 13: No project match
NO_PROJ_OUT=$(echo '{"session_id":"t3","cwd":"/Users/nobody/random"}' | CLAUDE_MEMORY_DIR="$FIXTURE" bash "$HOOK" 2>/dev/null)
NO_PROJ_CTX=$(printf '%s' "$NO_PROJ_OUT" | jq -r '.hookSpecificOutput.additionalContext' 2>/dev/null)
assert "No project — still injects global" 'printf "%s" "$NO_PROJ_CTX" | grep -q "global-bug"'
assert "No project — excludes project files" '! printf "%s" "$NO_PROJ_CTX" | grep -q "project-bug"'

# Test 14: Longest-prefix boundary
BOUNDARY_OUT=$(echo '{"session_id":"t4","cwd":"/tmp/test-project-other"}' | CLAUDE_MEMORY_DIR="$FIXTURE" bash "$HOOK" 2>/dev/null)
BOUNDARY_CTX=$(printf '%s' "$BOUNDARY_OUT" | jq -r '.hookSpecificOutput.additionalContext' 2>/dev/null)
assert "Prefix boundary — /test-project-other != test-proj" '! printf "%s" "$BOUNDARY_CTX" | grep -q "Project: test-proj"'

# Test 15: Stop hook syntax OK
STOP_HOOK="$HOME/.claude/hooks/memory-stop.sh"
assert "Stop hook syntax valid" 'bash -n "$STOP_HOOK" 2>/dev/null'

# Test: TF-IDF indexer
TFIDF_DIR=$(mktemp -d)
TFIDF_MEM="$TFIDF_DIR/memory"
mkdir -p "$TFIDF_MEM"/{knowledge,feedback}
cat > "$TFIDF_MEM/knowledge/no-kw.md" << 'TEOF'
---
type: knowledge
project: global
status: active
---
Docker container networking bridge mode explained.
TEOF
cat > "$TFIDF_MEM/feedback/has-kw.md" << 'TEOF'
---
type: feedback
project: global
status: active
keywords: [docker, network]
---
Docker feedback with keywords.
TEOF
cat > "$TFIDF_MEM/.stopwords" << 'TEOF'
the
is
a
TEOF
CLAUDE_MEMORY_DIR="$TFIDF_MEM" bash "$HOME/.claude/hooks/memory-index.sh" 2>/dev/null
assert "TF-IDF index created" '[ -f "$TFIDF_MEM/.tfidf-index" ]'
assert "TF-IDF index has entries" '[ -s "$TFIDF_MEM/.tfidf-index" ]'
assert "TF-IDF skips files with keywords" '! grep -q "has-kw" "$TFIDF_MEM/.tfidf-index"'
assert "TF-IDF includes files without keywords" 'grep -q "no-kw" "$TFIDF_MEM/.tfidf-index"'
rm -rf "$TFIDF_DIR"

# Test: outcome hook — negative detection
OUTCOME_DIR=$(mktemp -d)
OUTCOME_MEM="$OUTCOME_DIR/memory"
mkdir -p "$OUTCOME_MEM"
printf '2026-04-10|inject|wrong-root-cause-diagnosis|outcome-test-session\n' > "$OUTCOME_MEM/.wal"
echo '{"session_id":"outcome-test-session","tool_name":"Write","tool_input":{"file_path":"'"$OUTCOME_MEM"'/mistakes/wrong-root-cause-diagnosis.md","content":"test"}}' | CLAUDE_MEMORY_DIR="$OUTCOME_MEM" bash "$HOME/.claude/hooks/memory-outcome.sh" 2>/dev/null
assert "Outcome hook — negative detected" 'grep -q "outcome-negative.*wrong-root-cause" "$OUTCOME_MEM/.wal"'

# Test: outcome hook — new mistake (not injected)
echo '{"session_id":"outcome-test-session","tool_name":"Write","tool_input":{"file_path":"'"$OUTCOME_MEM"'/mistakes/brand-new-bug.md","content":"test"}}' | CLAUDE_MEMORY_DIR="$OUTCOME_MEM" bash "$HOME/.claude/hooks/memory-outcome.sh" 2>/dev/null
assert "Outcome hook — new mistake detected" 'grep -q "outcome-new.*brand-new-bug" "$OUTCOME_MEM/.wal"'

# Test: outcome hook — non-mistake file ignored
echo '{"session_id":"outcome-test-session","tool_name":"Write","tool_input":{"file_path":"'"$OUTCOME_MEM"'/feedback/test.md","content":"test"}}' | CLAUDE_MEMORY_DIR="$OUTCOME_MEM" bash "$HOME/.claude/hooks/memory-outcome.sh" 2>/dev/null
OUTCOME_LINES=$(wc -l < "$OUTCOME_MEM/.wal")
assert "Outcome hook — non-mistake ignored" '[ "$OUTCOME_LINES" -eq 3 ]'
rm -rf "$OUTCOME_DIR"

# Test: spaced repetition — spread across days scores higher than burst
SR_DIR=$(mktemp -d)
SR_MEM="$SR_DIR/memory"
mkdir -p "$SR_MEM"/{feedback,projects}
cat > "$SR_MEM/projects.json" << 'EOF'
{}
EOF
cat > "$SR_MEM/projects-domains.json" << 'EOF'
{}
EOF
cat > "$SR_MEM/feedback/spread-file.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2026-04-01
---
Spread feedback content here.
EOF
cat > "$SR_MEM/feedback/burst-file.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2026-04-01
---
Burst feedback content here.
EOF
cat > "$SR_MEM/.wal" << 'EOF'
2026-04-05|inject|spread-file|s1
2026-04-06|inject|spread-file|s2
2026-04-07|inject|spread-file|s3
2026-04-08|inject|spread-file|s4
2026-04-09|inject|spread-file|s5
2026-04-09|inject|burst-file|s5
2026-04-09|inject|burst-file|s5b
2026-04-09|inject|burst-file|s5c
2026-04-09|inject|burst-file|s5d
2026-04-09|inject|burst-file|s5e
EOF
SR_OUT=$(printf '{"session_id":"sr-test","cwd":"/tmp"}' | CLAUDE_MEMORY_DIR="$SR_MEM" bash "$HOOK" 2>/dev/null)
SR_CTX=$(printf '%s' "$SR_OUT" | jq -r '.hookSpecificOutput.additionalContext')
SR_POS_SPREAD=$(printf '%s\n' "$SR_CTX" | grep -n "spread-file" | head -1 | cut -d: -f1)
SR_POS_BURST=$(printf '%s\n' "$SR_CTX" | grep -n "burst-file" | head -1 | cut -d: -f1)
assert "Spaced repetition — spread before burst" '[ -n "$SR_POS_SPREAD" ] && [ -n "$SR_POS_BURST" ] && [ "$SR_POS_SPREAD" -lt "$SR_POS_BURST" ]'
rm -rf "$SR_DIR"

# Test: transcript error detection
ERR_DIR=$(mktemp -d)
ERR_MEM="$ERR_DIR/memory"
mkdir -p "$ERR_MEM"
cat > "$ERR_MEM/.confidence-excludes" << 'EEOF'
grep
test
EEOF
cat > "$ERR_DIR/transcript.jsonl" << 'EEOF'
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"npm run build"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"Exit code 1\nError: Module not found","is_error":true}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"grep foo bar"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"Exit code 1","is_error":true}]}}
EEOF
MARKER_ERR="/tmp/.claude-session-err-test"
touch -t 202601010000 "$MARKER_ERR"
STOP_OUT=$(printf '{"session_id":"err-test","transcript_path":"%s/transcript.jsonl"}' "$ERR_DIR" | CLAUDE_MEMORY_DIR="$ERR_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null)
STOP_CTX=$(printf '%s' "$STOP_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""' 2>/dev/null)
assert "Error detection — npm error found" 'printf "%s" "$STOP_CTX" | grep -q "npm"'
assert "Error detection — grep excluded" '! printf "%s" "$STOP_CTX" | grep -q "grep foo"'
assert "Error detection — WAL entry" 'grep -q "error-detected" "$ERR_MEM/.wal" 2>/dev/null'
rm -rf "$ERR_DIR" "$MARKER_ERR"

# Test: dedup wrapper — syntax and graceful fallback
assert "Dedup wrapper syntax valid" 'bash -n "$HOME/.claude/hooks/memory-dedup.sh" 2>/dev/null'
DEDUP_DIR=$(mktemp -d)
DEDUP_MEM="$DEDUP_DIR/memory"
mkdir -p "$DEDUP_MEM/mistakes"
cat > "$DEDUP_MEM/mistakes/existing.md" << 'DEOF'
---
type: mistake
project: global
status: active
recurrence: 1
root-cause: "CSS variable not applied in shadow DOM"
prevention: "Check computed styles"
---
DEOF
cat > "$DEDUP_MEM/mistakes/new-similar.md" << 'DEOF'
---
type: mistake
project: global
status: active
recurrence: 1
root-cause: "CSS variable not inherited in shadow DOM components"
prevention: "Verify CSS custom properties"
---
DEOF
DEDUP_EXIT=0
echo '{"session_id":"dedup-test","tool_name":"Write","tool_input":{"file_path":"'"$DEDUP_MEM"'/mistakes/new-similar.md"}}' | CLAUDE_MEMORY_DIR="$DEDUP_MEM" bash "$HOME/.claude/hooks/memory-dedup.sh" 2>/dev/null || DEDUP_EXIT=$?
assert "Dedup wrapper — no crash" '[ $DEDUP_EXIT -eq 0 ]'
if python3 -c "import rapidfuzz" 2>/dev/null; then
  assert "Dedup — merged duplicate" '[ ! -f "$DEDUP_MEM/mistakes/new-similar.md" ]'
  assert "Dedup — WAL entry" 'grep -q "dedup-merged" "$DEDUP_MEM/.wal" 2>/dev/null'
  assert "Dedup — recurrence incremented" 'grep -q "recurrence: 2" "$DEDUP_MEM/mistakes/existing.md"'
fi
rm -rf "$DEDUP_DIR"

# --- Results ---
echo ""
echo "=== Test Results ==="
for t in "${TESTS[@]}"; do echo "  $t"; done
echo ""
echo "Passed: $PASS / $((PASS + FAIL))"
[ "$FAIL" -eq 0 ] && echo "ALL TESTS PASSED" || echo "FAILURES: $FAIL"

# Cleanup
rm -rf "$TEST_DIR"

exit $FAIL
