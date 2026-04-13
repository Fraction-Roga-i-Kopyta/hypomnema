# Memory Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a standalone bash TUI (`memory-dashboard.sh`) with two tabs (Overview, Ops) for read-only observation and manual operations on `~/.claude/memory/`.

**Architecture:** Single bash script with modular functions, sourceable in test mode (`SOURCE_ONLY=1`) to enable function-level testing without invoking the main loop. Pure bash, no external deps. ANSI escape codes for layout, alt-screen for clean exit.

**Tech Stack:** bash 3.2+ (macOS-compatible), `tput`, `awk`, `find`, `stat`. No frameworks.

**Spec:** `~/.claude/docs/specs/2026-04-13-memory-dashboard-design.md`

---

## File Structure

| File | Responsibility |
|------|----------------|
| `~/Development/hypomnema-memdash/bin/memory-dashboard.sh` | Main script: parsers, renderers, main loop. Sources nothing. ~400 lines. |
| `~/Development/hypomnema-memdash/bin/tests/test-memory-dashboard.sh` | Test runner: sources main script with `SOURCE_ONLY=1`, asserts on synthetic fixtures. |
| `~/Development/hypomnema-memdash/bin/tests/fixtures/memory/` | Synthetic memory tree (mistakes/, notes/, .wal/) used by tests. |

After implementation: symlink `~/.claude/bin/memory-dash` → `memory-dashboard.sh` for short alias.

---

## Test Harness Convention

Every parser/aggregator function is exercised by `tests/test-memory-dashboard.sh`. Pattern:

```bash
#!/bin/bash
set -e
export SOURCE_ONLY=1
source ~/Development/hypomnema-memdash/bin/memory-dashboard.sh

PASS=0; FAIL=0
assert_eq() {
    local exp=$1 act=$2 name=$3
    if [[ "$exp" == "$act" ]]; then
        PASS=$((PASS+1)); echo "  ok  $name"
    else
        FAIL=$((FAIL+1)); echo "  FAIL $name: expected '$exp', got '$act'"
    fi
}

FIX="$(dirname "$0")/fixtures/memory"
# ... call functions, assert results ...

echo "$PASS passed, $FAIL failed"
[[ $FAIL -eq 0 ]]
```

Main script must guard the entry point:
```bash
[[ "${SOURCE_ONLY:-0}" == "1" ]] && return 0
main "$@"
```

---

## Task 1: Skeleton + test harness

**Files:**
- Create: `~/Development/hypomnema-memdash/bin/memory-dashboard.sh`
- Create: `~/Development/hypomnema-memdash/bin/tests/test-memory-dashboard.sh`
- Create: `~/Development/hypomnema-memdash/bin/tests/fixtures/memory/.gitkeep`

- [ ] **Step 1: Create empty fixtures dir**

```bash
mkdir -p ~/Development/hypomnema-memdash/bin/tests/fixtures/memory
touch ~/Development/hypomnema-memdash/bin/tests/fixtures/memory/.gitkeep
```

- [ ] **Step 2: Create main script skeleton**

Write `~/Development/hypomnema-memdash/bin/memory-dashboard.sh`:

```bash
#!/bin/bash
# memory-dashboard.sh — TUI for ~/.claude/memory observation and ops
# Spec: ~/.claude/docs/specs/2026-04-13-memory-dashboard-design.md

set -u

MEMORY_DIR="${MEMORY_DIR:-$HOME/.claude/memory}"
BIN_DIR="${BIN_DIR:-$HOME/.claude/bin}"
HOOKS_DIR="${HOOKS_DIR:-$HOME/.claude/hooks}"

main() {
    echo "memory-dashboard: not implemented yet"
}

[[ "${SOURCE_ONLY:-0}" == "1" ]] && return 0
main "$@"
```

- [ ] **Step 3: Make executable**

```bash
chmod +x ~/Development/hypomnema-memdash/bin/memory-dashboard.sh
```

- [ ] **Step 4: Create test harness**

Write `~/Development/hypomnema-memdash/bin/tests/test-memory-dashboard.sh`:

```bash
#!/bin/bash
set -u
export SOURCE_ONLY=1
source ~/Development/hypomnema-memdash/bin/memory-dashboard.sh

PASS=0; FAIL=0

assert_eq() {
    local exp=$1 act=$2 name=$3
    if [[ "$exp" == "$act" ]]; then
        PASS=$((PASS+1)); echo "  ok  $name"
    else
        FAIL=$((FAIL+1)); echo "  FAIL $name: expected '$exp', got '$act'"
    fi
}

FIX="$(dirname "$0")/fixtures/memory"

# --- tests appended in subsequent tasks ---

echo
echo "$PASS passed, $FAIL failed"
[[ $FAIL -eq 0 ]]
```

- [ ] **Step 5: Run harness, expect clean pass with 0 tests**

```bash
chmod +x ~/Development/hypomnema-memdash/bin/tests/test-memory-dashboard.sh
bash ~/Development/hypomnema-memdash/bin/tests/test-memory-dashboard.sh
```

Expected output:
```
0 passed, 0 failed
```
Exit code 0.

- [ ] **Step 6: Verify direct invocation works**

```bash
~/Development/hypomnema-memdash/bin/memory-dashboard.sh
```
Expected: `memory-dashboard: not implemented yet`

- [ ] **Step 7: Commit**

```bash
cd ~/Development/hypomnema-memdash && git add bin/memory-dashboard.sh bin/tests/
git commit -m "feat(memory-dash): skeleton + test harness"
```

---

## Task 2: parse_frontmatter_field()

**Files:**
- Modify: `~/Development/hypomnema-memdash/bin/memory-dashboard.sh`
- Modify: `~/Development/hypomnema-memdash/bin/tests/test-memory-dashboard.sh`
- Create: `~/Development/hypomnema-memdash/bin/tests/fixtures/memory/mistakes/sample.md`

- [ ] **Step 1: Create fixture**

`~/Development/hypomnema-memdash/bin/tests/fixtures/memory/mistakes/sample.md`:

```markdown
---
type: mistake
recurrence: 5
severity: major
created: 2026-04-05
ref_count: 8
---
body text
```

- [ ] **Step 2: Add failing test**

Append to `tests/test-memory-dashboard.sh` before the summary lines:

```bash
# parse_frontmatter_field
assert_eq "5"      "$(parse_frontmatter_field "$FIX/mistakes/sample.md" recurrence)" "fm/recurrence"
assert_eq "major"  "$(parse_frontmatter_field "$FIX/mistakes/sample.md" severity)"   "fm/severity"
assert_eq ""       "$(parse_frontmatter_field "$FIX/mistakes/sample.md" missing)"    "fm/missing"
```

- [ ] **Step 3: Run, expect FAIL**

```bash
bash ~/Development/hypomnema-memdash/bin/tests/test-memory-dashboard.sh
```
Expected: 3 FAIL ("function not defined" or empty output).

- [ ] **Step 4: Implement**

Add to `memory-dashboard.sh` above `main()`:

```bash
# parse_frontmatter_field FILE FIELD
# Extracts a top-level YAML field from frontmatter (between --- markers).
# Prints empty string if not found. Strips surrounding quotes from value.
parse_frontmatter_field() {
    local file=$1 field=$2
    awk -v f="$field" '
        BEGIN { in_fm = 0; depth = 0 }
        /^---[[:space:]]*$/ { depth++; in_fm = (depth == 1); next }
        depth >= 2 { exit }
        in_fm {
            if (match($0, "^" f ":[[:space:]]*")) {
                v = substr($0, RLENGTH+1)
                gsub(/^["'\'']|["'\'']$/, "", v)
                gsub(/[[:space:]]+$/, "", v)
                print v
                exit
            }
        }
    ' "$file" 2>/dev/null
}
```

- [ ] **Step 5: Run, expect PASS**

```bash
bash ~/Development/hypomnema-memdash/bin/tests/test-memory-dashboard.sh
```
Expected: `3 passed, 0 failed`.

- [ ] **Step 6: Commit**

```bash
cd ~/Development/hypomnema-memdash && git add bin/memory-dashboard.sh bin/tests/
git commit -m "feat(memory-dash): parse_frontmatter_field"
```

---

## Task 3: count_notes_by_category() + format_age()

**Files:**
- Modify: `~/Development/hypomnema-memdash/bin/memory-dashboard.sh`
- Modify: `~/Development/hypomnema-memdash/bin/tests/test-memory-dashboard.sh`
- Create: `~/Development/hypomnema-memdash/bin/tests/fixtures/memory/notes/n1.md`, `notes/n2.md`, `projects/p1.md`

- [ ] **Step 1: Create fixtures**

```bash
echo "stub" > ~/Development/hypomnema-memdash/bin/tests/fixtures/memory/notes/n1.md
echo "stub" > ~/Development/hypomnema-memdash/bin/tests/fixtures/memory/notes/n2.md
mkdir -p ~/Development/hypomnema-memdash/bin/tests/fixtures/memory/projects
echo "stub" > ~/Development/hypomnema-memdash/bin/tests/fixtures/memory/projects/p1.md
```

- [ ] **Step 2: Add failing tests**

Append to `tests/test-memory-dashboard.sh`:

```bash
# count_notes_by_category
assert_eq "1" "$(count_notes_by_category "$FIX" mistakes)"  "count/mistakes"
assert_eq "2" "$(count_notes_by_category "$FIX" notes)"     "count/notes"
assert_eq "1" "$(count_notes_by_category "$FIX" projects)"  "count/projects"
assert_eq "0" "$(count_notes_by_category "$FIX" missing)"   "count/missing"
assert_eq "4" "$(count_notes_total "$FIX")"                  "count/total"

# format_age
assert_eq "just now" "$(format_age 0)"     "age/0"
assert_eq "5m ago"   "$(format_age 300)"   "age/5m"
assert_eq "2h ago"   "$(format_age 7200)"  "age/2h"
assert_eq "3d ago"   "$(format_age 259200)" "age/3d"
```

- [ ] **Step 3: Run, expect FAIL**

- [ ] **Step 4: Implement**

Add to `memory-dashboard.sh`:

```bash
# Categories scanned for note counts
MEMORY_CATEGORIES=(mistakes notes projects feedback knowledge continuity strategies seeds decisions journal)

count_notes_by_category() {
    local root=$1 cat=$2
    local dir="$root/$cat"
    [[ -d "$dir" ]] || { echo 0; return; }
    find "$dir" -maxdepth 1 -name "*.md" -type f 2>/dev/null | wc -l | awk '{print $1}'
}

count_notes_total() {
    local root=$1 total=0
    for cat in "${MEMORY_CATEGORIES[@]}"; do
        total=$((total + $(count_notes_by_category "$root" "$cat")))
    done
    echo "$total"
}

format_age() {
    local secs=$1
    if   (( secs < 60 ));     then echo "just now"
    elif (( secs < 3600 ));   then echo "$((secs / 60))m ago"
    elif (( secs < 86400 ));  then echo "$((secs / 3600))h ago"
    else                           echo "$((secs / 86400))d ago"
    fi
}
```

- [ ] **Step 5: Run, expect PASS** (9 assertions total)

- [ ] **Step 6: Commit**

```bash
cd ~/Development/hypomnema-memdash && git add bin/memory-dashboard.sh bin/tests/
git commit -m "feat(memory-dash): note counts + age formatter"
```

---

## Task 4: WAL parsing — wal_events_in_window()

**Files:**
- Modify: `~/Development/hypomnema-memdash/bin/memory-dashboard.sh`
- Modify: `~/Development/hypomnema-memdash/bin/tests/test-memory-dashboard.sh`
- Create: `~/Development/hypomnema-memdash/bin/tests/fixtures/memory/.wal/2026-04-13.jsonl`

- [ ] **Step 1: Create WAL fixture**

```bash
mkdir -p ~/Development/hypomnema-memdash/bin/tests/fixtures/memory/.wal
cat > ~/Development/hypomnema-memdash/bin/tests/fixtures/memory/.wal/2026-04-13.jsonl <<'EOF'
{"ts":"2026-04-13T21:30:00Z","event":"trigger","note":"wrong-root-cause","trigger":"debug"}
{"ts":"2026-04-13T21:31:00Z","event":"create","note":"foo","category":"notes"}
{"ts":"2026-04-13T21:32:00Z","event":"trigger","note":"wrong-root-cause","trigger":"hypothesis"}
{"ts":"2026-04-13T21:33:00Z","event":"trigger","note":"code-approach","trigger":"sql"}
{"ts":"2026-04-13T21:34:00Z","event":"reference","note":"akasha","category":"projects"}
EOF
```

- [ ] **Step 2: Add failing tests**

```bash
# wal_events_in_window
mapfile -t lines < <(wal_events_in_window "$FIX/.wal" 7) || true
assert_eq "5" "${#lines[@]}" "wal/count_in_7d"

# wal_count_event
assert_eq "3" "$(wal_count_event "$FIX/.wal" 7 trigger)"   "wal/triggers"
assert_eq "1" "$(wal_count_event "$FIX/.wal" 7 create)"    "wal/creates"
assert_eq "1" "$(wal_count_event "$FIX/.wal" 7 reference)" "wal/refs"
```

> **Note:** macOS bash 3.2 lacks `mapfile`. If the runner errors, use the fallback:
> ```bash
> lines=()
> while IFS= read -r line; do lines+=("$line"); done < <(wal_events_in_window "$FIX/.wal" 7)
> ```

- [ ] **Step 3: Run, expect FAIL**

- [ ] **Step 4: Implement**

Add to `memory-dashboard.sh`:

```bash
# wal_events_in_window WAL_DIR DAYS
# Prints all JSONL lines from .wal/ files modified within last DAYS days.
wal_events_in_window() {
    local wal_dir=$1 days=$2
    [[ -d "$wal_dir" ]] || return 0
    # Use `find` with -mtime; on macOS -mtime accepts negative for "newer than"
    find "$wal_dir" -name "*.jsonl" -type f -mtime -"$days" 2>/dev/null \
        | sort \
        | xargs -I{} cat {} 2>/dev/null
}

# wal_count_event WAL_DIR DAYS EVENT_TYPE
wal_count_event() {
    local wal_dir=$1 days=$2 evt=$3
    wal_events_in_window "$wal_dir" "$days" \
        | grep -c "\"event\":\"$evt\"" 2>/dev/null \
        | awk '{print $1+0}'
}
```

- [ ] **Step 5: Run, expect PASS**

- [ ] **Step 6: Commit**

```bash
cd ~/Development/hypomnema-memdash && git add bin/memory-dashboard.sh bin/tests/
git commit -m "feat(memory-dash): WAL window + event counter"
```

---

## Task 5: Aggregators — top_triggered, recurring, recently_created, stale, active_context

**Files:**
- Modify: `~/Development/hypomnema-memdash/bin/memory-dashboard.sh`
- Modify: `~/Development/hypomnema-memdash/bin/tests/test-memory-dashboard.sh`
- Create: `~/Development/hypomnema-memdash/bin/tests/fixtures/memory/_agent_context.md`

- [ ] **Step 1: Create context fixture**

```bash
cat > ~/Development/hypomnema-memdash/bin/tests/fixtures/memory/_agent_context.md <<'EOF'
# Memory Context

**Active project:** akasha
**Active domains:** frontend, debugging
EOF
```

- [ ] **Step 2: Enrich note fixtures with frontmatter**

Replace `notes/n1.md` and `notes/n2.md`:

```bash
cat > ~/Development/hypomnema-memdash/bin/tests/fixtures/memory/notes/n1.md <<'EOF'
---
type: note
created: 2026-04-13
referenced: 2026-04-13
ref_count: 5
---
recent
EOF

cat > ~/Development/hypomnema-memdash/bin/tests/fixtures/memory/notes/n2.md <<'EOF'
---
type: note
created: 2025-01-01
referenced: 2025-01-01
ref_count: 0
---
stale
EOF
```

- [ ] **Step 3: Add failing tests**

```bash
# top_triggered: format "<note>\t<count>", sorted desc, limit N
result=$(top_triggered "$FIX/.wal" 7 5)
assert_eq "wrong-root-cause	2" "$(echo "$result" | head -1)" "top/first"
assert_eq "code-approach	1"    "$(echo "$result" | sed -n 2p)" "top/second"

# recurring_mistakes: prints "<name>\t<recurrence>" for recurrence >= 2
rec=$(recurring_mistakes "$FIX")
assert_eq "sample	5" "$(echo "$rec" | head -1)" "recurring/first"

# recently_created: prints "<name>\t<created>" sorted desc, limit N
rc=$(recently_created "$FIX" 10)
assert_eq "n1" "$(echo "$rc" | head -1 | cut -f1)" "recent/first"

# stale_notes: ref_count == 0 AND referenced older than 90d (relative to today 2026-04-13)
st=$(stale_notes "$FIX" 90)
assert_eq "n2" "$(echo "$st" | head -1 | cut -f1)" "stale/first"

# active_context: parse _agent_context.md
assert_eq "akasha"             "$(active_project "$FIX")" "active/project"
assert_eq "frontend, debugging" "$(active_domains "$FIX")" "active/domains"
```

- [ ] **Step 4: Run, expect FAIL**

- [ ] **Step 5: Implement**

Add to `memory-dashboard.sh`:

```bash
# top_triggered WAL_DIR DAYS LIMIT
top_triggered() {
    local wal_dir=$1 days=$2 limit=$3
    wal_events_in_window "$wal_dir" "$days" \
        | grep '"event":"trigger"' \
        | sed -E 's/.*"note":"([^"]+)".*/\1/' \
        | sort | uniq -c | sort -rn \
        | awk -v lim="$limit" 'NR<=lim {printf "%s\t%d\n", $2, $1}'
}

# recurring_mistakes ROOT
recurring_mistakes() {
    local root=$1
    local dir="$root/mistakes"
    [[ -d "$dir" ]] || return 0
    for f in "$dir"/*.md; do
        [[ -f "$f" ]] || continue
        local rec; rec=$(parse_frontmatter_field "$f" recurrence)
        [[ -n "$rec" && "$rec" -ge 2 ]] 2>/dev/null || continue
        printf "%s\t%s\n" "$(basename "$f" .md)" "$rec"
    done | sort -t$'\t' -k2 -rn
}

# recently_created ROOT LIMIT
recently_created() {
    local root=$1 limit=$2
    for cat in "${MEMORY_CATEGORIES[@]}"; do
        local d="$root/$cat"
        [[ -d "$d" ]] || continue
        for f in "$d"/*.md; do
            [[ -f "$f" ]] || continue
            local cr; cr=$(parse_frontmatter_field "$f" created)
            [[ -n "$cr" ]] || continue
            printf "%s\t%s\n" "$(basename "$f" .md)" "$cr"
        done
    done | sort -t$'\t' -k2 -r | head -n "$limit"
}

# stale_notes ROOT DAYS_THRESHOLD
# A note is stale if ref_count == 0 AND referenced is older than DAYS_THRESHOLD.
stale_notes() {
    local root=$1 threshold=$2
    local today_epoch; today_epoch=$(date +%s)
    for cat in "${MEMORY_CATEGORIES[@]}"; do
        local d="$root/$cat"
        [[ -d "$d" ]] || continue
        for f in "$d"/*.md; do
            [[ -f "$f" ]] || continue
            local rc; rc=$(parse_frontmatter_field "$f" ref_count)
            [[ "$rc" == "0" ]] || continue
            local ref; ref=$(parse_frontmatter_field "$f" referenced)
            [[ -n "$ref" ]] || continue
            local ref_epoch
            ref_epoch=$(date -j -f "%Y-%m-%d" "$ref" +%s 2>/dev/null) \
                || ref_epoch=$(date -d "$ref" +%s 2>/dev/null) \
                || continue
            local age_days=$(( (today_epoch - ref_epoch) / 86400 ))
            (( age_days > threshold )) || continue
            printf "%s\t%dd\n" "$(basename "$f" .md)" "$age_days"
        done
    done
}

active_project() {
    local f="$1/_agent_context.md"
    [[ -f "$f" ]] || { echo "-"; return; }
    awk -F': ' '/\*\*Active project:\*\*/ {gsub(/\*\*/, "", $2); print $2; exit}' "$f"
}

active_domains() {
    local f="$1/_agent_context.md"
    [[ -f "$f" ]] || { echo "-"; return; }
    awk -F': ' '/\*\*Active domains:\*\*/ {gsub(/\*\*/, "", $2); print $2; exit}' "$f"
}
```

> **`date` portability:** macOS uses `date -j -f`, Linux uses `date -d`. The `||` fallback covers both.

- [ ] **Step 6: Run, expect PASS**

- [ ] **Step 7: Commit**

```bash
cd ~/Development/hypomnema-memdash && git add bin/memory-dashboard.sh bin/tests/
git commit -m "feat(memory-dash): aggregators (top, recurring, recent, stale, context)"
```

---

## Task 6: Frame + tab header rendering

**Files:**
- Modify: `~/Development/hypomnema-memdash/bin/memory-dashboard.sh`
- Modify: `~/Development/hypomnema-memdash/bin/tests/test-memory-dashboard.sh`

Layout: terminal width fixed to 80 cols (validated at startup). Inner content area = 78 cols. Two columns of 37 each + 4-char gap.

- [ ] **Step 1: Add failing tests**

```bash
# pad: pad string to width with spaces (right padding)
assert_eq "ab    " "$(pad "ab" 6)" "pad/right"
# truncate: cut string to width, no ellipsis
assert_eq "abcd" "$(truncate "abcdefgh" 4)" "truncate/cut"
assert_eq "ab"   "$(truncate "ab" 4)" "truncate/short"

# render_two_col: takes left+right strings, returns single line "| LEFT_37  RIGHT_37 |"
line=$(render_two_col "left" "right")
# Total width 80
assert_eq "80" "${#line}" "two_col/width"
```

- [ ] **Step 2: Run, expect FAIL**

- [ ] **Step 3: Implement**

Add to `memory-dashboard.sh`:

```bash
TUI_WIDTH=80
COL_WIDTH=37   # 1 (border) + 37 + 2 (gap) + 37 + 1 (border) = 78. Add 2 spaces padding inside borders.

pad() {
    local s=$1 w=$2
    printf "%-${w}s" "$s"
}

truncate() {
    local s=$1 w=$2
    if (( ${#s} > w )); then
        printf "%s" "${s:0:w}"
    else
        printf "%s" "$s"
    fi
}

# render_two_col LEFT RIGHT
# Produces: "| <LEFT_37> <space×2> <RIGHT_37> |"  (total 80 chars)
render_two_col() {
    local left right
    left=$(pad "$(truncate "$1" $COL_WIDTH)" $COL_WIDTH)
    right=$(pad "$(truncate "$2" $COL_WIDTH)" $COL_WIDTH)
    printf "|%s  %s|" "$left" "$right"
}

# draw_top BAR_TITLE
# "+-- TITLE -- ... -- TIMESTAMP --+"  total 80
draw_top() {
    local title=$1 ts=$2
    local body=" $title "
    local right=" $ts "
    local fill=$(( TUI_WIDTH - 2 - ${#body} - ${#right} - 4 ))
    printf "+--%s%s%s--+\n" "$body" "$(printf -- '-%.0s' $(seq 1 $fill))" "$right"
}

draw_sep() {
    printf "+%s+\n" "$(printf -- '-%.0s' $(seq 1 $((TUI_WIDTH - 2))))"
}

draw_bottom() {
    local hint=$1
    local body=" $hint "
    local fill=$(( TUI_WIDTH - 2 - ${#body} - 2 ))
    printf "+--%s%s+\n" "$body" "$(printf -- '-%.0s' $(seq 1 $fill))"
}

# draw_tab_header ACTIVE_TAB ("overview" or "ops")
draw_tab_header() {
    local active=$1 left=""
    if [[ "$active" == "overview" ]]; then
        left="[ Overview ]  Ops"
    else
        left="  Overview  [ Ops ]"
    fi
    render_two_col "$left" "                              q: exit"
    echo
}
```

- [ ] **Step 4: Run, expect PASS**

- [ ] **Step 5: Visual sanity check**

```bash
SOURCE_ONLY=1 source ~/Development/hypomnema-memdash/bin/memory-dashboard.sh
draw_top "Memory Dashboard" "2026-04-13 21:32"
draw_tab_header overview
render_two_col "Storage" "Top Triggered (7d)"
draw_bottom "[Tab] switch  [r] refresh  [q] quit"
```

Expected: clean 80-wide ASCII frame, columns aligned.

- [ ] **Step 6: Commit**

```bash
cd ~/Development/hypomnema-memdash && git add bin/memory-dashboard.sh bin/tests/
git commit -m "feat(memory-dash): frame + tab header + two-col rendering"
```

---

## Task 7: Render Overview tab

**Files:**
- Modify: `~/Development/hypomnema-memdash/bin/memory-dashboard.sh`

- [ ] **Step 1: Implement render_overview()**

Add to `memory-dashboard.sh`:

```bash
# render_overview ROOT
# Composes the Overview tab content (between header and footer).
render_overview() {
    local root=$1

    # Gather data
    local total; total=$(count_notes_total "$root")
    local mist; mist=$(count_notes_by_category "$root" mistakes)
    local note; note=$(count_notes_by_category "$root" notes)
    local proj; proj=$(count_notes_by_category "$root" projects)
    local fb;   fb=$(count_notes_by_category "$root" feedback)
    local kn;   kn=$(count_notes_by_category "$root" knowledge)
    local cont; cont=$(count_notes_by_category "$root" continuity)
    local strat; strat=$(count_notes_by_category "$root" strategies)
    local seed; seed=$(count_notes_by_category "$root" seeds)

    local wal_lines=0 wal_size="-"
    if [[ -d "$root/.wal" ]]; then
        wal_lines=$(find "$root/.wal" -name "*.jsonl" -exec cat {} \; 2>/dev/null | wc -l | awk '{print $1}')
        wal_size=$(du -sh "$root/.wal" 2>/dev/null | awk '{print $1}')
    fi

    local proj_act dom_act
    proj_act=$(active_project "$root")
    dom_act=$(active_domains "$root")

    # Right column data
    local top; top=$(top_triggered "$root/.wal" 7 4)
    local rec; rec=$(recurring_mistakes "$root" | head -3)
    local recent; recent=$(recently_created "$root" 3)
    local stale; stale=$(stale_notes "$root" 90 | head -3)

    # Build left column lines
    local L=()
    L+=("Storage")
    L+=("-------")
    L+=("Total notes:    $(printf '%4d' "$total")")
    L+=("  mistakes:     $(printf '%4d' "$mist")")
    L+=("  notes:        $(printf '%4d' "$note")")
    L+=("  projects:     $(printf '%4d' "$proj")")
    L+=("  feedback:     $(printf '%4d' "$fb")")
    L+=("  knowledge:    $(printf '%4d' "$kn")")
    L+=("  continuity:   $(printf '%4d' "$cont")")
    L+=("  strategies:   $(printf '%4d' "$strat")")
    L+=("  seeds:        $(printf '%4d' "$seed")")
    L+=("")
    L+=("WAL: $wal_lines entries ($wal_size)")
    L+=("")
    L+=("Active project: $proj_act")
    L+=("Active domains: $dom_act")

    # Build right column lines
    local R=()
    R+=("Top Triggered (7d)")
    R+=("------------------")
    if [[ -z "$top" ]]; then
        R+=("(no WAL data)")
    else
        while IFS=$'\t' read -r name cnt; do
            R+=("$(printf '%-25s x%d' "$(truncate "$name" 25)" "$cnt")")
        done <<< "$top"
    fi
    R+=("")
    R+=("Recurring Mistakes")
    R+=("------------------")
    if [[ -z "$rec" ]]; then
        R+=("(none)")
    else
        while IFS=$'\t' read -r name cnt; do
            R+=("$(printf '%-25s [x%d]' "$(truncate "$name" 25)" "$cnt")")
        done <<< "$rec"
    fi
    R+=("")
    R+=("Recently Created")
    R+=("----------------")
    if [[ -z "$recent" ]]; then
        R+=("(empty)")
    else
        while IFS=$'\t' read -r name dt; do
            R+=("$(printf '%-25s (%s)' "$(truncate "$name" 25)" "$dt")")
        done <<< "$recent"
    fi
    R+=("")
    R+=("Stale (>90d, no refs)")
    R+=("---------------------")
    if [[ -z "$stale" ]]; then
        R+=("(none)")
    else
        while IFS=$'\t' read -r name age; do
            R+=("$(printf '%-25s %s' "$(truncate "$name" 25)" "$age")")
        done <<< "$stale"
    fi

    # Pair lines
    local n_left=${#L[@]} n_right=${#R[@]}
    local n_max=$(( n_left > n_right ? n_left : n_right ))
    local i
    for ((i = 0; i < n_max; i++)); do
        render_two_col "${L[i]:-}" "${R[i]:-}"
        echo
    done
}
```

- [ ] **Step 2: Visual sanity check against real memory**

```bash
SOURCE_ONLY=1 source ~/Development/hypomnema-memdash/bin/memory-dashboard.sh
draw_top "Memory Dashboard" "$(date '+%Y-%m-%d %H:%M')"
draw_tab_header overview
render_overview "$HOME/.claude/memory"
draw_bottom "[Tab] switch  [r] refresh  [q] quit"
```

Expected: full Overview screen, all blocks aligned, no width overflow.

- [ ] **Step 3: Commit**

```bash
cd ~/Development/hypomnema-memdash && git add bin/memory-dashboard.sh
git commit -m "feat(memory-dash): render Overview tab"
```

---

## Task 8: Hook history + health checks + activity

**Files:**
- Modify: `~/Development/hypomnema-memdash/bin/memory-dashboard.sh`
- Modify: `~/Development/hypomnema-memdash/bin/tests/test-memory-dashboard.sh`

- [ ] **Step 1: Add tests**

```bash
# health_check_frontmatter ROOT -> "<valid>/<total>"
hc=$(health_check_frontmatter "$FIX")
assert_eq "1/1" "$(echo "$hc" | grep -o '^[0-9]*/[0-9]*$' || echo "$hc")" "health/frontmatter"
# (sample.md has type+created=missing; n1/n2 have both. Let's add 'created' to sample, expect 4/4)
```

> **Test fixture caveat:** `sample.md` already has `created`; `n1.md`/`n2.md` have it too. Total = 4 .md files (sample, n1, n2, p1 — but p1 has no frontmatter). Update p1 to have proper frontmatter, OR test should expect 3/4.

Update `projects/p1.md`:
```bash
cat > ~/Development/hypomnema-memdash/bin/tests/fixtures/memory/projects/p1.md <<'EOF'
---
type: project
created: 2026-04-01
---
proj
EOF
```

Now expected: 4/4 valid.

- [ ] **Step 2: Add hook_last_run test**

Skip — depends on real `~/.claude/hooks/` content. Test only that function returns "-" when hook missing:

```bash
assert_eq "-" "$(hook_last_run /nonexistent/path/hook.sh)" "hook/missing"
```

- [ ] **Step 3: Run, expect FAIL**

- [ ] **Step 4: Implement**

Add to `memory-dashboard.sh`:

```bash
# stat_mtime FILE — portable mtime in epoch seconds
stat_mtime() {
    local f=$1
    [[ -e "$f" ]] || { echo ""; return; }
    stat -f %m "$f" 2>/dev/null || stat -c %Y "$f" 2>/dev/null
}

# hook_last_run HOOK_PATH -> "HH:MM" (today) or "Nd ago" (older) or "-"
hook_last_run() {
    local f=$1
    local mt; mt=$(stat_mtime "$f")
    [[ -n "$mt" ]] || { echo "-"; return; }
    local now; now=$(date +%s)
    local age=$(( now - mt ))
    if (( age < 86400 )); then
        date -r "$mt" '+%H:%M' 2>/dev/null || date -d "@$mt" '+%H:%M'
    else
        echo "$((age / 86400))d ago"
    fi
}

# health_check_frontmatter ROOT -> "valid/total"
health_check_frontmatter() {
    local root=$1 valid=0 total=0
    for cat in "${MEMORY_CATEGORIES[@]}"; do
        local d="$root/$cat"
        [[ -d "$d" ]] || continue
        for f in "$d"/*.md; do
            [[ -f "$f" ]] || continue
            total=$((total + 1))
            local t; t=$(parse_frontmatter_field "$f" type)
            local c; c=$(parse_frontmatter_field "$f" created)
            [[ -n "$t" && -n "$c" ]] && valid=$((valid + 1))
        done
    done
    echo "$valid/$total"
}

# health_check_orphans ROOT MEMORY_MD -> count of broken links in MEMORY.md
health_check_orphans() {
    local root=$1 memory_md=$2
    [[ -f "$memory_md" ]] || { echo 0; return; }
    local broken=0
    while IFS= read -r link; do
        # link is relative path like ../../../.claude/memory/notes/foo.md
        local resolved
        resolved=$(cd "$(dirname "$memory_md")" && cd "$(dirname "$link")" 2>/dev/null && echo "$PWD/$(basename "$link")")
        [[ -f "$resolved" ]] || broken=$((broken + 1))
    done < <(grep -oE '\([^)]+\.md\)' "$memory_md" | tr -d '()')
    echo "$broken"
}
```

- [ ] **Step 5: Run, expect PASS**

- [ ] **Step 6: Commit**

```bash
cd ~/Development/hypomnema-memdash && git add bin/memory-dashboard.sh bin/tests/
git commit -m "feat(memory-dash): hook history + health checks"
```

---

## Task 9: Render Ops tab

**Files:**
- Modify: `~/Development/hypomnema-memdash/bin/memory-dashboard.sh`

- [ ] **Step 1: Define ops registry**

Add to `memory-dashboard.sh`:

```bash
# Ops registry: parallel arrays (bash 3.2 has no assoc arrays).
OPS_LABEL=(
    "Regenerate index"
    "Run consolidate"
    "Run dedup (dry-run)"
    "Backfill ref_count"
    "Enrich frontmatter"
    "Score strategies"
    "Self-profile refresh"
    "WAL compact"
)
OPS_CMD=(
    "$HOOKS_DIR/regen-memory-index.sh"
    "$BIN_DIR/memory-consolidate.sh"
    "python3 $BIN_DIR/memory-dedup.py --dry-run"
    "$BIN_DIR/memory-backfill-refcount.sh"
    "$BIN_DIR/memory-enrich-frontmatter.sh"
    "$BIN_DIR/memory-strategy-score.sh"
    "$BIN_DIR/memory-self-profile.sh"
    "$HOOKS_DIR/wal-compact.sh"
)

# Hooks tracked in "Last Hook Runs" panel
HOOK_PATHS=(
    "$HOOKS_DIR/memory-session-start.sh"
    "$HOOKS_DIR/memory-user-prompt-submit.sh"
    "$HOOKS_DIR/memory-stop.sh"
    "$HOOKS_DIR/memory-index.sh"
)
HOOK_LABELS=(
    "session-start"
    "user-prompt"
    "memory-stop"
    "memory-index"
)
```

- [ ] **Step 2: Implement render_ops()**

```bash
render_ops() {
    local root=$1

    # Left column: ops menu + health
    local L=()
    L+=("Operations")
    L+=("----------")
    local i
    for ((i = 0; i < ${#OPS_LABEL[@]}; i++)); do
        local cmd_path="${OPS_CMD[i]%% *}"   # first word
        local marker=""
        [[ -x "$cmd_path" ]] || marker="  (missing)"
        L+=("$(printf '[%d] %s%s' "$((i + 1))" "${OPS_LABEL[i]}" "$marker")")
    done
    L+=("")
    L+=("Health Checks")
    L+=("-------------")
    local fm; fm=$(health_check_frontmatter "$root")
    local orph; orph=$(health_check_orphans "$root" "$HOME/.claude/projects/-Users-akamash/memory/MEMORY.md")
    L+=("Frontmatter valid: $fm")
    L+=("Broken md links:   $orph")

    # Right column: hook history + activity
    local R=()
    R+=("Last Hook Runs")
    R+=("--------------")
    for ((i = 0; i < ${#HOOK_PATHS[@]}; i++)); do
        local last; last=$(hook_last_run "${HOOK_PATHS[i]}")
        R+=("$(printf '%-15s %s' "${HOOK_LABELS[i]}" "$last")")
    done
    R+=("")
    R+=("Activity (7d)")
    R+=("-------------")
    R+=("Notes created:    $(wal_count_event "$root/.wal" 7 create)")
    R+=("Notes referenced: $(wal_count_event "$root/.wal" 7 reference)")
    R+=("Triggers fired:   $(wal_count_event "$root/.wal" 7 trigger)")

    # Pair
    local n_left=${#L[@]} n_right=${#R[@]}
    local n_max=$(( n_left > n_right ? n_left : n_right ))
    for ((i = 0; i < n_max; i++)); do
        render_two_col "${L[i]:-}" "${R[i]:-}"
        echo
    done
}
```

- [ ] **Step 3: Visual sanity check**

```bash
SOURCE_ONLY=1 source ~/Development/hypomnema-memdash/bin/memory-dashboard.sh
draw_top "Memory Dashboard" "$(date '+%Y-%m-%d %H:%M')"
draw_tab_header ops
render_ops "$HOME/.claude/memory"
draw_bottom "[1-8] run  [Tab] switch  [r] refresh  [q] quit"
```

Expected: aligned Ops tab, all 8 operations listed, missing scripts marked.

- [ ] **Step 4: Commit**

```bash
cd ~/Development/hypomnema-memdash && git add bin/memory-dashboard.sh
git commit -m "feat(memory-dash): render Ops tab"
```

---

## Task 10: Operation runner

**Files:**
- Modify: `~/Development/hypomnema-memdash/bin/memory-dashboard.sh`

- [ ] **Step 1: Implement run_operation()**

```bash
# run_operation N  (1-based index into OPS_CMD)
# Exits alt-screen, runs the command, waits for keypress, re-enters alt-screen.
run_operation() {
    local n=$1
    local idx=$((n - 1))
    [[ $idx -ge 0 && $idx -lt ${#OPS_CMD[@]} ]] || return
    local cmd="${OPS_CMD[idx]}"
    local cmd_path="${cmd%% *}"

    # Exit alt-screen, show normal terminal
    tput rmcup 2>/dev/null
    tput cnorm 2>/dev/null
    echo
    echo "==> Running: ${OPS_LABEL[idx]}"
    echo "    Command: $cmd"
    echo "    -----------------"

    if [[ ! -x "$cmd_path" ]]; then
        echo "    ERROR: '$cmd_path' not executable or missing"
    else
        eval "$cmd"
        local rc=$?
        echo "    -----------------"
        echo "    Exit code: $rc"
    fi

    echo
    printf "Press any key to return to dashboard..."
    read -rsn1
    # Re-enter alt-screen
    tput smcup 2>/dev/null
    tput civis 2>/dev/null
}
```

- [ ] **Step 2: Manual smoke test**

```bash
SOURCE_ONLY=1 source ~/Development/hypomnema-memdash/bin/memory-dashboard.sh
run_operation 1   # regen index
```

Expected: exits to normal screen, runs `regen-memory-index.sh`, prints exit code, waits for key, returns.

- [ ] **Step 3: Commit**

```bash
cd ~/Development/hypomnema-memdash && git add bin/memory-dashboard.sh
git commit -m "feat(memory-dash): operation runner with alt-screen toggle"
```

---

## Task 11: Main loop

**Files:**
- Modify: `~/Development/hypomnema-memdash/bin/memory-dashboard.sh`

- [ ] **Step 1: Implement validate + cleanup + main**

Replace the `main()` stub:

```bash
validate_terminal() {
    local cols; cols=$(tput cols 2>/dev/null || echo 80)
    local lines; lines=$(tput lines 2>/dev/null || echo 24)
    if (( cols < TUI_WIDTH || lines < 24 )); then
        echo "memory-dashboard: terminal too small (need ≥ ${TUI_WIDTH}x24, got ${cols}x${lines})" >&2
        exit 1
    fi
}

cleanup() {
    tput cnorm 2>/dev/null
    tput rmcup 2>/dev/null
    stty echo 2>/dev/null
    echo
}

draw_screen() {
    local active=$1 root=$2
    clear
    draw_top "Memory Dashboard" "$(date '+%Y-%m-%d %H:%M')"
    draw_tab_header "$active"
    if [[ "$active" == "overview" ]]; then
        render_overview "$root"
        draw_bottom "[Tab] switch  [r] refresh  [q] quit"
    else
        render_ops "$root"
        draw_bottom "[1-8] run  [Tab] switch  [r] refresh  [q] quit"
    fi
}

main() {
    validate_terminal
    trap cleanup EXIT INT TERM

    tput smcup 2>/dev/null
    tput civis 2>/dev/null

    local active="overview"
    local root="$MEMORY_DIR"

    while true; do
        draw_screen "$active" "$root"
        # Read one key
        local key
        IFS= read -rsn1 key
        # Tab is \t (ASCII 9)
        case "$key" in
            q|Q) break ;;
            r|R) continue ;;
            $'\t') [[ "$active" == "overview" ]] && active="ops" || active="overview" ;;
            1) active="ops"; run_operation 1 ;;
            2) active="ops"; run_operation 2 ;;
            3) active="ops"; run_operation 3 ;;
            4) active="ops"; run_operation 4 ;;
            5) active="ops"; run_operation 5 ;;
            6) active="ops"; run_operation 6 ;;
            7) active="ops"; run_operation 7 ;;
            8) active="ops"; run_operation 8 ;;
            *) ;;  # ignore
        esac
    done
}
```

- [ ] **Step 2: End-to-end test**

```bash
~/Development/hypomnema-memdash/bin/memory-dashboard.sh
```

Verify:
- Overview tab loads
- `Tab` switches to Ops
- `Tab` switches back to Overview
- `r` redraws (timestamp updates)
- `1` runs regen-memory-index.sh, returns to Ops
- `q` exits cleanly, terminal restored, cursor visible

- [ ] **Step 3: Commit**

```bash
cd ~/Development/hypomnema-memdash && git add bin/memory-dashboard.sh
git commit -m "feat(memory-dash): main loop with keyboard handling"
```

---

## Task 12: install.sh integration + README

**Files:**
- Modify: `install.sh` — add symlinks for dashboard + short alias
- Create: `bin/README-memory-dash.md`

- [ ] **Step 1: Update install.sh**

Find the `# --- Install bin helpers ---` block (around line 52-55) and add:

```bash
ln -sf "$SCRIPT_DIR/bin/memory-dashboard.sh" "$CLAUDE_DIR/bin/memory-dashboard.sh"
ln -sf "$SCRIPT_DIR/bin/memory-dashboard.sh" "$CLAUDE_DIR/bin/memory-dash"
```

- [ ] **Step 2: Run install locally**

```bash
cd ~/Development/hypomnema-memdash && ./install.sh
```

Verify:
```bash
ls -la ~/.claude/bin/memory-dash ~/.claude/bin/memory-dashboard.sh
```
Both should be symlinks to `~/Development/hypomnema-memdash/bin/memory-dashboard.sh`.

- [ ] **Step 3: Add README**

Create `bin/README-memory-dash.md`:

```markdown
# memory-dash

Console TUI for `~/.claude/memory/` observation and operations.

## Usage

    memory-dash         # short alias (after install.sh)
    memory-dashboard.sh # full name

Tabs: `[Tab]` switch · `[r]` refresh · `[1-8]` run op (Ops tab) · `[q]` quit.

## Tests

    bash bin/tests/test-memory-dashboard.sh

## Spec

See `docs/specs/2026-04-13-memory-dashboard-design.md`.
```

- [ ] **Step 4: End-to-end test via installed symlink**

```bash
~/.claude/bin/memory-dash
```
Verify: launches dashboard, Overview tab renders against real `~/.claude/memory/`.

- [ ] **Step 5: Final test run**

```bash
bash bin/tests/test-memory-dashboard.sh
```
Expected: all assertions pass, exit 0.

- [ ] **Step 6: Final commit**

```bash
cd ~/Development/hypomnema-memdash && git add install.sh bin/README-memory-dash.md
git commit -m "feat(memory-dash): install.sh integration + README"
```

---

## Self-Review Notes

**Spec coverage:**
- Overview blocks (Storage, Top Triggered, Recurring, Recently Created, Stale, Active project/domains, WAL stats) → Tasks 3, 4, 5, 7
- Ops blocks (Operations menu, Last Hook Runs, Health Checks, Activity, Recent Errors) → Tasks 8, 9
  - **Gap:** "Recent Errors" block from spec is NOT implemented (no clear log location for hook errors yet). Per spec's open question #2, this is honest — the block is replaced by Activity stats. Update spec or add a follow-up task if log format gets defined.
- Ops runner (alt-screen toggle) → Task 10
- Main loop (keyboard, refresh, tab switch, cleanup) → Task 11
- Health checks: frontmatter ✓, broken links ✓, MEMORY.md sync (= broken links) ✓. Orphan files (= files on disk not in MEMORY.md) **NOT implemented** — judged YAGNI for MVP, broken-links covers the inverse direction. Add follow-up if needed.

**Type/name consistency:** `OPS_LABEL`/`OPS_CMD` parallel arrays, `MEMORY_CATEGORIES` array, `parse_frontmatter_field` signature consistent across tasks. ✓

**Placeholder scan:** No TBD/TODO. All steps have concrete code. ✓

**Known portability caveats** (called out in tasks):
- `mapfile` not in bash 3.2 → fallback `read -r` loop noted in Task 4
- `stat -f %m` (macOS) vs `stat -c %Y` (Linux) → wrapper `stat_mtime()` in Task 8
- `date -j -f` (macOS) vs `date -d` (Linux) → `||` fallback in Task 5

---

## Execution

Plan complete and saved to `~/.claude/docs/plans/2026-04-13-memory-dashboard.md`. Two execution options:

**1. Subagent-Driven (recommended)** — fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — execute tasks in this session using executing-plans, batch with checkpoints.

Which approach?
