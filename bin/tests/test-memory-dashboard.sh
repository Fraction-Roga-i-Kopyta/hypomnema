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

echo
echo "$PASS passed, $FAIL failed"
[[ $FAIL -eq 0 ]]
