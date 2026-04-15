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

# Test: outcome hook — dedup same session write (regression for double outcome-new)
echo '{"session_id":"outcome-test-session","tool_name":"Write","tool_input":{"file_path":"'"$OUTCOME_MEM"'/mistakes/brand-new-bug.md","content":"test"}}' | CLAUDE_MEMORY_DIR="$OUTCOME_MEM" bash "$HOME/.claude/hooks/memory-outcome.sh" 2>/dev/null
OUTCOME_NEW_COUNT=$(grep -c "outcome-new|brand-new-bug|outcome-test-session" "$OUTCOME_MEM/.wal")
assert "Outcome hook — outcome-new deduped within session" '[ "$OUTCOME_NEW_COUNT" -eq 1 ]'

# Test: outcome hook — non-mistake file: no outcome entry, but cascade scan runs
echo '{"session_id":"outcome-test-session","tool_name":"Write","tool_input":{"file_path":"'"$OUTCOME_MEM"'/feedback/test.md","content":"test"}}' | CLAUDE_MEMORY_DIR="$OUTCOME_MEM" bash "$HOME/.claude/hooks/memory-outcome.sh" 2>/dev/null
OUTCOME_LINES=$(wc -l < "$OUTCOME_MEM/.wal")
assert "Outcome hook — non-mistake no outcome entry" '[ "$OUTCOME_LINES" -eq 3 ]'
rm -rf "$OUTCOME_DIR"

# Test: cascade detection — instance_of child detected when parent updated
CASC_DIR=$(mktemp -d)
CASC_MEM="$CASC_DIR/memory"
mkdir -p "$CASC_MEM"/{mistakes,feedback,knowledge}

cat > "$CASC_MEM/mistakes/parent-bug.md" << 'EOF'
---
type: mistake
project: global
status: active
severity: major
recurrence: 3
root-cause: "Parent root cause"
prevention: "Parent prevention"
---
Parent bug body.
EOF

cat > "$CASC_MEM/mistakes/child-bug.md" << 'EOF'
---
type: mistake
project: global
status: active
severity: minor
recurrence: 1
root-cause: "Child root cause"
prevention: "Child prevention"
related:
  - parent-bug: instance_of
---
Child bug body.
EOF

# Write to parent — should cascade to child
echo '{"session_id":"casc-session","tool_name":"Write","tool_input":{"file_path":"'"$CASC_MEM"'/mistakes/parent-bug.md","content":"updated"}}' | CLAUDE_MEMORY_DIR="$CASC_MEM" bash "$HOME/.claude/hooks/memory-outcome.sh" 2>/dev/null
assert "Cascade — child detected on parent update" 'grep -q "cascade-review|child-bug|parent:parent-bug" "$CASC_MEM/.wal"'
assert "Cascade — outcome-new also written for mistake" 'grep -q "outcome-new|parent-bug" "$CASC_MEM/.wal"'

# Write to a feedback file with a child in knowledge
cat > "$CASC_MEM/feedback/parent-fb.md" << 'EOF'
---
type: feedback
project: global
status: active
---
Parent feedback.
EOF

cat > "$CASC_MEM/knowledge/child-kb.md" << 'EOF'
---
type: knowledge
project: global
status: active
related:
  - parent-fb: instance_of
---
Child knowledge.
EOF

echo '{"session_id":"casc-session","tool_name":"Write","tool_input":{"file_path":"'"$CASC_MEM"'/feedback/parent-fb.md","content":"updated"}}' | CLAUDE_MEMORY_DIR="$CASC_MEM" bash "$HOME/.claude/hooks/memory-outcome.sh" 2>/dev/null
assert "Cascade — cross-dir instance_of detected" 'grep -q "cascade-review|child-kb|parent:parent-fb" "$CASC_MEM/.wal"'
assert "Cascade — no outcome entry for non-mistake" '! grep -q "outcome-.*parent-fb" "$CASC_MEM/.wal"'

# File without instance_of children — no cascade
cat > "$CASC_MEM/feedback/lonely.md" << 'EOF'
---
type: feedback
project: global
status: active
---
No children here.
EOF

CASC_LINES_BEFORE=$(wc -l < "$CASC_MEM/.wal")
echo '{"session_id":"casc-session","tool_name":"Write","tool_input":{"file_path":"'"$CASC_MEM"'/feedback/lonely.md","content":"updated"}}' | CLAUDE_MEMORY_DIR="$CASC_MEM" bash "$HOME/.claude/hooks/memory-outcome.sh" 2>/dev/null
CASC_LINES_AFTER=$(wc -l < "$CASC_MEM/.wal")
assert "Cascade — no cascade for file without children" '[ "$CASC_LINES_BEFORE" -eq "$CASC_LINES_AFTER" ]'

rm -rf "$CASC_DIR"

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

# Test: strategy-used — clean session + injected strategy
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
CLAUDE_MEMORY_DIR="$AN_MEM" bash "$HOME/.claude/hooks/memory-analytics.sh" 2>/dev/null
assert "Analytics — report created" '[ -f "$AN_MEM/.analytics-report" ]'
assert "Analytics — has winners" 'grep -q "winner-file" "$AN_MEM/.analytics-report"'
assert "Analytics — has noise" 'grep -q "noise-file" "$AN_MEM/.analytics-report"'
assert "Analytics — has clean_ratio" 'grep -q "clean_ratio" "$AN_MEM/.analytics-report"'
assert "Analytics — has strategy gaps" 'grep -qi "backend" "$AN_MEM/.analytics-report"'
rm -rf "$AN_DIR"

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
{
  echo "2026-04-05|inject|noisy-fb|s1"
  echo "2026-04-06|inject|noisy-fb|s2"
  echo "2026-04-07|inject|noisy-fb|s3"
  echo "2026-04-05|inject|good-fb|s1"
  echo "2026-04-06|inject|good-fb|s2"
  echo "2026-04-07|inject|good-fb|s3"
} > "$NP_MEM/.wal"
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

# Test: analytics trigger — stale report triggers rebuild
AT_DIR=$(mktemp -d)
AT_MEM="$AT_DIR/memory"
mkdir -p "$AT_MEM"
echo "date: 2026-03-01" > "$AT_MEM/.analytics-report"
if [[ "$OSTYPE" == darwin* ]]; then
  touch -t $(date -v-10d +%Y%m%d0000) "$AT_MEM/.analytics-report"
else
  touch -d "10 days ago" "$AT_MEM/.analytics-report"
fi
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
sleep 1
assert "Analytics trigger — report refreshed" '[ -f "$AT_MEM/.analytics-report" ]'
if [[ "$OSTYPE" == darwin* ]]; then
  ar_mtime=$(stat -f %m "$AT_MEM/.analytics-report" 2>/dev/null || echo 0)
else
  ar_mtime=$(stat -c %Y "$AT_MEM/.analytics-report" 2>/dev/null || echo 0)
fi
now_at=$(date +%s)
ar_age=$(( now_at - ar_mtime ))
assert "Analytics trigger — report is fresh" '[ "$ar_age" -lt 5 ]'
rm -rf "$AT_DIR" "$AT_MARKER"

# --- Full pipeline integration test ---
# Test: full pipeline — session-start injects, session-stop writes outcomes
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

# --- Cluster activation tests (v0.4 "Wiki") ---

# Cluster fixtures: temp dir with related files
CL_DIR=$(mktemp -d)
CL_MEM="$CL_DIR/memory"
mkdir -p "$CL_MEM"/{mistakes,feedback,strategies,knowledge,projects,decisions,continuity,notes}

cat > "$CL_MEM/projects.json" << 'PJSON'
{"/tmp/cluster-test": "cluster-proj"}
PJSON
cat > "$CL_MEM/projects-domains.json" << 'DJSON'
{"cluster-proj": ["debugging"]}
DJSON

# 1. Mistake with forward links to two related files
cat > "$CL_MEM/mistakes/root-cause-bug.md" << 'EOF'
---
type: mistake
project: global
status: active
severity: major
recurrence: 5
injected: 2026-04-01
referenced: 2026-04-11
root-cause: "Jumps to first hypothesis without alternatives"
prevention: "List 2-3 possible root causes before fixing"
domains: [debugging]
keywords: [debugging, root-cause]
decay_rate: 0.03
ref_count: 12
related:
  - debug-feedback: reinforces
  - debug-strategy: reinforces
---
Root cause diagnosis mistake — always list alternatives first.
EOF

# 2. Feedback file — should be pulled by cluster (forward link target)
cat > "$CL_MEM/feedback/debug-feedback.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2026-04-08
domains: [debugging]
keywords: [debugging]
decay_rate: 0.05
ref_count: 4
---
When debugging, generate 2-3 root cause hypotheses before committing to a fix.
EOF

# 3. Strategy file — should be pulled by cluster (forward link target)
cat > "$CL_MEM/strategies/debug-strategy.md" << 'EOF'
---
type: strategy
project: global
status: active
referenced: 2026-04-07
domains: [debugging, strategy]
keywords: [debugging, strategy]
decay_rate: 0.04
ref_count: 6
---
1. Read the error message carefully
2. List 2-3 hypotheses
3. Test cheapest hypothesis first
EOF

# 4. Feedback with reverse link — tests reverse scan (points TO root-cause-bug)
cat > "$CL_MEM/feedback/reverse-linked.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2026-04-06
domains: [debugging]
keywords: [debugging, instance]
decay_rate: 0.05
ref_count: 2
related:
  - root-cause-bug: instance_of
---
This is a specific instance of the root cause diagnosis problem.
EOF

# 5. Contradicts pair — old-rule links to new-rule as contradicts
cat > "$CL_MEM/feedback/old-rule.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2026-04-05
domains: [debugging]
keywords: [debugging, old-rule]
decay_rate: 0.05
ref_count: 3
related:
  - new-rule: contradicts
---
Old debugging rule: always add console.log first.
EOF

cat > "$CL_MEM/feedback/new-rule.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2026-04-11
domains: [debugging]
keywords: [debugging, new-rule]
decay_rate: 0.05
ref_count: 7
---
New rule: use debugger breakpoints, not console.log spam.
EOF

# 6. Cascade test — instance_of pointing to root-cause-bug
cat > "$CL_MEM/mistakes/specific-css-bug.md" << 'EOF'
---
type: mistake
project: global
status: active
severity: minor
recurrence: 1
injected: 2026-04-05
referenced: 2026-04-09
root-cause: "Applied first CSS fix without checking cascade"
prevention: "Trace CSS from viewport down"
domains: [css, debugging]
keywords: [css, debugging]
decay_rate: 0.05
ref_count: 2
related:
  - root-cause-bug: instance_of
---
Specific CSS instance of root cause diagnosis problem.
EOF

# 7. Superseded file — should NOT be loaded by cluster
cat > "$CL_MEM/feedback/deprecated-rule.md" << 'EOF'
---
type: feedback
project: global
status: superseded
referenced: 2026-03-01
domains: [debugging]
keywords: [debugging, deprecated]
decay_rate: 0.05
ref_count: 1
---
This rule is superseded and should not appear.
EOF

# Run hook with cluster fixtures
mkdir -p /tmp/cluster-test
CL_OUT=$(echo '{"session_id":"cluster-test","cwd":"/tmp/cluster-test"}' | CLAUDE_MEMORY_DIR="$CL_MEM" bash "$HOOK" 2>/dev/null)
CL_CTX=$(printf '%s' "$CL_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')

# Cluster loading assertions
assert "Cluster — root-cause-bug injected by main pass" 'printf "%s" "$CL_CTX" | grep -q "root-cause-bug"'
assert "Cluster — debug-feedback loaded (forward link)" 'printf "%s" "$CL_CTX" | grep -q "debug-feedback"'
assert "Cluster — debug-strategy loaded (forward link)" 'printf "%s" "$CL_CTX" | grep -q "debug-strategy"'
assert "Cluster — reverse-linked loaded (reverse scan)" 'printf "%s" "$CL_CTX" | grep -q "reverse-linked"'
assert "Cluster — provenance shown (via reinforces)" 'printf "%s" "$CL_CTX" | grep -iq "via.*reinforces\|via.*instance_of"'
assert "Cluster — WAL has cluster-load events" 'grep -q "cluster-load" "$CL_MEM/.wal"'
assert "Cluster — superseded file NOT loaded" '! printf "%s" "$CL_CTX" | grep -q "deprecated-rule"'

# Contradicts assertions
assert "Cluster contradicts — warning appears" 'printf "%s" "$CL_CTX" | grep -iq "конфликт\|contradicts"'
assert "Cluster contradicts — priority marker" 'printf "%s" "$CL_CTX" | grep -q "\[ПРИОРИТЕТ\]"'

rm -rf /tmp/cluster-test

# --- Cluster cap test (max +4 files) ---
CAP_DIR=$(mktemp -d)
CAP_MEM="$CAP_DIR/memory"
mkdir -p "$CAP_MEM"/{mistakes,feedback,projects}

cat > "$CAP_MEM/projects.json" << 'PJSON'
{"/tmp/cap-test": "cap-proj"}
PJSON
cat > "$CAP_MEM/projects-domains.json" << 'DJSON'
{"cap-proj": ["testing"]}
DJSON

# Hub mistake with 8 related feedback files
cat > "$CAP_MEM/mistakes/hub-mistake.md" << 'EOF'
---
type: mistake
project: global
status: active
severity: major
recurrence: 5
injected: 2026-04-01
referenced: 2026-04-11
root-cause: "Hub mistake linking many files"
prevention: "Cap test prevention"
domains: [testing]
keywords: [testing]
decay_rate: 0.03
ref_count: 10
related:
  - cap-fb-1: reinforces
  - cap-fb-2: reinforces
  - cap-fb-3: reinforces
  - cap-fb-4: reinforces
  - cap-fb-5: reinforces
  - cap-fb-6: reinforces
  - cap-fb-7: reinforces
  - cap-fb-8: reinforces
---
Hub mistake with many related files to test cap.
EOF

# Create 8 feedback files as targets
for i in $(seq 1 8); do
cat > "$CAP_MEM/feedback/cap-fb-$i.md" << CAPEOF
---
type: feedback
project: global
status: active
referenced: 2026-04-0$((i % 9 + 1))
domains: [testing]
keywords: [testing, cap]
decay_rate: 0.05
ref_count: $i
---
Cap test feedback file number $i.
CAPEOF
done

mkdir -p /tmp/cap-test
echo '{"session_id":"cap-test","cwd":"/tmp/cap-test"}' | CLAUDE_MEMORY_DIR="$CAP_MEM" bash "$HOOK" >/dev/null 2>&1
CAP_CLUSTER_COUNT=$(grep -c "cluster-load" "$CAP_MEM/.wal" 2>/dev/null || echo 0)
assert "Cluster cap — max 4 files loaded by cluster" '[ "$CAP_CLUSTER_COUNT" -le 4 ]'

rm -rf "$CAP_DIR" /tmp/cap-test

# --- Cascade review display test ---
CASCADE_DIR=$(mktemp -d)
CASCADE_MEM="$CASCADE_DIR/memory"
mkdir -p "$CASCADE_MEM"/{mistakes,feedback,projects}

cat > "$CASCADE_MEM/projects.json" << 'PJSON'
{"/tmp/cascade-test": "cascade-proj"}
PJSON
cat > "$CASCADE_MEM/projects-domains.json" << 'DJSON'
{"cascade-proj": ["css"]}
DJSON

cat > "$CASCADE_MEM/mistakes/cascade-mistake.md" << 'EOF'
---
type: mistake
project: global
status: active
severity: major
recurrence: 3
injected: 2026-04-01
referenced: 2026-04-11
root-cause: "CSS cascade test mistake"
prevention: "Trace from viewport"
domains: [css]
keywords: [css, cascade]
decay_rate: 0.03
ref_count: 5
---
Cascade review test mistake.
EOF

# Pre-populate WAL with a cascade-review event for this mistake (format: date|cascade-review|child|parent:parent-slug)
printf '2026-04-11|cascade-review|cascade-mistake|parent:some-parent\n' > "$CASCADE_MEM/.wal"

mkdir -p /tmp/cascade-test
CASCADE_OUT=$(echo '{"session_id":"cascade-test","cwd":"/tmp/cascade-test"}' | CLAUDE_MEMORY_DIR="$CASCADE_MEM" bash "$HOOK" 2>/dev/null)
CASCADE_CTX=$(printf '%s' "$CASCADE_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')
assert "Cascade display — REVIEW marker appears" 'printf "%s" "$CASCADE_CTX" | grep -q "REVIEW.*some-parent.*updated"'

rm -rf "$CASCADE_DIR" /tmp/cascade-test

# Cleanup cluster test dirs
rm -rf "$CL_DIR"

# --- End cluster activation tests ---

# --- Prompt triggers tests (v0.5) ---

TR_HOOK="$(dirname "$0")/user-prompt-submit.sh"
TR_DIR=$(mktemp -d)
TR_MEM="$TR_DIR/memory"
mkdir -p "$TR_MEM"/{mistakes,feedback,strategies,knowledge,notes}

# Mistake with `trigger:` single (backcompat)
cat > "$TR_MEM/mistakes/trigger-single.md" << 'EOF'
---
type: mistake
status: active
severity: major
recurrence: 3
ref_count: 10
trigger: "tailwind hsl"
---

Body of single-trigger mistake.
EOF

# Strategy with `triggers:` array (new syntax)
cat > "$TR_MEM/strategies/trigger-array.md" << 'EOF'
---
type: strategy
status: active
ref_count: 5
triggers:
  - "css layout"
  - "flex gap"
---

Body of array-trigger strategy.
EOF

# Pinned mistake (higher priority)
cat > "$TR_MEM/mistakes/trigger-pinned.md" << 'EOF'
---
type: mistake
status: pinned
severity: minor
recurrence: 1
ref_count: 1
trigger: "docker stale"
---

Body of pinned mistake.
EOF

# Active mistake same trigger word (lower priority than pinned)
cat > "$TR_MEM/mistakes/trigger-active-dup.md" << 'EOF'
---
type: mistake
status: active
severity: minor
recurrence: 10
ref_count: 100
trigger: "docker stale"
---

Body of active-dup mistake.
EOF

# File with no triggers (should never match)
cat > "$TR_MEM/feedback/no-triggers.md" << 'EOF'
---
type: feedback
status: active
---

This file has no triggers.
EOF

# Inactive file with triggers (should NOT match)
cat > "$TR_MEM/mistakes/inactive-trigger.md" << 'EOF'
---
type: mistake
status: superseded
severity: major
trigger: "inactive phrase"
---

Inactive body.
EOF

# --- Test 1: single trigger match ---
TR_OUT=$(echo '{"session_id":"trtest-1","prompt":"помоги с tailwind hsl"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
TR_CTX=$(printf '%s' "$TR_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')
assert "Trigger — single trigger: match" 'printf "%s" "$TR_CTX" | grep -q "### trigger-single"'
assert "Trigger — single trigger: matched phrase quoted" 'printf "%s" "$TR_CTX" | grep -q "matched: \"tailwind hsl\""'

# --- Test 2: case-insensitive ---
TR_OUT=$(echo '{"session_id":"trtest-2","prompt":"Помогите с TAILWIND HSL"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
TR_CTX=$(printf '%s' "$TR_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')
assert "Trigger — case-insensitive match" 'printf "%s" "$TR_CTX" | grep -q "### trigger-single"'

# --- Test 3: triggers: array match ---
TR_OUT=$(echo '{"session_id":"trtest-3","prompt":"есть проблема с css layout"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
TR_CTX=$(printf '%s' "$TR_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')
assert "Trigger — array trigger match (phrase 1)" 'printf "%s" "$TR_CTX" | grep -q "### trigger-array"'

TR_OUT=$(echo '{"session_id":"trtest-3b","prompt":"плохой flex gap"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
TR_CTX=$(printf '%s' "$TR_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')
assert "Trigger — array trigger match (phrase 2)" 'printf "%s" "$TR_CTX" | grep -q "### trigger-array"'

# --- Test 4: no match → empty output ---
TR_OUT=$(echo '{"session_id":"trtest-4","prompt":"совершенно нерелевантный текст"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
assert "Trigger — no match: empty output" '[ -z "$TR_OUT" ]'

# --- Test 5: dedup via /tmp list ---
mkdir -p "$TR_MEM/.runtime"
echo "trigger-single" > "$TR_MEM/.runtime/injected-trtest-dedup.list"
TR_OUT=$(echo '{"session_id":"trtest-dedup","prompt":"tailwind hsl"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
assert "Trigger — dedup: excludes already-injected slug" '[ -z "$TR_OUT" ]'
rm -f "$TR_MEM/.runtime/injected-trtest-dedup.list"

# --- Test 6: pinned priority over active ---
# Both trigger-pinned and trigger-active-dup match "docker stale"
TR_OUT=$(echo '{"session_id":"trtest-6","prompt":"docker stale"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
TR_CTX=$(printf '%s' "$TR_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')
# Pinned should come first (sort descending)
assert "Trigger — pinned before active in output" 'printf "%s" "$TR_CTX" | awk "/### trigger-pinned/{p=NR} /### trigger-active-dup/{a=NR} END{exit !(p && a && p < a)}"'

# --- Test 7: WAL trigger-match logged ---
# Clear WAL
: > "$TR_MEM/.wal"
TR_OUT=$(echo '{"session_id":"trtest-wal","prompt":"tailwind hsl test"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
assert "Trigger — WAL event trigger-match written" 'grep -q "trigger-match|trigger-single|trtest-wal" "$TR_MEM/.wal"'

# --- Test 8: cap MAX_TRIGGERED=4 ---
# Create 5 active files with same trigger word
for i in 1 2 3 4 5; do
  cat > "$TR_MEM/mistakes/bulk-${i}.md" << EOF
---
type: mistake
status: active
severity: minor
recurrence: ${i}
trigger: "bulk match"
---

Bulk $i body.
EOF
done
TR_OUT=$(echo '{"session_id":"trtest-cap","prompt":"bulk match test"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
TR_CTX=$(printf '%s' "$TR_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')
TR_COUNT=$(printf '%s' "$TR_CTX" | grep -c "^### bulk-" || true)
assert "Trigger — cap MAX_TRIGGERED=4 enforced" '[ "$TR_COUNT" -eq 4 ]'

# --- Test 9: inactive status ignored ---
TR_OUT=$(echo '{"session_id":"trtest-inactive","prompt":"inactive phrase"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
assert "Trigger — superseded file ignored" '[ -z "$TR_OUT" ]'

# --- Test 10: trigger inside fenced code block → no match ---
TR_P10=$'text before\n```\nпример tailwind hsl внутри кода\n```\nи после'
TR_OUT=$(printf '{"session_id":"trtest-fence","prompt":%s}' "$(printf '%s' "$TR_P10" | jq -Rs .)" | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
assert "Trigger — fenced code block ignored" '[ -z "$TR_OUT" ]'

# --- Test 11: trigger inside inline backticks → no match ---
TR_OUT=$(echo '{"session_id":"trtest-inline","prompt":"ссылка `tailwind hsl` как пример"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
assert "Trigger — inline backtick ignored" '[ -z "$TR_OUT" ]'

# --- Test 12: trigger inside blockquote → no match ---
TR_P12=$'обычный текст\n> пример tailwind hsl внутри цитаты'
TR_OUT=$(printf '{"session_id":"trtest-quote","prompt":%s}' "$(printf '%s' "$TR_P12" | jq -Rs .)" | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
assert "Trigger — blockquote line ignored" '[ -z "$TR_OUT" ]'

# --- Test 13: trigger outside code fence still matches (regression) ---
TR_P13=$'описание\n```\nsome other code\n```\nнужен фикс tailwind hsl срочно'
TR_OUT=$(printf '{"session_id":"trtest-mixed","prompt":%s}' "$(printf '%s' "$TR_P13" | jq -Rs .)" | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
TR_CTX=$(printf '%s' "$TR_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')
assert "Trigger — match outside fence still works" 'printf "%s" "$TR_CTX" | grep -q "### trigger-single"'

# --- Test 14: negation «не» before trigger → no match ---
TR_OUT=$(echo '{"session_id":"trtest-neg1","prompt":"не используй tailwind hsl в этом проекте"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
assert "Trigger — negation «не» skips match" '[ -z "$TR_OUT" ]'

# --- Test 15: negation «уже пофиксил» before trigger → no match ---
TR_OUT=$(echo '{"session_id":"trtest-neg2","prompt":"уже пофиксил tailwind hsl вчера"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
assert "Trigger — «уже» skips match" '[ -z "$TR_OUT" ]'

# --- Test 16: English «already» negation → no match ---
TR_OUT=$(echo '{"session_id":"trtest-neg3","prompt":"already fixed the tailwind hsl issue"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
assert "Trigger — «already» skips match" '[ -z "$TR_OUT" ]'

# --- Test 16b: «ignore» negation → no match ---
TR_OUT=$(echo '{"session_id":"trtest-neg4","prompt":"ignore tailwind hsl for now"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
assert "Trigger — «ignore» skips match" '[ -z "$TR_OUT" ]'

# --- Test 16c: «without» negation → no match ---
TR_OUT=$(echo '{"session_id":"trtest-neg5","prompt":"do it without tailwind hsl"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
assert "Trigger — «without» skips match" '[ -z "$TR_OUT" ]'

# --- Test 16d: Russian «игнор» negation → no match ---
TR_OUT=$(echo '{"session_id":"trtest-neg6","prompt":"игнорируй tailwind hsl правило"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
assert "Trigger — «игнор» skips match" '[ -z "$TR_OUT" ]'

# --- Test 17: project filter — mismatched project demoted vs global ---
mkdir -p "$TR_MEM/projects"
cat > "$TR_MEM/projects.json" << 'PJSON'
{"/tmp/project-a": "project-a"}
PJSON

cat > "$TR_MEM/mistakes/trigger-other-proj.md" << 'EOF'
---
type: mistake
project: project-b
status: active
severity: major
recurrence: 10
ref_count: 100
trigger: "shared phrase"
---
Body of other-proj mistake.
EOF

cat > "$TR_MEM/mistakes/trigger-global-match.md" << 'EOF'
---
type: mistake
project: global
status: active
severity: minor
recurrence: 1
ref_count: 1
trigger: "shared phrase"
---
Body of global mistake.
EOF

cat > "$TR_MEM/mistakes/trigger-current-proj.md" << 'EOF'
---
type: mistake
project: project-a
status: active
severity: minor
recurrence: 1
ref_count: 1
trigger: "shared phrase"
---
Body of current-proj mistake.
EOF

TR_OUT=$(echo '{"session_id":"trtest-proj","cwd":"/tmp/project-a","prompt":"shared phrase test"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
TR_CTX=$(printf '%s' "$TR_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')
assert "Project filter — current-proj ranked before global and mismatch" '
  printf "%s" "$TR_CTX" | awk "
    /### trigger-current-proj/{c=NR}
    /### trigger-global-match/{g=NR}
    /### trigger-other-proj/{o=NR}
    END{exit !(c && g && o && c<g && g<o)}
  "
'
rm -f "$TR_MEM/mistakes/trigger-other-proj.md" "$TR_MEM/mistakes/trigger-global-match.md" "$TR_MEM/mistakes/trigger-current-proj.md"

# --- Test 18: ranking — fresh with low ref_count beats old with high ref_count ---
TODAY_Y=$(date +%Y-%m-%d)
if [[ "$OSTYPE" == darwin* ]]; then
  OLD_DATE=$(date -v-200d +%Y-%m-%d)
else
  OLD_DATE=$(date -d "200 days ago" +%Y-%m-%d)
fi

cat > "$TR_MEM/mistakes/trigger-fresh.md" << EOF
---
type: mistake
status: active
severity: minor
recurrence: 1
ref_count: 1
created: $TODAY_Y
trigger: "rank phrase"
---
Fresh body.
EOF

cat > "$TR_MEM/mistakes/trigger-old-popular.md" << EOF
---
type: mistake
status: active
severity: minor
recurrence: 1
ref_count: 108
created: $OLD_DATE
trigger: "rank phrase"
---
Old popular body.
EOF

TR_OUT=$(echo '{"session_id":"trtest-rank","prompt":"rank phrase check"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
TR_CTX=$(printf '%s' "$TR_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')
assert "Ranking — fresh record wins over old high ref_count" '
  printf "%s" "$TR_CTX" | awk "
    /### trigger-fresh/{f=NR}
    /### trigger-old-popular/{o=NR}
    END{exit !(f && o && f<o)}
  "
'
rm -f "$TR_MEM/mistakes/trigger-fresh.md" "$TR_MEM/mistakes/trigger-old-popular.md"

# --- Test 19: ranking — severity beats recency ---
cat > "$TR_MEM/mistakes/trigger-fresh-minor.md" << EOF
---
type: mistake
status: active
severity: minor
recurrence: 1
ref_count: 1
created: $TODAY_Y
trigger: "sev phrase"
---
Fresh minor.
EOF

cat > "$TR_MEM/mistakes/trigger-old-major.md" << EOF
---
type: mistake
status: active
severity: major
recurrence: 1
ref_count: 1
created: $OLD_DATE
trigger: "sev phrase"
---
Old major.
EOF

TR_OUT=$(echo '{"session_id":"trtest-sev","prompt":"sev phrase"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$TR_HOOK" 2>/dev/null)
TR_CTX=$(printf '%s' "$TR_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')
assert "Ranking — major severity beats fresh minor" '
  printf "%s" "$TR_CTX" | awk "
    /### trigger-old-major/{m=NR}
    /### trigger-fresh-minor/{f=NR}
    END{exit !(m && f && m<f)}
  "
'
rm -f "$TR_MEM/mistakes/trigger-fresh-minor.md" "$TR_MEM/mistakes/trigger-old-major.md"

# --- Test 20e: feedback loop end-to-end — useful via evidence: frontmatter ---
FB_DIR=$(mktemp -d); FB_MEM="$FB_DIR/memory"
mkdir -p "$FB_MEM/mistakes"
cat > "$FB_MEM/mistakes/sql-rule.md" << 'EOF'
---
type: mistake
status: active
evidence:
  - "parameterized query"
---
Always parameterize.
EOF
cat > "$FB_MEM/mistakes/unused-rule.md" << 'EOF'
---
type: mistake
status: active
evidence:
  - "xyzzy nonexistent phrase"
---
Never applied.
EOF

TODAY=$(date +%Y-%m-%d)
printf '%s|inject|sql-rule|fb-e2e\n' "$TODAY" > "$FB_MEM/.wal"
printf '%s|inject|unused-rule|fb-e2e\n' "$TODAY" >> "$FB_MEM/.wal"

cat > "$FB_DIR/transcript.jsonl" << 'EOF'
{"type":"user","message":{"content":[{"type":"text","text":"fix the SQL bug"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"I will use a parameterized query to prevent injection."}]}}
EOF
FB_MARKER="/tmp/.claude-session-fb-e2e"
touch -t 202601010000 "$FB_MARKER"

printf '{"session_id":"fb-e2e","transcript_path":"%s/transcript.jsonl"}' "$FB_DIR" | \
  CLAUDE_MEMORY_DIR="$FB_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null >/dev/null

assert "Feedback e2e — evidence match → trigger-useful" \
  'grep -q "|trigger-useful|sql-rule|fb-e2e" "$FB_MEM/.wal"'
assert "Feedback e2e — no evidence match → trigger-silent" \
  'grep -q "|trigger-silent|unused-rule|fb-e2e" "$FB_MEM/.wal"'

rm -rf "$FB_DIR" "$FB_MARKER"

# --- Test 20f: feedback loop end-to-end — body-mining fallback ---
FB_DIR=$(mktemp -d); FB_MEM="$FB_DIR/memory"
mkdir -p "$FB_MEM/mistakes"
cat > "$FB_MEM/mistakes/bodyonly-rule.md" << 'EOF'
---
type: mistake
status: active
---
Use timezone-aware datetime objects for all reporting pipelines.
EOF

TODAY=$(date +%Y-%m-%d)
printf '%s|inject|bodyonly-rule|fb-body\n' "$TODAY" > "$FB_MEM/.wal"

cat > "$FB_DIR/transcript.jsonl" << 'EOF'
{"type":"user","message":{"content":[{"type":"text","text":"fix date handling"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Using timezone-aware datetime with explicit UTC for reporting."}]}}
EOF
FB_MARKER="/tmp/.claude-session-fb-body"
touch -t 202601010000 "$FB_MARKER"

printf '{"session_id":"fb-body","transcript_path":"%s/transcript.jsonl"}' "$FB_DIR" | \
  CLAUDE_MEMORY_DIR="$FB_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null >/dev/null

assert "Feedback e2e — body-mining ≥2 tokens → trigger-useful" \
  'grep -q "|trigger-useful|bodyonly-rule|fb-body" "$FB_MEM/.wal"'

rm -rf "$FB_DIR" "$FB_MARKER"

# --- Test 21: schema-error logged for broken frontmatter ---
SCH_DIR=$(mktemp -d); SCH_MEM="$SCH_DIR/memory"
mkdir -p "$SCH_MEM/mistakes"
cat > "$SCH_MEM/mistakes/missing-close.md" << 'EOF'
---
type: mistake
status: active
trigger: "sch phrase"
EOF
# Deliberately missing closing ---

echo '{"session_id":"sch-test","prompt":"unrelated text"}' | \
  CLAUDE_MEMORY_DIR="$SCH_MEM" bash "$TR_HOOK" 2>/dev/null >/dev/null
assert "Schema — missing closing --- logged as schema-error" \
  'grep -q "schema-error|missing-close|sch-test" "$SCH_MEM/.wal"'

# De-dup: calling again in same day should NOT append another entry
echo '{"session_id":"sch-test2","prompt":"another"}' | \
  CLAUDE_MEMORY_DIR="$SCH_MEM" bash "$TR_HOOK" 2>/dev/null >/dev/null
assert "Schema — de-duplicated within same day" \
  '[ "$(grep -c "schema-error|missing-close" "$SCH_MEM/.wal")" -eq 1 ]'

rm -rf "$SCH_DIR"

# --- Test 22: health-check — schema-error surfaces in SessionStart output ---
HC_DIR=$(mktemp -d); HC_MEM="$HC_DIR/memory"
mkdir -p "$HC_MEM/mistakes" "$HC_MEM/projects"
# Create one valid mistake so session-start has something to inject (otherwise early-exits)
cat > "$HC_MEM/mistakes/valid.md" << 'EOF'
---
type: mistake
status: active
severity: major
recurrence: 1
root-cause: "something"
prevention: "fix it"
---
EOF
echo "{}" > "$HC_MEM/projects.json"
# Pre-populate WAL with schema-error events
printf '%s|schema-error|broken-one|prev-session\n' "$(date +%Y-%m-%d)" > "$HC_MEM/.wal"
printf '%s|schema-error|broken-two|prev-session\n' "$(date +%Y-%m-%d)" >> "$HC_MEM/.wal"

HC_OUT=$(echo '{"session_id":"hc-test","cwd":"/tmp"}' | \
  CLAUDE_MEMORY_DIR="$HC_MEM" bash "$HOME/.claude/hooks/memory-session-start.sh" 2>/dev/null)
HC_CTX=$(printf '%s' "$HC_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""')
assert "Health — schema-error count surfaced in SessionStart" \
  'printf "%s" "$HC_CTX" | grep -q "schema.*2"'
assert "Health — names of broken files surfaced" \
  'printf "%s" "$HC_CTX" | grep -q "broken-one\|broken-two"'

rm -rf "$HC_DIR"

# --- Test 10: session-start writes dedup list ---
SS_HOOK="$(dirname "$0")/session-start.sh"
mkdir -p /tmp/trtest-ss-proj
cat > "$TR_MEM/projects.json" << 'PJSON'
{"/tmp/trtest-ss-proj": "ss-proj"}
PJSON
# Sanity: ensure file is created
SS_OUT=$(echo '{"session_id":"ss-dedup-test","cwd":"/tmp/trtest-ss-proj"}' | \
  CLAUDE_MEMORY_DIR="$TR_MEM" bash "$SS_HOOK" 2>/dev/null)
assert "Trigger — session-start writes dedup list" '[ -f "$TR_MEM/.runtime/injected-ss-dedup-test.list" ]'
rm -f "$TR_MEM/.runtime/injected-ss-dedup-test.list"
rm -rf /tmp/trtest-ss-proj

# Cleanup trigger test dir
rm -rf "$TR_DIR"

# --- End prompt triggers tests ---

# --- Test 20a: evidence-extract.sh — evidence_from_frontmatter parses YAML array ---
EX_DIR=$(mktemp -d); EX_MEM="$EX_DIR/memory"
mkdir -p "$EX_MEM/mistakes"
cat > "$EX_MEM/mistakes/ex-fm.md" << 'EOF'
---
type: mistake
evidence:
  - "parameterized query"
  - "timezone-aware datetime"
---
Body text.
EOF

# Source the lib and call the function
set +e
. "$HOME/.claude/hooks/lib/evidence-extract.sh" 2>/dev/null || \
  . "$(dirname "$0")/lib/evidence-extract.sh" 2>/dev/null || \
  . "/Users/akamash/Development/hypomnema/hooks/lib/evidence-extract.sh"
set -e
EX_OUT=$(evidence_from_frontmatter "$EX_MEM/mistakes/ex-fm.md")

assert "evidence_from_frontmatter — extracts YAML array entries" \
  'printf "%s" "$EX_OUT" | grep -qF "parameterized query" && printf "%s" "$EX_OUT" | grep -qF "timezone-aware datetime"'

rm -rf "$EX_DIR"

# --- Test 20b: evidence_from_body extracts content tokens, excludes stop-words + frontmatter ---
EX_DIR=$(mktemp -d); EX_MEM="$EX_DIR/memory"
mkdir -p "$EX_MEM/mistakes"
cat > "$EX_MEM/mistakes/ex-body.md" << 'EOF'
---
type: mistake
description: SQL injection prevention
---
Always use parameterized queries; never interpolate strings.
**Why:** Injection attacks are common.
**How to apply:** cursor.execute with placeholders.
EOF

set +e
. "$HOME/.claude/hooks/lib/evidence-extract.sh" 2>/dev/null || \
  . "$(dirname "$0")/lib/evidence-extract.sh" 2>/dev/null || \
  . "/Users/akamash/Development/hypomnema/hooks/lib/evidence-extract.sh"
set -e
EX_BODY=$(evidence_from_body "$EX_MEM/mistakes/ex-body.md")

# Must include content words from body
assert "evidence_from_body — includes 'parameterized'" \
  'printf "%s\n" "$EX_BODY" | grep -qx "parameterized"'
assert "evidence_from_body — includes 'queries'" \
  'printf "%s\n" "$EX_BODY" | grep -qx "queries"'
assert "evidence_from_body — includes 'interpolate'" \
  'printf "%s\n" "$EX_BODY" | grep -qx "interpolate"'

# Must EXCLUDE tokens from Why/How sections
assert "evidence_from_body — excludes tokens from **Why:** line" \
  '! printf "%s\n" "$EX_BODY" | grep -qx "injection"'
assert "evidence_from_body — excludes tokens from **How to apply:** line" \
  '! printf "%s\n" "$EX_BODY" | grep -qx "placeholders"'

# Must EXCLUDE frontmatter (description contains "prevention")
assert "evidence_from_body — excludes frontmatter description" \
  '! printf "%s\n" "$EX_BODY" | grep -qx "prevention"'

# Must EXCLUDE stop-words
assert "evidence_from_body — excludes stop-word 'never'" \
  '! printf "%s\n" "$EX_BODY" | grep -qx "never"'
assert "evidence_from_body — excludes stop-word 'always'" \
  '! printf "%s\n" "$EX_BODY" | grep -qx "always"'

rm -rf "$EX_DIR"

# --- Test 20c: evidence_from_body filters slug name ---
EX_DIR=$(mktemp -d); EX_MEM="$EX_DIR/memory"
mkdir -p "$EX_MEM/mistakes"
cat > "$EX_MEM/mistakes/selfslug.md" << 'EOF'
---
type: mistake
---
This memory is about selfslug concepts and parameterized handling.
EOF

set +e
. "$HOME/.claude/hooks/lib/evidence-extract.sh" 2>/dev/null || \
  . "$(dirname "$0")/lib/evidence-extract.sh" 2>/dev/null || \
  . "/Users/akamash/Development/hypomnema/hooks/lib/evidence-extract.sh"
set -e
EX_SELF=$(evidence_from_body "$EX_MEM/mistakes/selfslug.md")

assert "evidence_from_body — slug name filtered from tokens" \
  '! printf "%s\n" "$EX_SELF" | grep -qx "selfslug"'
assert "evidence_from_body — other content words still present" \
  'printf "%s\n" "$EX_SELF" | grep -qx "parameterized"'

rm -rf "$EX_DIR"

# --- Test 20d: evidence_from_body truncates at 10 KB ---
EX_DIR=$(mktemp -d); EX_MEM="$EX_DIR/memory"
mkdir -p "$EX_MEM/mistakes"
{
  printf -- '---\ntype: mistake\n---\n'
  printf 'uniquestart '
  # ~11 KB of filler between uniquestart and uniqueend
  head -c 11000 /dev/urandom | base64 | tr -d '=+/' | head -c 11000
  printf '\nuniqueend\n'
} > "$EX_MEM/mistakes/huge.md"

EX_HUGE=$(evidence_from_body "$EX_MEM/mistakes/huge.md")
assert "evidence_from_body — 'uniquestart' present (within 10 KB)" \
  'printf "%s\n" "$EX_HUGE" | grep -qx "uniquestart"'
assert "evidence_from_body — 'uniqueend' absent (beyond 10 KB)" \
  '! printf "%s\n" "$EX_HUGE" | grep -qx "uniqueend"'

rm -rf "$EX_DIR"

# --- Test 20e: evidence-missing WAL event when memory file absent ---
FB_DIR=$(mktemp -d); FB_MEM="$FB_DIR/memory"
mkdir -p "$FB_MEM/mistakes"
# NOTE: no ghost-file.md is created!

TODAY=$(date +%Y-%m-%d)
printf '%s|inject|ghost-file|fb-miss\n' "$TODAY" > "$FB_MEM/.wal"

cat > "$FB_DIR/transcript.jsonl" << 'EOF'
{"type":"user","message":{"content":[{"type":"text","text":"x"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"y"}]}}
EOF
FB_MARKER="/tmp/.claude-session-fb-miss"
touch -t 202601010000 "$FB_MARKER"

printf '{"session_id":"fb-miss","transcript_path":"%s/transcript.jsonl"}' "$FB_DIR" | \
  CLAUDE_MEMORY_DIR="$FB_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null >/dev/null

assert "Feedback — evidence-missing event when file absent" \
  'grep -q "|evidence-missing|ghost-file|fb-miss" "$FB_MEM/.wal"'
assert "Feedback — trigger-silent also written for missing file" \
  'grep -q "|trigger-silent|ghost-file|fb-miss" "$FB_MEM/.wal"'

rm -rf "$FB_DIR" "$FB_MARKER"

# --- Test 20f: evidence-empty WAL event when memory has no extractable evidence ---
FB_DIR=$(mktemp -d); FB_MEM="$FB_DIR/memory"
mkdir -p "$FB_MEM/mistakes"
cat > "$FB_MEM/mistakes/noevid.md" << 'EOF'
---
type: mistake
status: active
---
the.
EOF
# Body is one stop-word. No evidence frontmatter. evidence_from_body returns empty.

TODAY=$(date +%Y-%m-%d)
printf '%s|inject|noevid|fb-empty\n' "$TODAY" > "$FB_MEM/.wal"

cat > "$FB_DIR/transcript.jsonl" << 'EOF'
{"type":"user","message":{"content":[{"type":"text","text":"x"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"y"}]}}
EOF
FB_MARKER="/tmp/.claude-session-fb-empty"
touch -t 202601010000 "$FB_MARKER"

printf '{"session_id":"fb-empty","transcript_path":"%s/transcript.jsonl"}' "$FB_DIR" | \
  CLAUDE_MEMORY_DIR="$FB_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null >/dev/null

assert "Feedback — evidence-empty event when body has no content tokens" \
  'grep -q "|evidence-empty|noevid|fb-empty" "$FB_MEM/.wal"'
assert "Feedback — trigger-silent also written for empty evidence" \
  'grep -q "|trigger-silent|noevid|fb-empty" "$FB_MEM/.wal"'

rm -rf "$FB_DIR" "$FB_MARKER"

# --- Test 20g: case-insensitive evidence match ---
FB_DIR=$(mktemp -d); FB_MEM="$FB_DIR/memory"
mkdir -p "$FB_MEM/mistakes"
cat > "$FB_MEM/mistakes/case-rule.md" << 'EOF'
---
type: mistake
status: active
evidence:
  - "SQL injection"
---
Body.
EOF

TODAY=$(date +%Y-%m-%d)
printf '%s|inject|case-rule|fb-case\n' "$TODAY" > "$FB_MEM/.wal"

cat > "$FB_DIR/transcript.jsonl" << 'EOF'
{"type":"user","message":{"content":[{"type":"text","text":"x"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Preventing sql injection via prepared statements."}]}}
EOF
FB_MARKER="/tmp/.claude-session-fb-case"
touch -t 202601010000 "$FB_MARKER"

printf '{"session_id":"fb-case","transcript_path":"%s/transcript.jsonl"}' "$FB_DIR" | \
  CLAUDE_MEMORY_DIR="$FB_MEM" bash "$HOME/.claude/hooks/memory-stop.sh" 2>/dev/null >/dev/null

assert "Feedback — case-insensitive match works" \
  'grep -q "|trigger-useful|case-rule|fb-case" "$FB_MEM/.wal"'

rm -rf "$FB_DIR" "$FB_MARKER"

# --- Test 21: perl regex injection in section markers (v0.8) ---
# Two injected files cross-referencing via related: contradicts. The newer slug
# contains regex-special chars ([, ]) which would crash perl without escaping.
T21_DIR=$(mktemp -d); T21_MEM="$T21_DIR/memory"
mkdir -p "$T21_MEM"/{mistakes,feedback,knowledge,strategies,decisions,projects,continuity}

cat > "$T21_MEM/mistakes/normal-source.md" << 'EOF'
---
type: mistake
project: global
status: pinned
severity: critical
recurrence: 5
referenced: 2026-04-10
keywords: [shared, kw]
related:
  - edge)case: contradicts
root-cause: "Source bug"
prevention: "Source prevention"
---

Normal source body.
EOF

cat > "$T21_MEM/mistakes/edge)case.md" << 'EOF'
---
type: mistake
project: global
status: pinned
severity: critical
recurrence: 3
referenced: 2026-04-15
keywords: [shared, kw]
root-cause: "Target with regex-special slug"
prevention: "Target prevention"
---

Target body should survive perl pass.
EOF

T21_STDERR=$(mktemp)
T21_OUT=$(echo '{"session_id":"t21","cwd":"/tmp"}' | \
  CLAUDE_MEMORY_DIR="$T21_MEM" bash "$HOOK" 2>"$T21_STDERR")
T21_RC=$?
T21_CTX=$(printf '%s' "$T21_OUT" | jq -r '.hookSpecificOutput.additionalContext // ""' 2>/dev/null)
T21_ERR=$(cat "$T21_STDERR")

assert "perl-injection — hook exits 0 with regex-special slug" \
  '[ "$T21_RC" -eq 0 ]'
assert "perl-injection — no perl warning in stderr" \
  '! printf "%s" "$T21_ERR" | grep -qiE "(unmatched|trailing|nothing to repeat)"'
assert "perl-injection — target body survives perl pass" \
  'printf "%s" "$T21_CTX" | grep -q "Target body should survive perl pass"'
assert "perl-injection — source body still present" \
  'printf "%s" "$T21_CTX" | grep -q "Normal source body"'

rm -rf "$T21_DIR" "$T21_STDERR"

# --- Test 22: WAL feedback-loop read is locked (v0.8) ---
# session-stop.sh awk-reads $WAL_FILE for inject/trigger-match events;
# without a lock, concurrent appends from other sessions truncate lines.
T22_HOOK_SRC="$(cd "$(dirname "$0")/.." 2>/dev/null && pwd)/hooks/session-stop.sh"
[ -f "$T22_HOOK_SRC" ] || T22_HOOK_SRC="hooks/session-stop.sh"

assert "wal-lock — feedback awk read wrapped in wal_run_locked" \
  'grep -E "wal_run_locked[[:space:]]+awk" "$T22_HOOK_SRC" >/dev/null'

# --- Test 23: shell safety — pipefail in all executable hooks (v0.8) ---
T23_REPO="$(cd "$(dirname "$0")/.." 2>/dev/null && pwd)"
[ -d "$T23_REPO/hooks" ] || T23_REPO="."
T23_MISSING=""
for f in "$T23_REPO"/hooks/*.sh "$T23_REPO"/bin/*.sh "$T23_REPO"/install.sh; do
  [ -f "$f" ] || continue
  case "$(basename "$f")" in
    bench-memory.sh|test-memory-hooks.sh) continue ;;  # bench has set -e; tests have own discipline
  esac
  if ! head -10 "$f" | grep -q 'pipefail'; then
    T23_MISSING="$T23_MISSING $(basename "$f")"
  fi
done
assert "shell-safety — every hook/bin/install has pipefail (missing:$T23_MISSING )" \
  '[ -z "$T23_MISSING" ]'

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
