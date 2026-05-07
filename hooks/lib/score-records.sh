#!/bin/bash
# Score computations for SessionStart ranking.
# Source from hook scripts. Pure functions writing scoring strings to stdout.
#
# Provides:
#   compute_wal_scores <wal_file> <today>   → "name:raw:strat_used;..." or empty
#   compute_tfidf_scores <index> <kw>       → "name:score;..." or empty
#   load_noise_candidates <analytics_report> → space-separated slugs or empty
#
# All functions are read-only; freshness checks (is index stale?) stay in
# session-start.sh because they drive side effects (background rebuild).

# --- WAL-based spaced repetition scoring ---
# Formula: spread × decay(recency) × effectiveness, capped at 10.
#   spread        = unique days with inject in 30 days (min 2 injects to count)
#   decay         = 1 / (1 + days_since_last / 7)
#   effectiveness = Bayesian (positive + 1) / (positive + negative + 2)
# strategy-used events tracked separately as a side-channel bonus signal.
compute_wal_scores() {
  local wal_file="$1" today="$2"
  [ -f "$wal_file" ] || return 0

  # Cold-start ADR (docs/decisions/cold-start-scoring.md):
  # Bayesian effectiveness is gated behind a minimum outcome-signal
  # threshold inside a short recency window. When the window holds
  # fewer than MIN_SAMPLES outcome-* events for a slug, eff is pinned
  # to 1.0 (neutral) instead of being computed from the formula —
  # keyword/project/recency still rank, but noise slugs don't get
  # systematically halved by pseudo-counts on empty data.
  # Thresholds ENV-overrideable for local calibration.
  local min_samples="${HYPOMNEMA_BAYESIAN_MIN_SAMPLES:-5}"
  local window_days="${HYPOMNEMA_OUTCOME_WINDOW_DAYS:-14}"

  # Cutoffs anchored at $today (HYPOMNEMA_TODAY when frozen by tests,
  # real date otherwise). Anchoring on real `date` made fixtures with
  # frozen HYPOMNEMA_TODAY rot once wall-clock drifted past their WAL
  # window — fixtures wrote WAL events relative to HYPOMNEMA_TODAY but
  # the cutoff was being computed from real today, silently dropping
  # in-window events into the "out of window" bucket.
  local cutoff_30d cutoff_gate
  if [[ "$OSTYPE" == darwin* ]]; then
    cutoff_30d=$(date -v-30d -j -f '%Y-%m-%d' "$today" +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
    cutoff_gate=$(date -v-"${window_days}"d -j -f '%Y-%m-%d' "$today" +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
  else
    cutoff_30d=$(date -d "$today - 30 days" +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
    cutoff_gate=$(date -d "$today - ${window_days} days" +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
  fi

  awk -F'|' -v cutoff="$cutoff_30d" -v cutoff_gate="$cutoff_gate" \
    -v today="$today" -v min_samples="$min_samples" '
    $1 >= cutoff && $2 == "inject" {
      count[$3]++
      days[$3, $1] = 1
      if ($1 > last_day[$3] || last_day[$3] == "") last_day[$3] = $1
    }
    $1 >= cutoff && $2 == "inject-agg" {
      count[$3] += $4
      days[$3, $1] = 1
      if ($1 > last_day[$3] || last_day[$3] == "") last_day[$3] = $1
    }
    $1 >= cutoff && $2 == "outcome-positive" { pos_count[$3]++ }
    $1 >= cutoff && $2 == "outcome-negative" { neg_count[$3]++ }
    # Gate window is narrower than the formula window so we can detect
    # "stale signal" — a slug with outcomes only from 30 days ago stays
    # dormant rather than coasting on old data.
    $1 >= cutoff_gate && ($2 == "outcome-positive" || $2 == "outcome-negative") {
      gate_count[$3]++
    }
    # C9 (audit-2026-04-16): trigger-match is a retrieval/exposure signal,
    # NOT evidence that a record actually helped. Counting it into the
    # Bayesian effectiveness term let frequently-injected-but-never-useful
    # records float to the top by exposure alone. The docstring above
    # defines effectiveness purely over outcome-* events; keep it that way.
    $1 >= cutoff && $2 == "strategy-used"    { strat_used[$3]++ }
    function jdn(y, m, d) {
      if (m <= 2) { y--; m += 12 }
      return int(365.25*(y+4716)) + int(30.6001*(m+1)) + d - 1524
    }
    function days_between(d1, d2) {
      split(d1, a, "-"); split(d2, b, "-")
      return jdn(b[1]+0, b[2]+0, b[3]+0) - jdn(a[1]+0, a[2]+0, a[3]+0)
    }
    END {
      first = 1
      for (name in count) {
        if (count[name] < 2) continue
        spread = 0
        for (key in days) {
          split(key, kp, SUBSEP)
          if (kp[1] == name) spread++
        }
        ds = days_between(last_day[name], today)
        if (ds < 0) ds = 0
        decay = 1.0 / (1.0 + ds / 7.0)
        gs = (name in gate_count) ? gate_count[name] : 0
        if (gs < min_samples) {
          # Dormant: neutral 1.0. See cold-start ADR trade-off note on
          # 1.0 vs 0.5 — 0.5 systematically halved every slug on sparse
          # corpora, 1.0 lets the rest of the formula carry signal.
          effectiveness = 1.0
        } else {
          pc = (name in pos_count) ? pos_count[name] : 0
          nc = (name in neg_count) ? neg_count[name] : 0
          effectiveness = (pc + 1) / (pc + nc + 2)
        }
        raw = spread * decay * effectiveness
        if (raw > 10) raw = 10
        su = 0
        if (name in strat_used) su = strat_used[name]
        if (!first) printf ";"
        printf "%s:%.1f:%d", name, raw, su
        first = 0
      }
    }
  ' "$wal_file" 2>/dev/null || true
}

# --- TF-IDF scoring against current keywords ---
# Reads precomputed index ($MEMORY_DIR/.tfidf-index, TSV: term \t file:score \t ...).
# Returns "name:total_score;..." summed across all keywords.
compute_tfidf_scores() {
  local index="$1" keywords="$2"
  [ -f "$index" ] || return 0
  [ -z "$keywords" ] && return 0

  awk -F'\t' -v kw="$keywords" '
    BEGIN { n=split(kw, kws, " ") }
    {
      term=$1
      for (i=1; i<=n; i++) {
        if (term == kws[i]) {
          for (j=2; j<=NF; j++) {
            split($j, fp, ":")
            scores[fp[1]] += fp[2]+0
          }
        }
      }
    }
    END {
      first=1
      for (f in scores) {
        if (!first) printf ";"
        printf "%s:%.2f", f, scores[f]
        first=0
      }
    }
  ' "$index"
}

# --- Load noise candidates from analytics report ---
# Returns space-separated slugs found under "## Noise candidates" heading.
load_noise_candidates() {
  local analytics_report="$1"
  [ -f "$analytics_report" ] || return 0

  awk '
    /^## Noise candidates/ { in_noise=1; next }
    in_noise && /^## / { exit }
    in_noise && /^- / { sub(/^- /, ""); sub(/ .*/, ""); print }
  ' "$analytics_report" 2>/dev/null | tr '\n' ' '
}
