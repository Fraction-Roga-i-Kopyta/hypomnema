#!/bin/bash
# bench-gate.sh — perf-regression gate for CI.
#
# Runs `bash hooks/bench-memory.sh perf`, parses the per-scenario
# `avg=<N>ms` output, looks each scenario up in
# `docs/measurements/baselines.json`, and compares the observed avg
# against `baseline.avg_ms × baseline.tolerance` for the current
# platform (darwin or linux).
#
# Each scenario is either:
#   - gated     — exits non-zero if `observed > baseline*tolerance`
#   - observed  — printed for context only, never fails the run
#
# Usage:
#   bash scripts/bench-gate.sh                 # uses default baseline file
#   bash scripts/bench-gate.sh --baseline FILE # custom baseline
#   bash scripts/bench-gate.sh --runs N        # forwarded to bench
#
# Exit codes:
#   0  — every gated scenario within tolerance
#   1  — at least one gated regression
#   2  — harness error (missing baseline, parse failure)

set -uo pipefail
export LC_ALL=C

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BASELINE="${REPO_ROOT}/docs/measurements/baselines.json"
RUNS=""

while [ $# -gt 0 ]; do
  case "$1" in
    --baseline) BASELINE="$2"; shift 2 ;;
    --runs)     RUNS="$2";     shift 2 ;;
    -h|--help)
      sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

if [ ! -f "$BASELINE" ]; then
  echo "ERROR: baseline file not found: $BASELINE" >&2
  exit 2
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "ERROR: jq required for baseline lookup" >&2
  exit 2
fi

case "$(uname -s)" in
  Darwin) PLATFORM="darwin" ;;
  Linux)  PLATFORM="linux"  ;;
  *)      echo "ERROR: unsupported platform $(uname -s)" >&2; exit 2 ;;
esac

# --- Run bench, capture output -------------------------------------------
BENCH_SCRIPT="${REPO_ROOT}/hooks/bench-memory.sh"
[ -x "$BENCH_SCRIPT" ] || BENCH_SCRIPT="bash ${BENCH_SCRIPT}"

BENCH_OUT=$(mktemp)
trap 'rm -f "$BENCH_OUT"' EXIT

if [ -n "$RUNS" ]; then
  BENCH_RUNS="$RUNS" bash "${REPO_ROOT}/hooks/bench-memory.sh" perf 2>&1 | tee "$BENCH_OUT"
else
  bash "${REPO_ROOT}/hooks/bench-memory.sh" perf 2>&1 | tee "$BENCH_OUT"
fi

echo
echo "=== bench-gate ($PLATFORM, baseline: $(basename "$BASELINE")) ==="

# --- Per-scenario gate ---------------------------------------------------
# Extract scenarios from baseline. Each line: "<gated_flag>\t<scenario>"
SCENARIOS=$(jq -r '.scenarios | to_entries[] | (.value.gated|tostring) + "\t" + .key' "$BASELINE")

ANY_FAIL=0
GATED_PASS=0
GATED_FAIL=0
OBSERVED=0

while IFS=$'\t' read -r gated scenario; do
  [ -z "$scenario" ] && continue

  # Look up the platform-specific baseline.
  baseline_avg=$(jq -r --arg s "$scenario" --arg p "$PLATFORM" '.scenarios[$s][$p].avg_ms // empty' "$BASELINE")
  tolerance=$(jq -r --arg s "$scenario" --arg p "$PLATFORM" '.scenarios[$s][$p].tolerance // empty' "$BASELINE")

  if [ -z "$baseline_avg" ] || [ -z "$tolerance" ]; then
    printf "  %-40s  no baseline for platform=%s — skipped\n" "$scenario" "$PLATFORM"
    continue
  fi

  # Match scenario in bench output. Each scenario row has the shape:
  #   "  <label>   runs=N  min=Xms  avg=Yms  max=Zms"
  # The label is left-padded space; grep with leading literal-space anchor
  # is too brittle (line widths shift). Use a fixed-string match on the
  # exact label preceded by indentation.
  observed_line=$(grep -F "  $scenario " "$BENCH_OUT" | head -1)
  if [ -z "$observed_line" ]; then
    printf "  %-40s  not found in bench output — gate cannot evaluate\n" "$scenario"
    [ "$gated" = "true" ] && ANY_FAIL=1
    continue
  fi

  # Parse "avg=NNNms" out of the row.
  observed_avg=$(printf '%s\n' "$observed_line" | sed -n 's/.* avg=\([0-9]\{1,\}\)ms .*/\1/p')
  if [ -z "$observed_avg" ]; then
    printf "  %-40s  could not parse avg=Nms from: %s\n" "$scenario" "$observed_line"
    [ "$gated" = "true" ] && ANY_FAIL=1
    continue
  fi

  # Compare: observed_avg > baseline_avg * tolerance ?
  threshold_ms=$(awk -v b="$baseline_avg" -v t="$tolerance" 'BEGIN { printf "%.0f", b*t }')
  ratio=$(awk -v o="$observed_avg" -v b="$baseline_avg" 'BEGIN { printf "%.2f", (b>0)?o/b:0 }')

  if [ "$gated" = "true" ]; then
    if [ "$observed_avg" -gt "$threshold_ms" ]; then
      printf "  ✗ %-38s  %sms vs baseline %sms × %s = %sms (ratio %sx) FAIL\n" \
        "$scenario" "$observed_avg" "$baseline_avg" "$tolerance" "$threshold_ms" "$ratio"
      GATED_FAIL=$((GATED_FAIL + 1))
      ANY_FAIL=1
    else
      printf "  ✓ %-38s  %sms vs baseline %sms × %s = %sms (ratio %sx)\n" \
        "$scenario" "$observed_avg" "$baseline_avg" "$tolerance" "$threshold_ms" "$ratio"
      GATED_PASS=$((GATED_PASS + 1))
    fi
  else
    printf "  · %-38s  %sms vs baseline %sms (ratio %sx) [observational]\n" \
      "$scenario" "$observed_avg" "$baseline_avg" "$ratio"
    OBSERVED=$((OBSERVED + 1))
  fi
done <<< "$SCENARIOS"

echo
printf "summary: gated_pass=%d gated_fail=%d observed=%d\n" \
  "$GATED_PASS" "$GATED_FAIL" "$OBSERVED"

if [ "$ANY_FAIL" -ne 0 ]; then
  echo
  echo "Perf regression detected — investigate before merge."
  echo "If the baseline is genuinely outdated, edit ${BASELINE#$REPO_ROOT/}"
  echo "and document the calibration in docs/measurements/."
  exit 1
fi
exit 0
