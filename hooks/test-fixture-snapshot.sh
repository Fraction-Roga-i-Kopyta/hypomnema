#!/usr/bin/env bash
set -o pipefail
# test-fixture-snapshot.sh — snapshot-based verification of a synthetic
# fixture against its expected.json. SPEC-first: expected.json is written
# before the code it validates, then this harness is run to diff actual
# behaviour against spec.
#
# Usage: test-fixture-snapshot.sh <fixture-dir>
#
# Exit codes:
#   0 — all declared assertions pass
#   1 — at least one assertion failed (details on stderr)
#   2 — harness usage error (missing fixture, missing memoryctl)
export LC_ALL=C

FIXTURE="${1:-}"
if [ -z "$FIXTURE" ] || [ ! -d "$FIXTURE" ]; then
  echo "Usage: $0 <fixture-dir>" >&2
  exit 2
fi

EXPECTED="$FIXTURE/expected.json"
MEMORY="$FIXTURE/memory"

if [ ! -f "$EXPECTED" ]; then
  echo "ERROR: $EXPECTED not found" >&2
  exit 2
fi
if [ ! -d "$MEMORY" ]; then
  echo "ERROR: $MEMORY not found" >&2
  exit 2
fi

MEMORYCTL="${MEMORYCTL:-$(command -v memoryctl 2>/dev/null || echo "")}"
if [ -z "$MEMORYCTL" ] || [ ! -x "$MEMORYCTL" ]; then
  # Fall back to the committed build output.
  REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
  if [ -x "$REPO_ROOT/bin/memoryctl" ]; then
    MEMORYCTL="$REPO_ROOT/bin/memoryctl"
  else
    echo "ERROR: memoryctl binary not found (set MEMORYCTL env or run 'make build')" >&2
    exit 2
  fi
fi

FIXTURE_NAME="$(basename "$FIXTURE")"
echo "== fixture: $FIXTURE_NAME =="

PASS=0
FAIL=0
SKIP=0

pass() { echo "  ✓ $*"; PASS=$((PASS + 1)); }
fail() { echo "  ✗ $*" >&2; FAIL=$((FAIL + 1)); }
skip() { echo "  - $* (skipped)"; SKIP=$((SKIP + 1)); }

# --- Run memoryctl doctor against the fixture ---
# doctor exits non-zero on FAIL-severity checks; we only need the JSON.
DOCTOR_JSON=$(CLAUDE_MEMORY_DIR="$MEMORY" CLAUDE_DIR="$(dirname "$MEMORY")" \
  "$MEMORYCTL" doctor --json 2>/dev/null || true)

if [ -z "$DOCTOR_JSON" ]; then
  fail "memoryctl doctor --json produced empty output"
  echo "summary: passed=$PASS failed=$FAIL skipped=$SKIP"
  exit 1
fi

# --- wal_counts assertions ---
WAL="$MEMORY/.wal"
if jq -e '.assertions.wal_counts' "$EXPECTED" >/dev/null 2>&1; then
  if [ ! -f "$WAL" ]; then
    fail "wal_counts declared but $WAL missing"
  else
    # Count events on demand per key — `declare -A` is bash 4+ only,
    # and /bin/bash on macos-runner (and any stock macOS) is 3.2.
    # grep -c prints 0 on no match and exits 1; we swallow the exit.
    while IFS=$'\t' read -r key op value; do
      [ -z "$key" ] && continue
      case "$key" in
        inject)           actual=$(grep -c '|inject|'           "$WAL" 2>/dev/null || true) ;;
        inject_agg)       actual=$(grep -c '|inject-agg|'       "$WAL" 2>/dev/null || true) ;;
        trigger_silent)   actual=$(grep -c '|trigger-silent|'   "$WAL" 2>/dev/null || true) ;;
        trigger_useful)   actual=$(grep -c '|trigger-useful|'   "$WAL" 2>/dev/null || true) ;;
        outcome_positive) actual=$(grep -c '|outcome-positive|' "$WAL" 2>/dev/null || true) ;;
        outcome_negative) actual=$(grep -c '|outcome-negative|' "$WAL" 2>/dev/null || true) ;;
        clean_session)    actual=$(grep -c '|clean-session|'    "$WAL" 2>/dev/null || true) ;;
        *)                actual=0 ;;
      esac
      actual="${actual:-0}"
      case "$op" in
        eq)
          if [ "$actual" = "$value" ]; then
            pass "wal_counts.$key == $value (got $actual)"
          else
            fail "wal_counts.$key expected $value, got $actual"
          fi
          ;;
        min)
          if [ "$actual" -ge "$value" ]; then
            pass "wal_counts.$key >= $value (got $actual)"
          else
            fail "wal_counts.$key expected >= $value, got $actual"
          fi
          ;;
        max)
          if [ "$actual" -le "$value" ]; then
            pass "wal_counts.$key <= $value (got $actual)"
          else
            fail "wal_counts.$key expected <= $value, got $actual"
          fi
          ;;
      esac
    done < <(jq -r '
      .assertions.wal_counts
      | to_entries[]
      | [ (.key | sub("_min$"; "") | sub("_max$"; ""))
        , (if .key | test("_min$") then "min"
           elif .key | test("_max$") then "max"
           else "eq" end)
        , (.value | tostring)
        ] | @tsv
    ' "$EXPECTED" 2>/dev/null)
  fi
fi

# --- corpus_counts assertions ---
if jq -e '.assertions.corpus_counts' "$EXPECTED" >/dev/null 2>&1; then
  while IFS=$'\t' read -r type expected; do
    [ -z "$type" ] && continue
    actual=$(find "$MEMORY/$type" -name '*.md' -type f 2>/dev/null | wc -l | tr -d ' ')
    if [ "$actual" = "$expected" ]; then
      pass "corpus_counts.$type == $expected"
    else
      fail "corpus_counts.$type expected $expected, got $actual"
    fi
  done < <(jq -r '
    .assertions.corpus_counts
    | to_entries[]
    | [.key, (.value | tostring)] | @tsv
  ' "$EXPECTED")
fi

# --- doctor assertions ---
if jq -e '.assertions.doctor' "$EXPECTED" >/dev/null 2>&1; then
  # Iterate declared checks. Each key is a doctor check name.
  for check_name in $(jq -r '.assertions.doctor | keys[]' "$EXPECTED"); do
    check_json=$(printf '%s' "$DOCTOR_JSON" \
      | jq --arg n "$check_name" '.checks[] | select(.name == $n)')
    if [ -z "$check_json" ]; then
      fail "doctor.$check_name — check not present in memoryctl doctor output (not implemented yet?)"
      continue
    fi

    # Status assertion.
    expected_status=$(jq -r --arg n "$check_name" \
      '.assertions.doctor[$n].status // empty' "$EXPECTED")
    if [ -n "$expected_status" ]; then
      actual_status=$(printf '%s' "$check_json" | jq -r '.status')
      if [ "$actual_status" = "$expected_status" ]; then
        pass "doctor.$check_name.status == $expected_status"
      else
        fail "doctor.$check_name.status expected $expected_status, got $actual_status"
      fi
    fi

    # Extra-field assertions (declared under .assertions.doctor.<name>.extra).
    if jq -e --arg n "$check_name" \
         '.assertions.doctor[$n].extra' "$EXPECTED" >/dev/null 2>&1; then
      actual_extra=$(printf '%s' "$check_json" | jq '.extra // {}')
      expected_extra=$(jq --arg n "$check_name" \
        '.assertions.doctor[$n].extra' "$EXPECTED")

      # Iterate expected keys. Special-case *_sorted suffix — compare sorted
      # arrays regardless of input order.
      for k in $(printf '%s' "$expected_extra" | jq -r 'keys[]'); do
        if [[ "$k" == *"_sorted" ]]; then
          base="${k%_sorted}"
          exp_arr=$(printf '%s' "$expected_extra" | jq --arg k "$k" '.[$k] | sort')
          act_arr=$(printf '%s' "$actual_extra" | jq --arg k "$base" '.[$k] // [] | sort')
          if [ "$exp_arr" = "$act_arr" ]; then
            pass "doctor.$check_name.extra.$base (sorted) matches"
          else
            fail "doctor.$check_name.extra.$base expected $exp_arr, got $act_arr"
          fi
        else
          exp_val=$(printf '%s' "$expected_extra" | jq --arg k "$k" '.[$k]')
          act_val=$(printf '%s' "$actual_extra" | jq --arg k "$k" '.[$k] // null')
          if [ "$exp_val" = "$act_val" ]; then
            pass "doctor.$check_name.extra.$k == $exp_val"
          else
            fail "doctor.$check_name.extra.$k expected $exp_val, got $act_val"
          fi
        fi
      done
    fi
  done
fi

# --- scoring_probe: read-only ranking snapshot via memoryctl scoring-probe ---
# Runs the subcommand against the fixture and checks each field under
# `must_match`. Each declared key must be present in the probe output
# and have the declared value (exact match on JSON scalars).
if jq -e '.assertions.scoring_probe' "$EXPECTED" >/dev/null 2>&1; then
  probe_slug=$(jq -r '.assertions.scoring_probe.slug' "$EXPECTED")
  if [ -z "$probe_slug" ] || [ "$probe_slug" = "null" ]; then
    fail "scoring_probe declared but no slug field — expected.json malformed"
  else
    probe_json=$(CLAUDE_MEMORY_DIR="$MEMORY" CLAUDE_DIR="$(dirname "$MEMORY")" \
      "$MEMORYCTL" scoring-probe "$probe_slug" 2>/dev/null || true)
    if [ -z "$probe_json" ]; then
      fail "scoring_probe — memoryctl scoring-probe returned empty output"
    else
      for key in $(jq -r '.assertions.scoring_probe.must_match | keys[]' "$EXPECTED"); do
        exp=$(jq --arg k "$key" '.assertions.scoring_probe.must_match[$k]' "$EXPECTED")
        act=$(printf '%s' "$probe_json" | jq --arg k "$key" '.[$k]')
        if [ "$exp" = "$act" ]; then
          pass "scoring_probe.$probe_slug.$key == $exp"
        else
          fail "scoring_probe.$probe_slug.$key expected $exp, got $act"
        fi
      done
    fi
  fi
fi

echo "summary: passed=$PASS failed=$FAIL skipped=$SKIP"
[ "$FAIL" -eq 0 ]
