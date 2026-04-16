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

  local cutoff_30d
  if [[ "$OSTYPE" == darwin* ]]; then
    cutoff_30d=$(date -v-30d +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
  else
    cutoff_30d=$(date -d "30 days ago" +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
  fi

  awk -F'|' -v cutoff="$cutoff_30d" -v today="$today" '
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
        pc = 0; nc = 0
        if (name in pos_count) pc = pos_count[name]
        if (name in neg_count) nc = neg_count[name]
        effectiveness = (pc + 1) / (pc + nc + 2)
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
