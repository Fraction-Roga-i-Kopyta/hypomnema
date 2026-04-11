# v0.4 "Вики" — Related Links & Graph Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add typed related-links between memory files with cluster activation, contradicts handling, and cascade signals.

**Architecture:** Односторонние `related:` поля в frontmatter. SessionStart хук делает второй проход (cluster pass) после основного scoring — подтягивает related-записи сверх лимита (+4 cap). Cascade detection через расширение memory-outcome.sh.

**Tech Stack:** bash, awk (BSD), perl, jq — тот же стек что v0.3.

---

### Task 1: Test fixtures with related fields

**Files:**
- Modify: `hooks/test-memory-hooks.sh` (после строки ~24, setup секция)

Создать тестовые файлы с `related:` полями для проверки всех четырёх типов связей.

- [ ] **Step 1: Добавить fixture-файлы с related**

В `test-memory-hooks.sh`, после создания базовых директорий (строка 24), найти место перед первым тестом и добавить отдельную fixture-секцию для cluster-тестов:

```bash
# --- Cluster test fixtures ---
CLUSTER_DIR=$(mktemp -d)
CLUSTER_MEM="$CLUSTER_DIR/memory"
mkdir -p "$CLUSTER_MEM"/{mistakes,feedback,strategies,knowledge,continuity,projects,decisions}

cat > "$CLUSTER_MEM/projects.json" << 'PJSON'
{"/tmp/cluster-test": "cluster-proj"}
PJSON

# Mistake with related → feedback (reinforces) + strategy (reinforces)
cat > "$CLUSTER_MEM/mistakes/root-cause-bug.md" << 'EOF'
---
type: mistake
project: global
status: active
severity: major
recurrence: 3
injected: 2026-04-01
referenced: 2026-04-10
root-cause: "Jumps to first hypothesis"
prevention: "List 2-3 root causes"
domains: [general]
keywords: [debugging, hypothesis]
decay_rate: slow
ref_count: 10
related:
  - debug-feedback: reinforces
  - debug-strategy: reinforces
---
EOF

# Feedback that the mistake links to — should be pulled by cluster
cat > "$CLUSTER_MEM/feedback/debug-feedback.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2026-04-08
domains: [general]
keywords: [debugging]
decay_rate: slow
ref_count: 5
---
Always list hypotheses before fixing.
EOF

# Strategy linked by mistake — should be pulled by cluster
cat > "$CLUSTER_MEM/strategies/debug-strategy.md" << 'EOF'
---
type: strategy
project: global
status: active
referenced: 2026-04-08
domains: [general]
keywords: [debugging, strategy]
trigger: "debugging session"
success_count: 2
decay_rate: normal
ref_count: 3
---
1. List 3 hypotheses
2. Test cheapest first
EOF

# Feedback with reverse link to mistake — tests reverse scan
cat > "$CLUSTER_MEM/feedback/reverse-linked.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2026-04-08
domains: [general]
keywords: [css, layout]
decay_rate: normal
ref_count: 2
related:
  - root-cause-bug: instance_of
---
CSS-specific feedback linked back to root cause mistake.
EOF

# Contradicts pair: two feedback files that conflict
cat > "$CLUSTER_MEM/feedback/old-rule.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2026-04-01
domains: [general]
keywords: [timeout]
decay_rate: normal
ref_count: 1
related:
  - new-rule: contradicts
---
Hard timeout after 30 minutes.
EOF

cat > "$CLUSTER_MEM/feedback/new-rule.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2026-04-10
domains: [general]
keywords: [timeout]
decay_rate: normal
ref_count: 3
---
Auto-extend session on activity.
EOF

# File with instance_of — for cascade tests
cat > "$CLUSTER_MEM/mistakes/specific-css-bug.md" << 'EOF'
---
type: mistake
project: global
status: active
severity: minor
recurrence: 1
injected: 2026-04-05
referenced: 2026-04-09
root-cause: "CSS child-first"
prevention: "Trace from viewport"
domains: [css]
keywords: [css]
decay_rate: normal
ref_count: 2
related:
  - root-cause-bug: instance_of
---
EOF

# Superseded file — should NOT be injected
cat > "$CLUSTER_MEM/feedback/deprecated-rule.md" << 'EOF'
---
type: feedback
project: global
status: superseded
referenced: 2026-03-01
domains: [general]
keywords: [timeout]
decay_rate: normal
ref_count: 0
---
Old deprecated rule.
EOF
```

- [ ] **Step 2: Коммит fixtures**

```bash
cd /Users/akamash/Development/hypomnema
git add hooks/test-memory-hooks.sh
git commit -m "test: add cluster/related fixtures for v0.4"
```

---

### Task 2: Test assertions for cluster activation

**Files:**
- Modify: `hooks/test-memory-hooks.sh` (в конец, перед "Results")

- [ ] **Step 1: Написать тесты для cluster loading**

Добавить перед секцией `# --- Results ---` (строка ~1403):

```bash
# === v0.4: Cluster activation tests ===
echo ""
echo "--- Cluster activation ---"

HOOK="$(dirname "$0")/session-start.sh"

# Test: related files are loaded via cluster pass
CLUSTER_OUT=$(echo '{"session_id":"cluster-test-1","cwd":"/tmp/cluster-test"}' | CLAUDE_MEMORY_DIR="$CLUSTER_MEM" bash "$HOOK" 2>/dev/null)
CLUSTER_CTX=$(echo "$CLUSTER_OUT" | jq -r '.hookSpecificOutput.additionalContext // empty' 2>/dev/null)

# Mistake should be injected by main pass (high recurrence, keyword match)
assert "Cluster — mistake injected by main pass" 'echo "$CLUSTER_CTX" | grep -q "root-cause-bug"'

# Related feedback should be pulled by cluster pass (forward link)
assert "Cluster — forward related feedback loaded" 'echo "$CLUSTER_CTX" | grep -q "debug-feedback"'

# Related strategy should be pulled by cluster pass (forward link)
assert "Cluster — forward related strategy loaded" 'echo "$CLUSTER_CTX" | grep -q "debug-strategy"'

# Reverse-linked feedback should be pulled (it references an injected file)
assert "Cluster — reverse related feedback loaded" 'echo "$CLUSTER_CTX" | grep -q "reverse-linked"'

# Cluster section shows provenance (via source → type)
assert "Cluster — provenance shown" 'echo "$CLUSTER_CTX" | grep -q "via.*reinforces\|via.*instance_of"'

# WAL should have cluster-load events
assert "Cluster — WAL cluster-load events" 'grep -q "cluster-load|debug-feedback" "$CLUSTER_MEM/.wal"'

# Superseded file is NOT loaded
assert "Cluster — superseded file excluded" '! echo "$CLUSTER_CTX" | grep -q "deprecated-rule"'

# Test: contradicts pair formatting
# If old-rule is injected and has contradicts link to new-rule:
assert "Cluster — contradicts warning shown" 'echo "$CLUSTER_CTX" | grep -q "Конфликт\|contradicts"'
assert "Cluster — newer record marked priority" 'echo "$CLUSTER_CTX" | grep -q "ПРИОРИТЕТ"'

# Test: cluster cap (+4 max)
# Create 10 related files — only 4 should be loaded via cluster
CLUSTER_CAP_DIR=$(mktemp -d)
CLUSTER_CAP_MEM="$CLUSTER_CAP_DIR/memory"
mkdir -p "$CLUSTER_CAP_MEM"/{mistakes,feedback,strategies,knowledge,projects,continuity,decisions}
cat > "$CLUSTER_CAP_MEM/projects.json" << 'PJSON'
{"/tmp/cap-test": "cap-proj"}
PJSON

# Hub mistake with 8 related links
RELATED_YAML="related:"
for i in $(seq 1 8); do
  RELATED_YAML="${RELATED_YAML}
  - extra-fb-${i}: reinforces"
  cat > "$CLUSTER_CAP_MEM/feedback/extra-fb-${i}.md" << FBEOF
---
type: feedback
project: global
status: active
referenced: 2026-04-08
domains: [general]
keywords: [test]
decay_rate: normal
ref_count: 1
---
Extra feedback ${i}.
FBEOF
done

cat > "$CLUSTER_CAP_MEM/mistakes/hub-mistake.md" << HUBEOF
---
type: mistake
project: global
status: active
severity: major
recurrence: 5
injected: 2026-04-01
referenced: 2026-04-10
root-cause: "Hub mistake"
prevention: "Hub prevention"
domains: [general]
keywords: [test]
decay_rate: slow
ref_count: 10
${RELATED_YAML}
---
HUBEOF

CAP_OUT=$(echo '{"session_id":"cap-test","cwd":"/tmp/cap-test"}' | CLAUDE_MEMORY_DIR="$CLUSTER_CAP_MEM" bash "$HOOK" 2>/dev/null)
CAP_CTX=$(echo "$CAP_OUT" | jq -r '.hookSpecificOutput.additionalContext // empty' 2>/dev/null)
# Count cluster-loaded files in WAL
CAP_CLUSTER_COUNT=$(grep -c "cluster-load" "$CLUSTER_CAP_MEM/.wal" 2>/dev/null || echo 0)
assert "Cluster — cap limits to 4 max" '[ "$CAP_CLUSTER_COUNT" -le 4 ]'

rm -rf "$CLUSTER_CAP_DIR"

echo ""
echo "--- Cluster cleanup ---"
rm -rf "$CLUSTER_DIR"
```

- [ ] **Step 2: Запустить тесты, убедиться что cluster-тесты FAIL**

```bash
cd /Users/akamash/Development/hypomnema
bash hooks/test-memory-hooks.sh 2>&1 | grep -E "✗.*Cluster|Passed:"
```

Expected: все Cluster-тесты помечены ✗ (FAIL), остальные PASS.

- [ ] **Step 3: Коммит test assertions**

```bash
git add hooks/test-memory-hooks.sh
git commit -m "test: add cluster activation assertions (red)"
```

---

### Task 3: Parse `related` field from frontmatter

**Files:**
- Modify: `hooks/session-start.sh` (после AWK_SCORED ~строка 259, новая функция)

- [ ] **Step 1: Добавить функцию parse_related**

После блока `AWK_SCORED` (строка 259), перед `domain_matches()` (строка 262), добавить:

```bash
# --- Parse related links from a memory file ---
# Input: file path
# Output: space-separated "target:type" pairs (stdout)
# Example: "debugging-approach:reinforces css-child-first:instance_of"
parse_related() {
  local file="$1"
  [ -f "$file" ] || return 0
  awk '
    /^---$/ { fm_count++; if (fm_count >= 2) exit; next }
    fm_count == 1 && /^related:/ { in_rel = 1; next }
    fm_count == 1 && in_rel && /^  - / {
      line = $0
      sub(/^  - /, "", line)
      gsub(/: /, ":", line)
      gsub(/ /, "", line)
      printf "%s ", line
      next
    }
    fm_count == 1 && in_rel && !/^  - / { in_rel = 0 }
  ' "$file"
}
```

- [ ] **Step 2: Добавить функцию find_memory_file**

Сразу после `parse_related`:

```bash
# --- Find memory file by slug (basename without .md) ---
# Searches all memory subdirectories
# Output: full path or empty
find_memory_file() {
  local slug="$1"
  local f
  for dir in mistakes feedback strategies knowledge notes decisions; do
    f="$MEMORY_DIR/$dir/${slug}.md"
    [ -f "$f" ] && printf '%s' "$f" && return 0
  done
  return 1
}
```

- [ ] **Step 3: Коммит**

```bash
git add hooks/session-start.sh
git commit -m "feat: add parse_related and find_memory_file helpers"
```

---

### Task 4: Implement cluster activation pass

**Files:**
- Modify: `hooks/session-start.sh` (после collect_scored блоков ~строка 681, перед "Collect project overview" ~строка 683)

Это основная функция v0.4. Второй проход после scoring.

- [ ] **Step 1: Добавить константы и cluster pass функцию**

В начале файла (после `MAX_FILES=22`, строка 22), добавить:

```bash
MAX_CLUSTER=4
```

После строки 681 (`DECISIONS_MD="$_COLLECT_RESULT"`), перед строкой 683 (`# --- Collect project overview ---`), вставить:

```bash
# --- v0.4: Cluster activation pass ---
# Second pass: load related files not picked by main scoring
# Forward scan: related targets of injected files
# Reverse scan: files that reference injected files
# Cap: +MAX_CLUSTER files, score > 0 required, priority by link type

CLUSTER_MD=""
CLUSTER_COUNT=0
CONTRADICTS_WARNINGS=""

# Build set of already-injected slugs for fast lookup
declare -A INJECTED_SET
for f in "${INJECTED_FILES[@]}"; do
  local_slug=$(basename "$f" .md)
  INJECTED_SET["$local_slug"]=1
done

# Collect cluster candidates: [priority:slug:source:type:filepath]
CLUSTER_CANDIDATES=""

# Forward scan: for each injected file, find its related targets
for f in "${INJECTED_FILES[@]}"; do
  source_slug=$(basename "$f" .md)
  related_pairs=$(parse_related "$f")
  for pair in $related_pairs; do
    target="${pair%%:*}"
    link_type="${pair#*:}"
    [ -z "$target" ] && continue
    [ -n "${INJECTED_SET[$target]}" ] && continue  # already injected

    target_path=$(find_memory_file "$target")
    [ -z "$target_path" ] && continue

    # Check status: must be active or pinned
    target_status=$(awk '/^---$/{ fm++; next } fm==1 && /^status:/ { sub(/^status: */, ""); print; exit }' "$target_path")
    [ "$target_status" != "active" ] && [ "$target_status" != "pinned" ] && continue

    # Compute basic score: keyword_hits + WAL + TF-IDF (any > 0 qualifies)
    target_score=0
    target_slug=$(basename "$target_path" .md)
    # Keyword check
    if [ -n "$KEYWORDS" ]; then
      target_kw=$(awk '/^---$/{ fm++; next } fm==1 && /^keywords:/ { sub(/^keywords: *\[?/, ""); sub(/\].*/, ""); gsub(/, */, " "); print; exit }' "$target_path")
      if [ -n "$target_kw" ] && [ "$target_kw" != "_none_" ]; then
        for kw in $KEYWORDS; do
          case " $target_kw " in *" $kw "*) target_score=$((target_score + 3)) ;; esac
        done
      fi
    fi
    # WAL check
    case ";$WAL_SCORES;" in *";$target_slug:"*)
      wal_val=$(printf '%s' "$WAL_SCORES" | tr ';' '\n' | grep "^${target_slug}:" | head -1 | cut -d: -f2)
      [ -n "$wal_val" ] && [ "$wal_val" != "0" ] && [ "$wal_val" != "0.0" ] && target_score=$((target_score + 1))
    ;; esac
    # TF-IDF check
    case ";$TFIDF_SCORES;" in *";$target_slug:"*)
      target_score=$((target_score + 1))
    ;; esac

    [ "$target_score" -le 0 ] && continue

    # Priority: contradicts=0, reinforces=1, instance_of=2, supersedes=3
    case "$link_type" in
      contradicts)  prio=0 ;;
      reinforces)   prio=1 ;;
      instance_of)  prio=2 ;;
      supersedes)   prio=3 ;;
      *)            prio=9 ;;
    esac

    CLUSTER_CANDIDATES="${CLUSTER_CANDIDATES}${prio}:${target_slug}:${source_slug}:${link_type}:${target_path}
"
  done
done

# Reverse scan: find non-injected files that reference injected files
for dir in mistakes feedback strategies knowledge notes decisions; do
  [ -d "$MEMORY_DIR/$dir" ] || continue
  while IFS= read -r candidate_file; do
    [ -f "$candidate_file" ] || continue
    cand_slug=$(basename "$candidate_file" .md)
    [ -n "${INJECTED_SET[$cand_slug]}" ] && continue  # already injected

    cand_related=$(parse_related "$candidate_file")
    [ -z "$cand_related" ] && continue

    for pair in $cand_related; do
      target="${pair%%:*}"
      link_type="${pair#*:}"
      # Check if this file references an injected file
      [ -n "${INJECTED_SET[$target]}" ] || continue

      # Check status
      cand_status=$(awk '/^---$/{ fm++; next } fm==1 && /^status:/ { sub(/^status: */, ""); print; exit }' "$candidate_file")
      [ "$cand_status" != "active" ] && [ "$cand_status" != "pinned" ] && continue

      # Score check (same as forward scan)
      cand_score=0
      if [ -n "$KEYWORDS" ]; then
        cand_kw=$(awk '/^---$/{ fm++; next } fm==1 && /^keywords:/ { sub(/^keywords: *\[?/, ""); sub(/\].*/, ""); gsub(/, */, " "); print; exit }' "$candidate_file")
        if [ -n "$cand_kw" ] && [ "$cand_kw" != "_none_" ]; then
          for kw in $KEYWORDS; do
            case " $cand_kw " in *" $kw "*) cand_score=$((cand_score + 3)) ;; esac
          done
        fi
      fi
      case ";$WAL_SCORES;" in *";$cand_slug:"*)
        wal_val=$(printf '%s' "$WAL_SCORES" | tr ';' '\n' | grep "^${cand_slug}:" | head -1 | cut -d: -f2)
        [ -n "$wal_val" ] && [ "$wal_val" != "0" ] && [ "$wal_val" != "0.0" ] && cand_score=$((cand_score + 1))
      ;; esac
      case ";$TFIDF_SCORES;" in *";$cand_slug:"*)
        cand_score=$((cand_score + 1))
      ;; esac

      [ "$cand_score" -le 0 ] && continue

      case "$link_type" in
        contradicts)  prio=0 ;;
        reinforces)   prio=1 ;;
        instance_of)  prio=2 ;;
        supersedes)   prio=3 ;;
        *)            prio=9 ;;
      esac

      # For reverse: source is the candidate, target is the injected file
      CLUSTER_CANDIDATES="${CLUSTER_CANDIDATES}${prio}:${cand_slug}:${target}:${link_type}:${candidate_file}
"
      break  # one link is enough to qualify
    done
  done < <(find "$MEMORY_DIR/$dir" -name "*.md" -type f 2>/dev/null)
done

# Deduplicate, sort by priority, cap at MAX_CLUSTER
if [ -n "$CLUSTER_CANDIDATES" ]; then
  declare -A CLUSTER_SEEN
  while IFS=: read -r prio slug source link_type filepath; do
    [ -z "$slug" ] && continue
    [ -n "${CLUSTER_SEEN[$slug]}" ] && continue
    [ "$CLUSTER_COUNT" -ge "$MAX_CLUSTER" ] && break

    CLUSTER_SEEN["$slug"]=1
    CLUSTER_COUNT=$((CLUSTER_COUNT + 1))
    INJECTED_SET["$slug"]=1

    # Read body
    cl_body=$(sed '1,/^---$/d; 1,/^---$/d' "$filepath" 2>/dev/null | sed '/^$/d')
    # If empty body, try root-cause/prevention
    if [ -z "$(printf '%s' "$cl_body" | tr -d '[:space:]')" ]; then
      cl_rc=$(awk '/^---$/{ fm++; next } fm==1 && /^root-cause:/ { sub(/^root-cause: *"?/, ""); sub(/"$/, ""); print; exit }' "$filepath")
      cl_prev=$(awk '/^---$/{ fm++; next } fm==1 && /^prevention:/ { sub(/^prevention: *"?/, ""); sub(/"$/, ""); print; exit }' "$filepath")
      cl_body=""
      [ -n "$cl_rc" ] && cl_body="Root cause: ${cl_rc}"
      [ -n "$cl_prev" ] && cl_body="${cl_body}
Prevention: ${cl_prev}"
    fi

    CLUSTER_MD="${CLUSTER_MD}
### ${slug} (via ${source} → ${link_type})
${cl_body}
"
    INJECTED_FILES+=("$filepath")

    # Track contradicts pairs
    if [ "$link_type" = "contradicts" ]; then
      # Determine which is newer (by referenced date)
      src_ref=$(awk '/^---$/{ fm++; next } fm==1 && /^referenced:/ { sub(/^referenced: */, ""); print; exit }' "$(find_memory_file "$source" 2>/dev/null || echo /dev/null)")
      tgt_ref=$(awk '/^---$/{ fm++; next } fm==1 && /^referenced:/ { sub(/^referenced: */, ""); print; exit }' "$filepath")
      if [[ "$tgt_ref" > "$src_ref" ]]; then
        CONTRADICTS_WARNINGS="${CONTRADICTS_WARNINGS}
> **[ПРИОРИТЕТ]** ${slug} (newer: ${tgt_ref})
> ⚠ Конфликт: ${slug} contradicts ${source} — проверь актуальность
"
      else
        CONTRADICTS_WARNINGS="${CONTRADICTS_WARNINGS}
> **[ПРИОРИТЕТ]** ${source} (newer: ${src_ref})
> ⚠ Конфликт: ${source} contradicts ${slug} — проверь актуальность
"
      fi
    fi
  done < <(printf '%s' "$CLUSTER_CANDIDATES" | sort -t: -k1,1n | uniq)
fi
```

- [ ] **Step 2: Добавить cluster-load WAL logging**

В секции WAL logging (строка ~783, блок `for f in "${INJECTED_FILES[@]}"`), ПЕРЕД этим блоком, добавить отдельный WAL log для cluster-loaded files. Проще: после cluster pass, добавить:

```bash
# WAL: log cluster-loaded files
if [ "$CLUSTER_COUNT" -gt 0 ]; then
  for slug in "${!CLUSTER_SEEN[@]}"; do
    printf '%s|cluster-load|%s|%s\n' "$TODAY" "$slug" "$SAFE_SESSION_ID" >> "$WAL_FILE" 2>/dev/null || true
  done
fi
```

Вставить сразу после cluster pass блока, перед `# --- Collect project overview ---`.

- [ ] **Step 3: Добавить Related секцию в output builder**

В секции "Build output" (строка ~715), после блока NOTES (строка ~755), добавить:

```bash
if [ -n "$CLUSTER_MD" ]; then
  CONTEXT="${CONTEXT}
## Related
${CLUSTER_MD}"
  [ -n "$CONTRADICTS_WARNINGS" ] && CONTEXT="${CONTEXT}
${CONTRADICTS_WARNINGS}"
fi
```

- [ ] **Step 4: Запустить тесты**

```bash
cd /Users/akamash/Development/hypomnema
bash hooks/test-memory-hooks.sh 2>&1 | tail -30
```

Expected: новые Cluster-тесты PASS, остальные тоже PASS.

- [ ] **Step 5: Коммит**

```bash
git add hooks/session-start.sh
git commit -m "feat: cluster activation pass with forward/reverse scan, contradicts handling"
```

---

### Task 5: Cascade detection in memory-outcome.sh

**Files:**
- Modify: `hooks/memory-outcome.sh`

Расширить хук: при Write в любой memory файл — проверить, есть ли другие файлы с `instance_of: <обновлённый файл>`. Если да — записать cascade-review в WAL.

- [ ] **Step 1: Написать тесты для cascade detection**

В `test-memory-hooks.sh`, перед `# --- Results ---`, добавить:

```bash
# === v0.4: Cascade detection tests ===
echo ""
echo "--- Cascade detection ---"

CASCADE_DIR=$(mktemp -d)
CASCADE_MEM="$CASCADE_DIR/memory"
mkdir -p "$CASCADE_MEM"/{mistakes,feedback}

# Parent file being updated
cat > "$CASCADE_MEM/feedback/parent-rule.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2026-04-10
decay_rate: slow
ref_count: 5
---
Parent rule content.
EOF

# Child with instance_of link to parent
cat > "$CASCADE_MEM/mistakes/child-instance.md" << 'EOF'
---
type: mistake
project: global
status: active
severity: minor
recurrence: 1
referenced: 2026-04-09
root-cause: "Child specific case"
prevention: "See parent"
related:
  - parent-rule: instance_of
decay_rate: normal
ref_count: 2
---
EOF

# Simulate Write to parent-rule via memory-outcome.sh
OUTCOME_HOOK="$(dirname "$0")/memory-outcome.sh"
echo '{"session_id":"cascade-test","tool_input":{"file_path":"'"$CASCADE_MEM"'/feedback/parent-rule.md"}}' | \
  CLAUDE_MEMORY_DIR="$CASCADE_MEM" bash "$OUTCOME_HOOK" 2>/dev/null

assert "Cascade — cascade-review event in WAL" 'grep -q "cascade-review|child-instance|parent:parent-rule" "$CASCADE_MEM/.wal"'

# File WITHOUT instance_of — no cascade
cat > "$CASCADE_MEM/feedback/unrelated-fb.md" << 'EOF'
---
type: feedback
project: global
status: active
referenced: 2026-04-09
---
Unrelated feedback.
EOF

echo '{"session_id":"cascade-test-2","tool_input":{"file_path":"'"$CASCADE_MEM"'/feedback/unrelated-fb.md"}}' | \
  CLAUDE_MEMORY_DIR="$CASCADE_MEM" bash "$OUTCOME_HOOK" 2>/dev/null

CASCADE_COUNT=$(grep -c "cascade-review" "$CASCADE_MEM/.wal" 2>/dev/null || echo 0)
assert "Cascade — no false cascade for unrelated file" '[ "$CASCADE_COUNT" -eq 1 ]'

rm -rf "$CASCADE_DIR"
```

- [ ] **Step 2: Запустить тесты, убедиться cascade тесты FAIL**

```bash
bash hooks/test-memory-hooks.sh 2>&1 | grep -E "✗.*Cascade|Passed:"
```

- [ ] **Step 3: Реализовать cascade detection**

В `hooks/memory-outcome.sh`, расширить обработку. Текущий хук обрабатывает только `mistakes/*.md`. Нужно добавить cascade detection для ЛЮБОГО memory файла.

Заменить содержимое `memory-outcome.sh`:

```bash
#!/bin/bash
# PostToolUse hook — detect negative outcomes + cascade signals (v0.4)
# Triggered on Write|Edit to memory/**/*.md
# Graceful: any error → exit 0

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
WAL_FILE="$MEMORY_DIR/.wal"

INPUT=$(cat)
FILE_PATH=$(printf '%s\n' "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)
[ -z "$FILE_PATH" ] && exit 0

# Only process writes to memory/**/*.md
case "$FILE_PATH" in
  */memory/*.md|*/memory/*/*.md) ;;
  *) exit 0 ;;
esac

SESSION_ID=$(printf '%s\n' "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)
[ -z "$SESSION_ID" ] && exit 0
SAFE_SESSION_ID="${SESSION_ID//|/_}"
SAFE_SESSION_ID="${SAFE_SESSION_ID//\//_}"

FILENAME=$(basename "$FILE_PATH" .md)
TODAY=$(date +%Y-%m-%d)

# --- Negative outcome detection (mistakes only) ---
case "$FILE_PATH" in
  */mistakes/*.md)
    INJECTED_IN_SESSION=""
    if [ -f "$WAL_FILE" ]; then
      INJECTED_IN_SESSION=$(awk -F'|' -v sid="$SAFE_SESSION_ID" -v fname="$FILENAME" '
        $4 == sid && $2 == "inject" && $3 == fname { found=1; exit }
        END { print found+0 }
      ' "$WAL_FILE")
    fi

    if [ "$INJECTED_IN_SESSION" = "1" ]; then
      printf '%s|outcome-negative|%s|%s\n' "$TODAY" "$FILENAME" "$SAFE_SESSION_ID" >> "$WAL_FILE" 2>/dev/null
    else
      printf '%s|outcome-new|%s|%s\n' "$TODAY" "$FILENAME" "$SAFE_SESSION_ID" >> "$WAL_FILE" 2>/dev/null
    fi
    ;;
esac

# --- Cascade detection (v0.4) ---
# When a file is updated, check if other files have instance_of pointing to it
# If so, log cascade-review event to WAL

for dir in mistakes feedback strategies knowledge notes decisions; do
  [ -d "$MEMORY_DIR/$dir" ] || continue
  find "$MEMORY_DIR/$dir" -name "*.md" -type f 2>/dev/null | while IFS= read -r candidate; do
    [ "$candidate" = "$FILE_PATH" ] && continue
    # Quick grep: does file contain "instance_of" at all?
    grep -q "instance_of" "$candidate" 2>/dev/null || continue
    # Parse related field for instance_of links to this file
    awk -v target="$FILENAME" '
      /^---$/ { fm++; if (fm >= 2) exit; next }
      fm == 1 && /^related:/ { in_rel = 1; next }
      fm == 1 && in_rel && /^  - / {
        line = $0; sub(/^  - /, "", line)
        split(line, parts, ": *")
        if (parts[1] == target && parts[2] ~ /instance_of/) { found = 1; exit }
        next
      }
      fm == 1 && in_rel && !/^  - / { in_rel = 0 }
      END { exit !found }
    ' "$candidate" 2>/dev/null && {
      child_slug=$(basename "$candidate" .md)
      printf '%s|cascade-review|%s|parent:%s\n' "$TODAY" "$child_slug" "$FILENAME" >> "$WAL_FILE" 2>/dev/null
    }
  done
done

exit 0
```

- [ ] **Step 4: Запустить тесты**

```bash
bash hooks/test-memory-hooks.sh 2>&1 | grep -E "Cascade|Passed:"
```

Expected: cascade тесты PASS.

- [ ] **Step 5: Коммит**

```bash
git add hooks/memory-outcome.sh hooks/test-memory-hooks.sh
git commit -m "feat: cascade detection — log instance_of children on parent update"
```

---

### Task 6: Cascade-review display in session-start.sh

**Files:**
- Modify: `hooks/session-start.sh` (output builder section ~строка 715)

При injection, если файл имеет pending cascade-review в WAL (< 14 дней), добавить пометку.

- [ ] **Step 1: Написать тест**

В `test-memory-hooks.sh`, перед `# --- Results ---`:

```bash
# === v0.4: Cascade-review display tests ===
echo ""
echo "--- Cascade-review display ---"

CASDISP_DIR=$(mktemp -d)
CASDISP_MEM="$CASDISP_DIR/memory"
mkdir -p "$CASDISP_MEM"/{mistakes,feedback,projects,continuity,strategies,knowledge,decisions}

cat > "$CASDISP_MEM/projects.json" << 'PJSON'
{"/tmp/casdisp-test": "casdisp-proj"}
PJSON

# A mistake that has a pending cascade-review in WAL
cat > "$CASDISP_MEM/mistakes/reviewed-child.md" << 'EOF'
---
type: mistake
project: global
status: active
severity: major
recurrence: 3
injected: 2026-04-01
referenced: 2026-04-10
root-cause: "Child that needs review"
prevention: "Check parent"
domains: [general]
keywords: [test]
decay_rate: slow
ref_count: 5
---
EOF

# Pre-populate WAL with cascade-review event
printf '%s|cascade-review|reviewed-child|parent:some-parent\n' "$(date +%Y-%m-%d)" > "$CASDISP_MEM/.wal"

CASDISP_OUT=$(echo '{"session_id":"casdisp-test","cwd":"/tmp/casdisp-test"}' | CLAUDE_MEMORY_DIR="$CASDISP_MEM" bash "$HOOK" 2>/dev/null)
CASDISP_CTX=$(echo "$CASDISP_OUT" | jq -r '.hookSpecificOutput.additionalContext // empty' 2>/dev/null)

assert "Cascade display — REVIEW marker shown" 'echo "$CASDISP_CTX" | grep -q "REVIEW.*parent.*updated\|REVIEW.*some-parent"'

rm -rf "$CASDISP_DIR"
```

- [ ] **Step 2: Реализовать cascade-review display**

В `session-start.sh`, перед `# --- Build output ---` (строка ~715), добавить:

```bash
# --- v0.4: Load pending cascade-review events from WAL ---
# Events younger than 14 days
declare -A CASCADE_REVIEWS
if [ -f "$WAL_FILE" ]; then
  if [[ "$OSTYPE" == darwin* ]]; then
    CASCADE_CUTOFF=$(date -v-14d +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
  else
    CASCADE_CUTOFF=$(date -d "14 days ago" +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
  fi
  while IFS='|' read -r cdate ctype cchild cparent; do
    [ "$ctype" = "cascade-review" ] || continue
    [[ "$cdate" < "$CASCADE_CUTOFF" ]] && continue
    parent_name="${cparent#parent:}"
    CASCADE_REVIEWS["$cchild"]="$parent_name updated $cdate"
  done < "$WAL_FILE"
fi
```

Затем в output builder, при формировании markdown для mistakes и scored types, нужно проверять `CASCADE_REVIEWS`. Проще всего — добавить пост-обработку CONTEXT после его формирования.

После финальной сборки CONTEXT (строка ~755), перед `[ -z "$CONTEXT" ] && exit 0`:

```bash
# Inject cascade-review markers into CONTEXT
if [ ${#CASCADE_REVIEWS[@]} -gt 0 ]; then
  for slug in "${!CASCADE_REVIEWS[@]}"; do
    review_info="${CASCADE_REVIEWS[$slug]}"
    # Add marker after the ### heading line for this slug
    CONTEXT=$(printf '%s' "$CONTEXT" | sed "s/^### ${slug}/### ${slug} [REVIEW: ${review_info}]/")
  done
fi
```

- [ ] **Step 3: Запустить тесты**

```bash
bash hooks/test-memory-hooks.sh 2>&1 | grep -E "Cascade display|Passed:"
```

Expected: PASS.

- [ ] **Step 4: Коммит**

```bash
git add hooks/session-start.sh hooks/test-memory-hooks.sh
git commit -m "feat: cascade-review display markers from WAL events"
```

---

### Task 7: Add initial related links to memory files

**Files:**
- Modify: 5 files in `~/.claude/memory/`

Проставить связи согласно спецификации.

- [ ] **Step 1: wrong-root-cause-diagnosis → debugging-approach: reinforces**

В файле `~/.claude/memory/mistakes/wrong-root-cause-diagnosis.md`, добавить в frontmatter перед закрывающим `---`:

```yaml
related:
  - debugging-approach: reinforces
```

- [ ] **Step 2: css-child-first-debugging → wrong-root-cause-diagnosis: instance_of, css-layout-debugging: reinforces**

В файле `~/.claude/memory/mistakes/css-child-first-debugging.md`:

```yaml
related:
  - wrong-root-cause-diagnosis: instance_of
  - css-layout-debugging: reinforces
```

- [ ] **Step 3: jira-deprecated-api-endpoints → jira-cloud-delete-user-misleading: reinforces**

В файле `~/.claude/memory/mistakes/jira-deprecated-api-endpoints.md`:

```yaml
related:
  - jira-cloud-delete-user-misleading: reinforces
```

- [ ] **Step 4: docker-stale-next-cache → code-approach: instance_of**

В файле `~/.claude/memory/mistakes/docker-stale-next-cache.md`:

```yaml
related:
  - code-approach: instance_of
```

- [ ] **Step 5: Проверить parse_related на живых файлах**

```bash
source /Users/akamash/Development/hypomnema/hooks/session-start.sh <<< '{}' 2>/dev/null || true
# Или напрямую:
awk '
  /^---$/ { fm++; if (fm >= 2) exit; next }
  fm == 1 && /^related:/ { in_rel = 1; next }
  fm == 1 && in_rel && /^  - / {
    line = $0; sub(/^  - /, "", line); gsub(/: /, ":", line); gsub(/ /, "", line)
    printf "%s ", line; next
  }
  fm == 1 && in_rel && !/^  - / { in_rel = 0 }
' ~/.claude/memory/mistakes/css-child-first-debugging.md
```

Expected output: `wrong-root-cause-diagnosis:instance_of css-layout-debugging:reinforces`

- [ ] **Step 6: Коммит**

```bash
cd /Users/akamash/Development/hypomnema
git add -A
git commit -m "data: add initial related links to 5 memory files"
```

---

### Task 8: Full test suite + benchmark

**Files:**
- Run: `hooks/test-memory-hooks.sh`, `hooks/bench-memory.sh`

- [ ] **Step 1: Запустить полный тест-сьют**

```bash
cd /Users/akamash/Development/hypomnema
bash hooks/test-memory-hooks.sh
```

Expected: ALL TESTS PASSED (включая новые Cluster, Cascade, Cascade display тесты).

- [ ] **Step 2: Запустить бенчмарк**

```bash
bash hooks/bench-memory.sh
```

Expected: session-start.sh завершается за < 500ms (с учётом cluster pass).

- [ ] **Step 3: Тест на живых данных**

```bash
echo '{"session_id":"v04-live-test","cwd":"/Users/akamash"}' | \
  bash ~/.claude/hooks/memory-session-start.sh 2>/dev/null | \
  jq -r '.hookSpecificOutput.additionalContext' | head -60
```

Проверить: есть ли секция `## Related` с cluster-загруженными файлами.

- [ ] **Step 4: Проверить WAL на cluster-load события**

```bash
grep "cluster-load" ~/.claude/memory/.wal | tail -5
```

- [ ] **Step 5: Обновить continuity**

Обновить `~/.claude/memory/continuity/hypomnema.md`:

```markdown
**Задача:** Memory system evolution v0.3→v1.0. v0.4 реализована.
**Статус:** v0.4 done — related links, cluster activation, contradicts, cascade signals.
**Следующий шаг:** Рестарт сессии → проверить cluster injection вживую → v0.5 (контекстная активация).
```

- [ ] **Step 6: Финальный коммит**

```bash
git add -A
git commit -m "v0.4: related links, cluster activation, contradicts handling, cascade signals"
```
