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

echo
echo "$PASS passed, $FAIL failed"
[[ $FAIL -eq 0 ]]
