#!/bin/bash
# Batch analytics for hypomnema memory system (WAL over 30 days → .analytics-report)
set -o pipefail
# LC_ALL=C: locale-stable decimal separator (audit-2026-04-16 R1).
export LC_ALL=C

# Effectiveness score = (outcome-positive + 1) / (outcome-positive + outcome-negative + 2)
# This is Bayesian mean with Laplace pseudo-counts (+1, +2), not naive ratio —
# it keeps records with few observations from swinging to 0% or 100% and
# makes thresholds meaningful for small-N mistakes. Do not "simplify" to
# positive / (positive + negative) without also adjusting the thresholds.
#
# Classification thresholds are tunable via environment variables. The
# defaults are calibrated for a typical single-user corpus (~10-50 active
# mistakes); users with much larger / smaller corpora may benefit from
# tightening.
#   ANALYTICS_WINNER_EFF         (default 0.7)  — effectiveness above which a
#                                                 record is flagged "winner"
#   ANALYTICS_NOISE_EFF          (default 0.3)  — effectiveness below which a
#                                                 record is flagged "noise"
#   ANALYTICS_MIN_INJECTS_WINNER (default 3)    — min injects to qualify as winner
#   ANALYTICS_MIN_INJECTS_NOISE  (default 5)    — min injects to qualify as noise

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
WAL_FILE="$MEMORY_DIR/.wal"
REPORT_FILE="$MEMORY_DIR/.analytics-report"
TODAY="${HYPOMNEMA_TODAY:-$(date +%Y-%m-%d)}"

# Tunable classification thresholds (see header).
WINNER_EFF="${ANALYTICS_WINNER_EFF:-0.7}"
NOISE_EFF="${ANALYTICS_NOISE_EFF:-0.3}"
MIN_INJ_WINNER="${ANALYTICS_MIN_INJECTS_WINNER:-3}"
MIN_INJ_NOISE="${ANALYTICS_MIN_INJECTS_NOISE:-5}"

[ -f "$WAL_FILE" ] || exit 0

if [[ "$OSTYPE" == darwin* ]]; then
  CUTOFF=$(date -v-30d +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
else
  CUTOFF=$(date -d "30 days ago" +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
fi

# Single-pass awk: compute all analytics from WAL
ANALYTICS=$(awk -F'|' \
  -v cutoff="$CUTOFF" \
  -v winner_eff="$WINNER_EFF" \
  -v noise_eff="$NOISE_EFF" \
  -v min_inj_winner="$MIN_INJ_WINNER" \
  -v min_inj_noise="$MIN_INJ_NOISE" '
  $1 < cutoff { next }

  $2 == "inject" || $2 == "inject-agg" {
    if ($2 == "inject-agg") injects[$3] += $4
    else injects[$3]++
  }
  $2 == "outcome-positive" { pos[$3]++ }
  $2 == "outcome-negative" { neg[$3]++ }
  $2 == "clean-session" {
    total_clean++
    n = split($3, doms, ",")
    for (i = 1; i <= n; i++) clean_domains[doms[i]]++
  }
  $2 == "session-metrics" {
    total_sessions++
    ec = $4; sub(/.*error_count:/, "", ec); sub(/,.*/, "", ec)
    total_errors += ec + 0
    if (ec + 0 > 0) {
      n = split($3, doms, ",")
      for (i = 1; i <= n; i++) error_domains[doms[i]]++
    }
  }
  $2 == "strategy-used" { strat_used[$3]++ }
  $2 == "strategy-gap" {
    n = split($3, doms, ",")
    for (i = 1; i <= n; i++) gap_domains[doms[i]]++
  }

  END {
    printf "date: %s\n", cutoff
    if (total_sessions > 0) {
      printf "sessions: %d\n", total_sessions
      cr = (total_clean + 0) / total_sessions
      printf "clean_ratio: %.2f\n", cr
      avg_err = total_errors / total_sessions
      printf "avg_errors: %.1f\n", avg_err
    } else {
      printf "sessions: 0\nclean_ratio: 0\navg_errors: 0\n"
    }

    winner_count = 0; noise_count = 0; unproven_count = 0
    for (name in injects) {
      ic = injects[name]
      pc = 0; nc = 0
      if (name in pos) pc = pos[name]
      if (name in neg) nc = neg[name]
      eff = (pc + 1) / (pc + nc + 2)

      if (eff > winner_eff && ic >= min_inj_winner) {
        winners[++winner_count] = sprintf("- %s (eff: %.2f, injects: %d, +%d/-%d)", name, eff, ic, pc, nc)
      }
      if (eff < noise_eff && ic >= min_inj_noise) {
        noise[++noise_count] = sprintf("- %s (eff: %.2f, injects: %d, +%d/-%d)", name, eff, ic, pc, nc)
      }
      if (ic >= 5 && pc == 0 && nc == 0) {
        unproven[++unproven_count] = sprintf("- %s (injects: %d, 0 outcomes)", name, ic)
      }
    }

    if (winner_count > 0) {
      printf "\n## Winners\n"
      for (i = 1; i <= winner_count; i++) print winners[i]
    }
    if (noise_count > 0) {
      printf "\n## Noise candidates\n"
      for (i = 1; i <= noise_count; i++) print noise[i]
    }

    gap_count = 0
    for (dom in clean_domains) {
      if (clean_domains[dom] >= 3) {
        gaps[++gap_count] = sprintf("- %s: %d clean sessions", dom, clean_domains[dom])
      }
    }
    if (gap_count > 0) {
      printf "\n## Strategy gaps\n"
      for (i = 1; i <= gap_count; i++) print gaps[i]
    }

    if (unproven_count > 0) {
      printf "\n## Unproven\n"
      for (i = 1; i <= unproven_count; i++) print unproven[i]
    }
  }
' "$WAL_FILE" 2>/dev/null)

[ -z "$ANALYTICS" ] && exit 0

# Cross-reference strategy gaps with existing strategies
STRATEGY_DIR="$MEMORY_DIR/strategies"
if [ -d "$STRATEGY_DIR" ] && ls "$STRATEGY_DIR"/*.md >/dev/null 2>&1; then
  EXISTING_DOMAINS=$(awk '
    /^---$/ { n++ }
    n==1 && /^domains:/ { sub(/^domains: *\[?/, ""); sub(/\].*/, ""); gsub(/, */, "\n"); print }
  ' "$STRATEGY_DIR"/*.md 2>/dev/null | sort -u | tr '\n' ',' | sed 's/,$//')
  if [ -n "$EXISTING_DOMAINS" ]; then
    ANALYTICS=$(printf '%s\n' "$ANALYTICS" | awk -v covered="$EXISTING_DOMAINS" '
      BEGIN { n = split(covered, c, ","); for (i=1;i<=n;i++) has[c[i]]=1 }
      /^## Strategy gaps/ { in_gaps=1; print; next }
      in_gaps && /^- / {
        dom = $2; sub(/:$/, "", dom)
        if (!(dom in has)) print
        next
      }
      in_gaps && /^## / { in_gaps=0 }
      { print }
    ')
  fi
fi

printf '%s\n' "$ANALYTICS" > "$REPORT_FILE" 2>/dev/null
exit 0
