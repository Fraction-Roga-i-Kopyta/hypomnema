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
    # Format detection (FORMAT.md §5):
    #   v1 — $3 = domains_csv,         $4 = metrics_blob
    #   v2 — $3 = "domains:csv,error_count:N,tool_calls:M,duration:Xs"
    #        $4 = session_id
    if (substr($3, 1, 8) == "domains:") {
      _rest = substr($3, 9)
      if (match(_rest, /,[a-z_]+:/)) {
        _domains = substr(_rest, 1, RSTART - 1)
        _metrics = substr(_rest, RSTART + 1)
      } else {
        _domains = _rest
        _metrics = ""
      }
    } else {
      _domains = $3
      _metrics = $4
    }
    ec = _metrics; sub(/.*error_count:/, "", ec); sub(/,.*/, "", ec)
    total_errors += ec + 0
    if (ec + 0 > 0) {
      n = split(_domains, doms, ",")
      for (i = 1; i <= n; i++) error_domains[doms[i]]++
    }
  }
  $2 == "strategy-used" { strat_used[$3]++ }
  $2 == "strategy-gap" {
    n = split($3, doms, ",")
    for (i = 1; i <= n; i++) gap_domains[doms[i]]++
  }

  # Recall + classification signals (previously unread by analytics).
  # Motivation: hooks emit ~27 distinct WAL event types but pre-v0.10.4
  # analytics read only 8, dropping >50% of observable signal. These
  # three classes are the highest-leverage additions — they tell the
  # operator which files FTS surfaces that substring triggers miss,
  # which files get cited vs go silent, and whether parse/evidence
  # breakage is accumulating.
  $2 == "shadow-miss"        { shadow_miss[$3]++ }
  $2 == "trigger-useful"     { trig_useful[$3]++ }
  $2 == "trigger-silent"     { trig_silent[$3]++ }
  # v0.13: retroactive silent classification for sessions that never
  # reached Stop (crash / <120s / no transcript). Counted separately
  # so trends stay traceable: a slug going from 20% silent to 70%
  # silent-retro is a different signal than going to 70% silent.
  $2 == "trigger-silent-retro" { trig_silent_retro[$3]++ }
  $2 == "schema-error"       { schema_err++ }
  $2 == "format-unsupported" { format_un++ }
  $2 == "evidence-missing"   { evi_missing++ }
  $2 == "evidence-empty"     { evi_empty++ }

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

    # Recall gaps: files FTS5 shadow surfaced but substring triggers
    # never matched. Candidates for `triggers:` field expansion. Top 10.
    # `%06d`-prefixed keys sort lexicographically in numeric order; the
    # manual insertion-sort below stays BSD-awk-safe (no asort).
    sm_count = 0
    for (name in shadow_miss) {
      sm_entries[++sm_count] = sprintf("%06d\t%s", shadow_miss[name], name)
    }
    if (sm_count > 0) {
      insertion_sort(sm_entries, sm_count)
      printf "\n## Recall gaps\n"
      printed = 0
      for (i = sm_count; i >= 1 && printed < 10; i--) {
        split(sm_entries[i], f, "\t")
        printf "- %s: %d shadow-miss hits\n", f[2], f[1] + 0
        printed++
      }
    }

    # Trigger usefulness: per-slug breakdown of useful/silent events.
    # High silent + zero useful ⇒ rule lacks evidence phrases or is
    # ambient without precision_class mark. Top 10 by silent count.
    tu_count = 0
    for (name in trig_silent) tu_seen[name] = 1
    for (name in trig_useful) tu_seen[name] = 1
    for (name in trig_silent_retro) tu_seen[name] = 1
    for (name in tu_seen) {
      u = (name in trig_useful) ? trig_useful[name] : 0
      s = (name in trig_silent) ? trig_silent[name] : 0
      r = (name in trig_silent_retro) ? trig_silent_retro[name] : 0
      tu_entries[++tu_count] = sprintf("%06d\t%s\t%d\t%d\t%d", s + r, name, u, s, r)
    }
    if (tu_count > 0) {
      insertion_sort(tu_entries, tu_count)
      printf "\n## Trigger usefulness\n"
      printed = 0
      for (i = tu_count; i >= 1 && printed < 10; i--) {
        split(tu_entries[i], f, "\t")
        if (f[5] + 0 > 0) {
          printf "- %s: useful=%d silent=%d silent-retro=%d\n", f[2], f[3] + 0, f[4] + 0, f[5] + 0
        } else {
          printf "- %s: useful=%d silent=%d\n", f[2], f[3] + 0, f[4] + 0
        }
        printed++
      }
    }

    # Schema & format health: if parsing is failing somewhere, the
    # operator needs to see counts, not just `memoryctl doctor` binary
    # WARN. Emits only when something is non-zero so a healthy install
    # gets a clean report.
    if (schema_err + format_un + evi_missing + evi_empty > 0) {
      printf "\n## Schema & format health (last 30d)\n"
      if (schema_err > 0)  printf "- schema-error: %d\n", schema_err
      if (format_un > 0)   printf "- format-unsupported: %d\n", format_un
      if (evi_missing > 0) printf "- evidence-missing: %d\n", evi_missing
      if (evi_empty > 0)   printf "- evidence-empty: %d\n", evi_empty
    }
  }

  # Ascending insertion sort by string comparison — callers expecting
  # numeric order format their keys with `%06d` so lexicographic and
  # numeric orderings coincide. Stays portable (no gawk `asort`).
  function insertion_sort(arr, n,   i, j, tmp) {
    for (i = 2; i <= n; i++) {
      tmp = arr[i]; j = i
      while (j > 1 && arr[j-1] > tmp) { arr[j] = arr[j-1]; j-- }
      arr[j] = tmp
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
