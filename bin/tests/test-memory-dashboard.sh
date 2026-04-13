#!/bin/bash
set -u
export SOURCE_ONLY=1
source "$(dirname "$0")/../memory-dashboard.sh"

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

# parse_frontmatter_field
assert_eq "5"      "$(parse_frontmatter_field "$FIX/mistakes/sample.md" recurrence)" "fm/recurrence"
assert_eq "major"  "$(parse_frontmatter_field "$FIX/mistakes/sample.md" severity)"   "fm/severity"
assert_eq ""       "$(parse_frontmatter_field "$FIX/mistakes/sample.md" missing)"    "fm/missing"

# count_notes_by_category
assert_eq "1" "$(count_notes_by_category "$FIX" mistakes)"  "count/mistakes"
assert_eq "2" "$(count_notes_by_category "$FIX" notes)"     "count/notes"
assert_eq "1" "$(count_notes_by_category "$FIX" projects)"  "count/projects"
assert_eq "0" "$(count_notes_by_category "$FIX" missing)"   "count/missing"
assert_eq "4" "$(count_notes_total "$FIX")"                  "count/total"

# format_age
assert_eq "just now" "$(format_age 0)"      "age/0"
assert_eq "5m ago"   "$(format_age 300)"    "age/5m"
assert_eq "2h ago"   "$(format_age 7200)"   "age/2h"
assert_eq "3d ago"   "$(format_age 259200)" "age/3d"

# wal_events_in_window
lines=()
while IFS= read -r line; do lines+=("$line"); done < <(wal_events_in_window "$FIX/.wal" 7)
assert_eq "5" "${#lines[@]}" "wal/count_in_7d"

# wal_count_event
assert_eq "3" "$(wal_count_event "$FIX/.wal" 7 trigger)"   "wal/triggers"
assert_eq "1" "$(wal_count_event "$FIX/.wal" 7 create)"    "wal/creates"
assert_eq "1" "$(wal_count_event "$FIX/.wal" 7 reference)" "wal/refs"

# top_triggered (tab-separated "name<TAB>count", sorted desc, limit N)
result=$(top_triggered "$FIX/.wal" 7 5)
assert_eq $'wrong-root-cause\t2' "$(echo "$result" | head -1)" "top/first"
assert_eq $'code-approach\t1'    "$(echo "$result" | sed -n 2p)" "top/second"

# recurring_mistakes: prints "<name>\t<recurrence>" for recurrence >= 2
rec=$(recurring_mistakes "$FIX")
assert_eq $'sample\t5' "$(echo "$rec" | head -1)" "recurring/first"

# recently_created: prints "<name>\t<created>" sorted desc, limit N
rc=$(recently_created "$FIX" 10)
assert_eq "n1" "$(echo "$rc" | head -1 | cut -f1)" "recent/first"

# stale_notes: ref_count == 0 AND referenced older than threshold days
st=$(stale_notes "$FIX" 90)
assert_eq "n2" "$(echo "$st" | head -1 | cut -f1)" "stale/first"

# active_context: parse _agent_context.md
assert_eq "akasha"              "$(active_project "$FIX")" "active/project"
assert_eq "frontend, debugging" "$(active_domains "$FIX")" "active/domains"

echo
echo "$PASS passed, $FAIL failed"
[[ $FAIL -eq 0 ]]
