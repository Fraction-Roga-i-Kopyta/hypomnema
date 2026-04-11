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
mkdir -p "$FIXTURE"/{mistakes,feedback,knowledge,strategies,decisions,projects,continuity}

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

# --- CRLF handling ---
CRLF_DIR=$(mktemp -d)
CRLF_MEM="$CRLF_DIR/memory"
mkdir -p "$CRLF_MEM/mistakes"
cat > "$CRLF_MEM/projects.json" <<'CREOF'
{"/test/crlf": "crlf-proj"}
CREOF
# Create CRLF file (Windows-style line endings)
printf -- "---\r\ntype: mistake\r\nstatus: active\r\nproject: global\r\nrecurrence: 3\r\nseverity: major\r\ndomains: [general]\r\n---\r\nCRLF body line\r\n" > "$CRLF_MEM/mistakes/crlf-test.md"
CRLF_OUT=$(echo '{"session_id":"crlf-test","cwd":"/test/crlf"}' | CLAUDE_MEMORY_DIR="$CRLF_MEM" bash "$HOME/.claude/hooks/memory-session-start.sh" 2>/dev/null)
assert "CRLF — file injected" 'echo "$CRLF_OUT" | grep -q "crlf-test"'
assert "CRLF — body preserved" 'echo "$CRLF_OUT" | grep -q "CRLF body line"'
rm -rf "$CRLF_DIR"

# --- WAL pipe in session_id ---
WAL_PIPE_DIR=$(mktemp -d)
WAL_PIPE_MEM="$WAL_PIPE_DIR/memory"
mkdir -p "$WAL_PIPE_MEM/feedback"
cat > "$WAL_PIPE_MEM/feedback/test-fb.md" <<'WPEOF'
---
status: active
project: global
referenced: 2026-04-10
domains: [general]
---
Test feedback body
WPEOF
echo '{"session_id":"sess|with|pipes","cwd":"/tmp"}' | CLAUDE_MEMORY_DIR="$WAL_PIPE_MEM" bash "$HOME/.claude/hooks/memory-session-start.sh" >/dev/null 2>&1
WAL_LAST=$(tail -1 "$WAL_PIPE_MEM/.wal" 2>/dev/null)
assert "WAL — pipe in session_id sanitized" 'echo "$WAL_LAST" | grep -q "sess_with_pipes"'
assert "WAL — exactly 4 fields" '[ "$(echo "$WAL_LAST" | awk -F"|" "{print NF}")" -eq 4 ]'
rm -rf "$WAL_PIPE_DIR"

# Test: injected date preserved (not overwritten)
INJECT_DIR=$(mktemp -d)
INJECT_MEM="$INJECT_DIR/memory"
mkdir -p "$INJECT_MEM"/{feedback,projects}
cat > "$INJECT_MEM/projects.json" << 'EOF'
{}
EOF
cat > "$INJECT_MEM/projects-domains.json" << 'EOF'
{}
EOF
cat > "$INJECT_MEM/feedback/preserve-test.md" << 'EOF'
---
type: feedback
project: global
status: active
injected: 2026-01-15
referenced: 2026-03-01
---
Test body content here.
EOF
echo '{"session_id":"inject-test","cwd":"/tmp"}' | CLAUDE_MEMORY_DIR="$INJECT_MEM" bash "$HOOK" >/dev/null 2>&1
INJECTED_DATE=$(awk '/^---$/{n++} n==1 && /^injected:/{sub(/^injected: */,""); print; exit}' "$INJECT_MEM/feedback/preserve-test.md")
assert "Injected date preserved" '[ "$INJECTED_DATE" = "2026-01-15" ]'
REFERENCED_DATE=$(awk '/^---$/{n++} n==1 && /^referenced:/{sub(/^referenced: */,""); print; exit}' "$INJECT_MEM/feedback/preserve-test.md")
assert "Referenced date updated" '[ "$REFERENCED_DATE" = "'"$(date +%Y-%m-%d)"'" ]'
rm -rf "$INJECT_DIR"

# Test: TF-IDF auto-rebuild when index missing
TFIDF_AUTO_DIR=$(mktemp -d)
TFIDF_AUTO_MEM="$TFIDF_AUTO_DIR/memory"
mkdir -p "$TFIDF_AUTO_MEM"/{knowledge,feedback,projects}
cat > "$TFIDF_AUTO_MEM/projects.json" << 'EOF'
{}
EOF
cat > "$TFIDF_AUTO_MEM/projects-domains.json" << 'EOF'
{}
EOF
cat > "$TFIDF_AUTO_MEM/knowledge/docker-guide.md" << 'EOF'
---
type: knowledge
project: global
status: active
referenced: 2026-04-01
---
Docker container networking bridge mode explained in detail.
EOF
# No .tfidf-index exists — session-start should trigger build
echo '{"session_id":"tfidf-auto","cwd":"/tmp"}' | CLAUDE_MEMORY_DIR="$TFIDF_AUTO_MEM" bash "$HOOK" >/dev/null 2>&1
sleep 1  # async build needs a moment
assert "TF-IDF auto-rebuild on missing index" '[ -f "$TFIDF_AUTO_MEM/.tfidf-index" ]'
rm -rf "$TFIDF_AUTO_DIR"

# Test: git state with special chars doesn't break continuity
PERL_DIR=$(mktemp -d)
PERL_MEM="$PERL_DIR/memory"
mkdir -p "$PERL_MEM"/{continuity,projects}
cat > "$PERL_MEM/projects.json" << 'PEOF'
{"/tmp/perl-test": "perl-proj"}
PEOF
# Init a git repo with special chars in commit message
mkdir -p /tmp/perl-test
git -C /tmp/perl-test init -q 2>/dev/null
git -C /tmp/perl-test commit --allow-empty -m 'fix: handle $var @array (parens)' -q 2>/dev/null
# Create existing continuity with Git State section
cat > "$PERL_MEM/continuity/perl-proj.md" << 'CEOF'
---
type: continuity
project: perl-proj
created: 2026-04-01
status: active
---

## Git State
Old state here
CEOF
PERL_MARKER="/tmp/.claude-session-perl-test"
touch -t 202601010000 "$PERL_MARKER"
PERL_EXIT=0
echo '{"session_id":"perl-test","cwd":"/tmp/perl-test"}' | CLAUDE_MEMORY_DIR="$PERL_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null || PERL_EXIT=$?
assert "Special chars in git — no crash" '[ $PERL_EXIT -eq 0 ]'
assert "Continuity file intact" '[ -f "$PERL_MEM/continuity/perl-proj.md" ]'
rm -rf "$PERL_DIR" /tmp/perl-test "$PERL_MARKER"

# Test: notes injected by keyword match
NOTES_DIR=$(mktemp -d)
NOTES_MEM="$NOTES_DIR/memory"
mkdir -p "$NOTES_MEM"/{notes,projects,feedback}
cat > "$NOTES_MEM/projects.json" << 'EOF'
{}
EOF
cat > "$NOTES_MEM/projects-domains.json" << 'EOF'
{}
EOF
cat > "$NOTES_MEM/notes/docker-tips.md" << 'EOF'
---
type: knowledge
project: global
status: active
referenced: 2026-04-01
keywords: [docker, container, networking]
---
Docker networking tip: use bridge mode for isolation.
EOF
# Simulate session with keyword "docker" (via CWD basename)
mkdir -p /tmp/docker-project
NOTES_OUT=$(echo '{"session_id":"notes-test","cwd":"/tmp/docker-project"}' | CLAUDE_MEMORY_DIR="$NOTES_MEM" bash "$HOOK" 2>/dev/null)
NOTES_CTX=$(printf '%s' "$NOTES_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')
assert "Notes injected by keyword" 'printf "%s" "$NOTES_CTX" | grep -q "docker-tips"'
rm -rf "$NOTES_DIR" /tmp/docker-project

# Test: WAL compaction preserves spread data
WALC_DIR=$(mktemp -d)
WALC_MEM="$WALC_DIR/memory"
mkdir -p "$WALC_MEM"
# Generate >1200 WAL entries with known spread pattern
{
  for day in $(seq 1 60); do
    if [[ "$OSTYPE" == darwin* ]]; then
      d=$(date -v-${day}d +%Y-%m-%d 2>/dev/null)
    else
      d=$(date -d "${day} days ago" +%Y-%m-%d)
    fi
    for i in $(seq 1 25); do
      echo "${d}|inject|spread-victim|sess-${day}-${i}"
    done
  done
} > "$WALC_MEM/.wal"
WAL_BEFORE=$(wc -l < "$WALC_MEM/.wal")
CLAUDE_MEMORY_DIR="$WALC_MEM" bash "$HOME/.claude/hooks/wal-compact.sh" 2>/dev/null
WAL_AFTER=$(wc -l < "$WALC_MEM/.wal")
assert "WAL compaction — reduced size" '[ "$WAL_AFTER" -lt "$WAL_BEFORE" ]'
assert "WAL compaction — has aggregates" 'grep -q "inject-agg" "$WALC_MEM/.wal"'
assert "WAL compaction — preserved recent raw" 'grep -q "|inject|" "$WALC_MEM/.wal"'
rm -rf "$WALC_DIR"

# Test: lifecycle rotation runs even without session marker
LC_DIR=$(mktemp -d)
LC_MEM="$LC_DIR/memory"
mkdir -p "$LC_MEM/feedback"
cat > "$LC_MEM/feedback/old-stale.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2025-01-01
created: 2025-01-01
---
Very old feedback.
EOF
# No session marker exists — stop hook should still do lifecycle rotation
LC_EXIT=0
echo '{"session_id":"no-marker-test"}' | CLAUDE_MEMORY_DIR="$LC_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null || LC_EXIT=$?
LC_STATUS=$(awk '/^---$/{n++} n==1 && /^status:/{sub(/^status: */,""); print; exit}' "$LC_MEM/feedback/old-stale.md" 2>/dev/null)
assert "Lifecycle rotation without marker — file marked stale" '[ "$LC_STATUS" = "stale" ]'
rm -rf "$LC_DIR"

# Test: days_between accuracy (Julian Day based, BSD awk compatible)
DAYS_TEST=$(echo "" | awk '
function jdn(y, m, d) {
  if (m <= 2) { y--; m += 12 }
  return int(365.25*(y+4716)) + int(30.6001*(m+1)) + d - 1524
}
function days_between(d1, d2) {
  split(d1, a, "-"); split(d2, b, "-")
  return jdn(b[1]+0, b[2]+0, b[3]+0) - jdn(a[1]+0, a[2]+0, a[3]+0)
}
END { printf "%d", days_between("2026-02-28", "2026-03-01") }')
assert "days_between Feb28→Mar1 = 1" '[ "$DAYS_TEST" = "1" ]'

# Test: frontmatter-less files archived after 90 days
NOFM_DIR=$(mktemp -d)
NOFM_MEM="$NOFM_DIR/memory"
mkdir -p "$NOFM_MEM/notes"
printf '# No frontmatter here\nJust a note.\n' > "$NOFM_MEM/notes/no-fm.md"
# Set mtime to 100 days ago
touch -t $(date -v-100d +%Y%m%d0000 2>/dev/null || date -d "100 days ago" +%Y%m%d0000) "$NOFM_MEM/notes/no-fm.md"
echo '{"session_id":"nofm-test"}' | CLAUDE_MEMORY_DIR="$NOFM_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null
assert "Frontmatter-less file archived" '[ ! -f "$NOFM_MEM/notes/no-fm.md" ] && [ -f "$NOFM_MEM/archive/notes/no-fm.md" ]'
rm -rf "$NOFM_DIR"

# Test: session_id with slashes — marker created
SLASH_MARKER="/tmp/.claude-session-v10-45569_10022"
rm -f "$SLASH_MARKER" 2>/dev/null
SLASH_DIR=$(mktemp -d)
SLASH_MEM="$SLASH_DIR/memory"
mkdir -p "$SLASH_MEM"
echo '{"session_id":"v10-45569/10022","cwd":"/tmp"}' | CLAUDE_MEMORY_DIR="$SLASH_MEM" bash "$HOOK" >/dev/null 2>&1
assert "Session ID with slashes — marker created" '[ -f "$SLASH_MARKER" ]'
rm -f "$SLASH_MARKER" 2>/dev/null
rm -rf "$SLASH_DIR"

# Test: notes not starved by other scored types
STARVE_DIR=$(mktemp -d)
STARVE_MEM="$STARVE_DIR/memory"
mkdir -p "$STARVE_MEM"/{feedback,knowledge,strategies,notes,projects}
cat > "$STARVE_MEM/projects.json" << 'EOF'
{}
EOF
cat > "$STARVE_MEM/projects-domains.json" << 'EOF'
{}
EOF
# Fill feedback to cap
for i in 1 2 3 4 5 6; do
cat > "$STARVE_MEM/feedback/fb-$i.md" << EOF
---
type: feedback
project: global
status: active
referenced: 2026-04-01
keywords: [docker]
---
Feedback item $i about docker.
EOF
done
# Fill knowledge
for i in 1 2 3 4; do
cat > "$STARVE_MEM/knowledge/kn-$i.md" << EOF
---
type: knowledge
project: global
status: active
referenced: 2026-04-01
keywords: [docker]
---
Knowledge item $i about docker.
EOF
done
# Add a note with matching keyword
cat > "$STARVE_MEM/notes/docker-note.md" << 'EOF'
---
type: knowledge
project: global
status: active
referenced: 2026-04-01
keywords: [docker]
---
Important docker note that should appear.
EOF
mkdir -p /tmp/docker-starve
STARVE_OUT=$(echo '{"session_id":"starve-test","cwd":"/tmp/docker-starve"}' | CLAUDE_MEMORY_DIR="$STARVE_MEM" bash "$HOOK" 2>/dev/null)
STARVE_CTX=$(printf '%s' "$STARVE_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')
assert "Notes not starved by budget" 'printf "%s" "$STARVE_CTX" | grep -q "docker-note"'
rm -rf "$STARVE_DIR" /tmp/docker-starve

# Test: outcome hook — no false positive on substring match
OUT_FP_DIR=$(mktemp -d)
OUT_FP_MEM="$OUT_FP_DIR/memory"
mkdir -p "$OUT_FP_MEM/mistakes"
printf '2026-04-10|inject|test-other-bug|sess-100\n' > "$OUT_FP_MEM/.wal"
echo '{"session_id":"sess-10","tool_name":"Write","tool_input":{"file_path":"'"$OUT_FP_MEM"'/mistakes/test.md","content":"x"}}' | CLAUDE_MEMORY_DIR="$OUT_FP_MEM" bash "$HOME/.claude/hooks/memory-outcome.sh" 2>/dev/null
assert "Outcome — no false positive on substring" 'grep -q "outcome-new.*test" "$OUT_FP_MEM/.wal"'
assert "Outcome — no false negative detection" '! grep -q "outcome-negative" "$OUT_FP_MEM/.wal"'
rm -rf "$OUT_FP_DIR"

# --- Error detection hook tests ---
DETECT_HOOK="$HOME/.claude/hooks/memory-error-detect.sh"
if [ -x "$DETECT_HOOK" ] || [ -L "$DETECT_HOOK" ]; then

# Test: pattern matched in stdout
ED_DIR=$(mktemp -d)
ED_MEM="$ED_DIR/memory"
mkdir -p "$ED_MEM/mistakes"
cat > "$ED_MEM/.error-patterns" << 'EPAT'
DeprecationWarning|deprecation|Deprecated API
Permission denied|permissions|Check permissions
EPAT
ED_OUT=$(echo '{"session_id":"ed-sess-1","tool_name":"Bash","tool_input":{"command":"npm install"},"tool_response":{"stdout":"npm WARN DeprecationWarning: punycode module","stderr":"","interrupted":false}}' | CLAUDE_MEMORY_DIR="$ED_MEM" bash "$DETECT_HOOK" 2>/dev/null)
assert "Error detect — pattern matched" 'echo "$ED_OUT" | grep -q "Deprecated API"'
assert "Error detect — WAL logged" 'grep -q "error-detect|deprecation|ed-sess-1" "$ED_MEM/.wal"'

# Test: no match on clean output
ED_OUT2=$(echo '{"session_id":"ed-sess-2","tool_name":"Bash","tool_input":{"command":"echo ok"},"tool_response":{"stdout":"ok","stderr":"","interrupted":false}}' | CLAUDE_MEMORY_DIR="$ED_MEM" bash "$DETECT_HOOK" 2>/dev/null)
assert "Error detect — no match on clean output" '[ -z "$ED_OUT2" ]'

# Test: session dedup — same category twice in same session
ED_OUT3=$(echo '{"session_id":"ed-sess-1","tool_name":"Bash","tool_input":{"command":"npm update"},"tool_response":{"stdout":"DeprecationWarning again","stderr":"","interrupted":false}}' | CLAUDE_MEMORY_DIR="$ED_MEM" bash "$DETECT_HOOK" 2>/dev/null)
assert "Error detect — session dedup works" '[ -z "$ED_OUT3" ]'

# Test: different category in same session — not deduped
ED_OUT4=$(echo '{"session_id":"ed-sess-1","tool_name":"Bash","tool_input":{"command":"ls /root"},"tool_response":{"stdout":"","stderr":"Permission denied","interrupted":false}}' | CLAUDE_MEMORY_DIR="$ED_MEM" bash "$DETECT_HOOK" 2>/dev/null)
assert "Error detect — different category not deduped" 'echo "$ED_OUT4" | grep -q "Check permissions"'

# Test: stderr detection
ED_OUT5=$(echo '{"session_id":"ed-sess-5","tool_name":"Bash","tool_input":{"command":"some cmd"},"tool_response":{"stdout":"","stderr":"DeprecationWarning: old API","interrupted":false}}' | CLAUDE_MEMORY_DIR="$ED_MEM" bash "$DETECT_HOOK" 2>/dev/null)
assert "Error detect — stderr matched" 'echo "$ED_OUT5" | grep -q "Deprecated API"'

# Test: cross-reference with existing mistake
cat > "$ED_MEM/mistakes/file-permissions.md" << 'MEOF'
---
type: mistake
project: global
status: active
keywords: [permissions]
---
Always check file permissions.
MEOF
ED_OUT6=$(echo '{"session_id":"ed-sess-6","tool_name":"Bash","tool_input":{"command":"chmod"},"tool_response":{"stdout":"Permission denied","stderr":"","interrupted":false}}' | CLAUDE_MEMORY_DIR="$ED_MEM" bash "$DETECT_HOOK" 2>/dev/null)
assert "Error detect — cross-ref existing mistake" 'echo "$ED_OUT6" | grep -q "file-permissions"'

# Test: non-Bash tool ignored
ED_OUT7=$(echo '{"session_id":"ed-sess-7","tool_name":"Write","tool_input":{"file_path":"/tmp/x"},"tool_response":{"stdout":"DeprecationWarning","stderr":""}}' | CLAUDE_MEMORY_DIR="$ED_MEM" bash "$DETECT_HOOK" 2>/dev/null)
assert "Error detect — non-Bash tool ignored" '[ -z "$ED_OUT7" ]'

rm -rf "$ED_DIR"

fi
# --- End error detection tests ---

# --- Decision type tests ---
DEC_DIR=$(mktemp -d)
DEC_MEM="$DEC_DIR/memory"
mkdir -p "$DEC_MEM"/{mistakes,feedback,knowledge,strategies,decisions,projects,continuity,notes}
cat > "$DEC_MEM/projects.json" << 'PJSON'
{"/tmp/dec-proj": "dec-proj"}
PJSON
cat > "$DEC_MEM/decisions/fastapi-over-django.md" << 'DEOF'
---
type: decision
project: dec-proj
status: active
referenced: 2026-04-10
domains: [backend]
keywords: [api, framework, fastapi, django]
---
**Decision:** FastAPI вместо Django для API-сервиса.
**Alternatives:** Django REST, Flask, Starlette
**Reasoning:** async-first, Pydantic из коробки, легче для microservices.
DEOF
cat > "$DEC_MEM/decisions/postgres-over-mysql.md" << 'DEOF'
---
type: decision
project: global
status: active
referenced: 2026-04-09
keywords: [database, postgres, mysql]
---
**Decision:** PostgreSQL как основная СУБД.
**Reasoning:** JSONB, массивы, CTE, лучше для сложных запросов.
DEOF

mkdir -p /tmp/dec-proj
DEC_OUT=$(echo '{"session_id":"dec-test","cwd":"/tmp/dec-proj"}' | CLAUDE_MEMORY_DIR="$DEC_MEM" bash "$HOOK" 2>/dev/null)
DEC_CTX=$(printf '%s' "$DEC_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')
assert "Decision — section present" 'printf "%s" "$DEC_CTX" | grep -q "## Decisions"'
assert "Decision — project-scoped injected" 'printf "%s" "$DEC_CTX" | grep -q "fastapi-over-django"'
assert "Decision — global injected" 'printf "%s" "$DEC_CTX" | grep -q "postgres-over-mysql"'
rm -rf "$DEC_DIR" /tmp/dec-proj

# --- End decision tests ---

# --- PreCompact hook tests ---
PC_HOOK="$HOME/.claude/hooks/memory-precompact.sh"
if [ -x "$PC_HOOK" ] || [ -L "$PC_HOOK" ]; then

# Test: outputs valid JSON with reminder
PC_DIR=$(mktemp -d)
PC_MEM="$PC_DIR/memory"
mkdir -p "$PC_MEM"
PC_SID="pc-test-$$"
PC_MARKER="/tmp/.claude-session-${PC_SID}"
touch "$PC_MARKER"
PC_OUT=$(echo '{"session_id":"'"$PC_SID"'","cwd":"/tmp"}' | CLAUDE_MEMORY_DIR="$PC_MEM" bash "$PC_HOOK" 2>/dev/null)
assert "PreCompact — valid JSON" 'printf "%s" "$PC_OUT" | jq -e ".hookSpecificOutput" >/dev/null 2>&1'
PC_CTX=$(printf '%s' "$PC_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')
assert "PreCompact — contains reminder" 'printf "%s" "$PC_CTX" | grep -q "Context compression"'

# Test: stronger message when nothing saved
assert "PreCompact — nothing-saved warning" 'printf "%s" "$PC_CTX" | grep -q "ничего не записано"'

# Test: no nothing-saved warning when files were created after marker
sleep 1
touch "$PC_MEM/test-insight.md"
PC_OUT2=$(echo '{"session_id":"'"$PC_SID"'","cwd":"/tmp"}' | CLAUDE_MEMORY_DIR="$PC_MEM" bash "$PC_HOOK" 2>/dev/null)
PC_CTX2=$(printf '%s' "$PC_OUT2" | jq -r '.hookSpecificOutput.additionalContext // ""')
assert "PreCompact — no nothing-saved when files exist" '! printf "%s" "$PC_CTX2" | grep -q "ничего не записано"'

rm -f "$PC_MARKER" "$PC_MEM/test-insight.md"
rm -rf "$PC_DIR"

fi
# --- End PreCompact tests ---

# --- Cold start / domain fallback tests ---
CS_DIR=$(mktemp -d)
CS_MEM="$CS_DIR/memory"
mkdir -p "$CS_MEM"/{mistakes,feedback,knowledge,strategies,decisions,projects,continuity,notes}

# Project with domains but we'll test from a non-git directory
cat > "$CS_MEM/projects.json" << 'PJSON'
{"/tmp/cold-start-proj": "cold-proj"}
PJSON
cat > "$CS_MEM/projects-domains.json" << 'DJSON'
{"cold-proj": ["backend", "api"]}
DJSON

# Knowledge tagged with domain keyword
cat > "$CS_MEM/knowledge/api-patterns.md" << 'EOF'
---
type: knowledge
project: global
status: active
referenced: 2026-04-10
keywords: [api, rest, pagination]
---
Always use cursor-based pagination for large datasets.
EOF

# Knowledge without matching domain keyword (should score lower)
cat > "$CS_MEM/knowledge/css-tricks.md" << 'EOF'
---
type: knowledge
project: global
status: active
referenced: 2026-04-10
keywords: [css, flexbox, grid]
---
Use CSS grid for 2D layouts, flexbox for 1D.
EOF

# Test from non-git dir (cold start — no git keywords)
mkdir -p /tmp/cold-start-proj
CS_OUT=$(echo '{"session_id":"cold-test","cwd":"/tmp/cold-start-proj"}' | CLAUDE_MEMORY_DIR="$CS_MEM" bash "$HOOK" 2>/dev/null)
CS_CTX=$(printf '%s' "$CS_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')

assert "Cold start — domain keyword matches api-patterns" 'printf "%s" "$CS_CTX" | grep -q "api-patterns"'
# css-tricks might still appear (global, no domain filter blocks it) but api-patterns should be first/present
assert "Cold start — context not empty" '[ -n "$CS_CTX" ]'

rm -rf "$CS_DIR" /tmp/cold-start-proj

# --- End cold start tests ---

# --- Fuzzy dedup (PreToolUse) tests ---
DEDUP_HOOK="$HOME/.claude/hooks/memory-dedup.sh"
if [ -x "$DEDUP_HOOK" ] || [ -L "$DEDUP_HOOK" ]; then
if command -v uv >/dev/null 2>&1; then

DD_DIR=$(mktemp -d)
DD_MEM="$DD_DIR/memory"
mkdir -p "$DD_MEM/mistakes"

cat > "$DD_MEM/mistakes/existing-mistake.md" << 'DEOF'
---
type: mistake
project: global
status: active
recurrence: 2
root-cause: "Claude jumps to first hypothesis without listing alternatives"
prevention: "List 2-3 possible causes"
---
DEOF

# Test: block high-similarity duplicate (exit 2)
DD_JSON1=$(jq -n --arg sid "dd-1" --arg fp "$DD_MEM/mistakes/dupe.md" \
  --arg content "$(printf '%s\n' '---' 'type: mistake' 'root-cause: "Claude jumps to first hypothesis without listing alternative causes"' '---')" \
  '{session_id:$sid,tool_name:"Write",tool_input:{file_path:$fp,content:$content}}')
DD_EXIT1=0
DD_OUT1=$(printf '%s\n' "$DD_JSON1" | CLAUDE_MEMORY_DIR="$DD_MEM" bash "$DEDUP_HOOK" 2>&1) || DD_EXIT1=$?
assert "Dedup — blocks high-similarity duplicate" '[ "$DD_EXIT1" -eq 2 ]'
assert "Dedup — block message mentions existing file" 'echo "$DD_OUT1" | grep -q "existing-mistake"'

# Test: allow unique mistake (exit 0)
DD_JSON2=$(jq -n --arg sid "dd-2" --arg fp "$DD_MEM/mistakes/css-safari.md" \
  --arg content "$(printf '%s\n' '---' 'type: mistake' 'root-cause: "CSS grid layout breaks on Safari due to gap property"' '---')" \
  '{session_id:$sid,tool_name:"Write",tool_input:{file_path:$fp,content:$content}}')
DD_OUT2=$(printf '%s\n' "$DD_JSON2" | CLAUDE_MEMORY_DIR="$DD_MEM" bash "$DEDUP_HOOK" 2>&1)
DD_EXIT2=$?
assert "Dedup — allows unique mistake" '[ "$DD_EXIT2" -eq 0 ]'

# Test: skip non-mistakes paths (exit 0)
DD_JSON3=$(jq -n --arg sid "dd-3" --arg fp "$DD_MEM/feedback/test.md" --arg content "anything" \
  '{session_id:$sid,tool_name:"Write",tool_input:{file_path:$fp,content:$content}}')
DD_OUT3=$(printf '%s\n' "$DD_JSON3" | CLAUDE_MEMORY_DIR="$DD_MEM" bash "$DEDUP_HOOK" 2>&1)
assert "Dedup — skips non-mistakes path" '[ $? -eq 0 ] && [ -z "$DD_OUT3" ]'

# Test: skip existing file overwrite (exit 0)
DD_JSON4=$(jq -n --arg sid "dd-4" --arg fp "$DD_MEM/mistakes/existing-mistake.md" --arg content "update" \
  '{session_id:$sid,tool_name:"Write",tool_input:{file_path:$fp,content:$content}}')
DD_OUT4=$(printf '%s\n' "$DD_JSON4" | CLAUDE_MEMORY_DIR="$DD_MEM" bash "$DEDUP_HOOK" 2>&1)
assert "Dedup — skips overwrite of existing file" '[ $? -eq 0 ]'

# Test: WAL logged on block
assert "Dedup — WAL has dedup-blocked entry" 'grep -q "dedup-blocked" "$DD_MEM/.wal" 2>/dev/null'

rm -rf "$DD_DIR"

fi
fi
# --- End dedup tests ---

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
printf '2026-04-11|inject|css-var-bug|op-test-session\n' > "$OP_MEM/.wal"
OP_MARKER="/tmp/.claude-session-op-test-session"
touch -t 202601010000 "$OP_MARKER"
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

# Test: session-metrics written to WAL
SM_DIR=$(mktemp -d)
SM_MEM="$SM_DIR/memory"
mkdir -p "$SM_MEM/projects"
cat > "$SM_MEM/projects.json" << 'EOF'
{}
EOF
SM_MARKER="/tmp/.claude-session-sm-test-session"
touch -t 202601010000 "$SM_MARKER"
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

# Test: WAL compaction preserves outcome and metrics events
WALC2_DIR=$(mktemp -d)
WALC2_MEM="$WALC2_DIR/memory"
mkdir -p "$WALC2_MEM"
{
  for day in $(seq 15 50); do
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
