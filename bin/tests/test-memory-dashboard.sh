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

echo
echo "$PASS passed, $FAIL failed"
[[ $FAIL -eq 0 ]]
