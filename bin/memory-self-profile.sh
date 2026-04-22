#!/bin/bash
# Generate ~/.claude/memory/self-profile.md from WAL + mistakes + strategies.
# Honest profile: strengths (strategy success + clean-session domains),
# weaknesses (recurring mistakes + outcome-negative), calibration (error-prone domains).
#
# Idempotent. Regenerates on every call — cheap because it only aggregates.
# Invoked from memory-stop.sh (background) at most once per session.

set -o pipefail

# LC_ALL=C: locale-stable decimal separator — awk printf "%.2f" must emit
# "3.00" not "3,00" regardless of user locale. See audit-2026-04-16 R1.
export LC_ALL=C

# Delegate to the Go implementation when available. `memoryctl self-profile`
# is byte-for-byte equivalent (verified by scripts/parity-check.sh). It's
# one WAL pass instead of ~10 awk invocations — significant savings once
# the WAL grows past a few thousand lines.
if command -v memoryctl >/dev/null 2>&1; then
  exec memoryctl self-profile
fi

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
WAL_FILE="$MEMORY_DIR/.wal"
OUT="$MEMORY_DIR/self-profile.md"

[ -f "$WAL_FILE" ] || exit 0

# HYPOMNEMA_NOW overrides the "generated:" stamp for parity tests; same
# contract as HYPOMNEMA_TODAY in the dedup path.
TS="${HYPOMNEMA_NOW:-$(date '+%Y-%m-%d %H:%M')}"

# --- Weaknesses: top mistakes by recurrence, scope=universal highlighted ---
weaknesses=""
if [ -d "$MEMORY_DIR/mistakes" ]; then
  weaknesses=$(awk '
    FNR==1 { fm=0; rec=0; scope="domain"; sev="?" }
    /^---$/ {
      fm++
      if (fm==2) {
        if (rec+0 > 0) printf "%s\t%s\t%s\t%s\n", rec, scope, sev, FILENAME
        fm=0; rec=0; scope="domain"; sev="?"
      }
      next
    }
    fm==1 && /^recurrence:/ { sub(/^recurrence: */,""); rec=$0; next }
    fm==1 && /^scope:/ { sub(/^scope: */,""); scope=$0; next }
    fm==1 && /^severity:/ { sub(/^severity: */,""); sev=$0; next }
  ' "$MEMORY_DIR/mistakes"/*.md 2>/dev/null | sort -rn | head -5 | while IFS=$'\t' read -r rec scope sev path; do
    slug=$(basename "$path" .md)
    marker=""
    [ "$scope" = "universal" ] && marker=" 🔴"
    printf -- "- \`%s\` [%s, x%s, %s]%s\n" "$slug" "$sev" "$rec" "$scope" "$marker"
  done)
fi

# --- Strengths: strategies ranked by success_count ---
strengths=""
if [ -d "$MEMORY_DIR/strategies" ]; then
  strengths=$(awk '
    FNR==1 { fm=0; sc=0 }
    /^---$/ {
      fm++
      if (fm==2) {
        printf "%s\t%s\n", (sc+0), FILENAME
        fm=0; sc=0
      }
      next
    }
    fm==1 && /^success_count:/ { sub(/^success_count: */,""); sc=$0; next }
  ' "$MEMORY_DIR/strategies"/*.md 2>/dev/null | sort -rn | head -5 | while IFS=$'\t' read -r sc path; do
    slug=$(basename "$path" .md)
    printf -- "- \`%s\` (success: %s)\n" "$slug" "$sc"
  done)
fi

# --- Calibration: error-prone domains from session-metrics WAL events ---
# Parse session-metrics rows: $3=domains (comma-sep), $4=error_count:N,tool_calls:M,duration:Xs
calibration=$(awk -F'|' '
  $2 == "session-metrics" {
    ec = 0
    if (match($4, /error_count:[0-9]+/)) {
      s = substr($4, RSTART, RLENGTH); sub(/^error_count:/, "", s); ec = s + 0
    }
    n = split($3, doms, ",")
    for (i=1; i<=n; i++) {
      d = doms[i]
      if (d == "" || d == "unknown") continue
      total[d]++
      if (ec > 0) err[d] += ec
    }
  }
  END {
    for (d in total) {
      rate = (total[d] > 0) ? err[d] / total[d] : 0
      printf "%.2f\t%d\t%s\n", rate, total[d], d
    }
  }
' "$WAL_FILE" 2>/dev/null | sort -rn | head -5 | while IFS=$'\t' read -r rate sessions domain; do
  printf -- "- \`%s\`: %s err/session avg (n=%s)\n" "$domain" "$rate" "$sessions"
done)

# --- Meta-signals: aggregate counts from WAL ---
total_sessions=$(awk -F'|' '$2 == "session-metrics"' "$WAL_FILE" 2>/dev/null | wc -l | tr -d ' ')
clean_sessions=$(awk -F'|' '$2 == "clean-session"' "$WAL_FILE" 2>/dev/null | wc -l | tr -d ' ')
outcome_pos=$(awk -F'|' '$2 == "outcome-positive"' "$WAL_FILE" 2>/dev/null | wc -l | tr -d ' ')
outcome_neg=$(awk -F'|' '$2 == "outcome-negative"' "$WAL_FILE" 2>/dev/null | wc -l | tr -d ' ')
strategy_used=$(awk -F'|' '$2 == "strategy-used"' "$WAL_FILE" 2>/dev/null | wc -l | tr -d ' ')
strategy_gap=$(awk -F'|' '$2 == "strategy-gap"' "$WAL_FILE" 2>/dev/null | wc -l | tr -d ' ')
# Build ambient set: files with precision_class: ambient in frontmatter.
# Ambient rules (language, code-approach, meta-philosophy) shape behavior silently
# and don't produce trigger-useful events — excluding from precision denominator.
ambient_list=$(grep -rl "^precision_class: ambient" "$MEMORY_DIR" 2>/dev/null \
  | xargs -n1 basename 2>/dev/null | sed 's/\.md$//' | tr '\n' ',' | sed 's/,$//')

# All trigger events (for totals and ambient breakdown)
trigger_useful_all=$(awk -F'|' '$2 == "trigger-useful"' "$WAL_FILE" 2>/dev/null | wc -l | tr -d ' ')
trigger_silent_all=$(awk -F'|' '$2 == "trigger-silent"' "$WAL_FILE" 2>/dev/null | wc -l | tr -d ' ')

# Diagnostic signals (previously write-only — now surfaced):
# evidence-empty: rules with no frontmatter.evidence and no body-extractable phrases
#   → cannot match trigger-useful by design. These need evidence added or deletion.
evidence_empty_unique=$(awk -F'|' '$2 == "evidence-empty" { print $3 }' "$WAL_FILE" 2>/dev/null | sort -u | wc -l | tr -d ' ')

# outcome-new: fresh mistakes recorded (mistake edited but not injected this session)
#   → rate of new learning. High = corpus growing, low = stable/stagnant.
outcome_new_count=$(awk -F'|' '$2 == "outcome-new"' "$WAL_FILE" 2>/dev/null | wc -l | tr -d ' ')

# Ambient activations: trigger events on ambient-classified files
ambient_activations=$(awk -F'|' -v list="$ambient_list" '
  BEGIN {
    n = split(list, arr, ",")
    for (i = 1; i <= n; i++) ambient[arr[i]] = 1
  }
  ($2 == "trigger-useful" || $2 == "trigger-silent") && ($3 in ambient) { c++ }
  END { print c+0 }
' "$WAL_FILE" 2>/dev/null)

# Measurable-only: exclude ambient from both buckets
trigger_useful=$(awk -F'|' -v list="$ambient_list" '
  BEGIN {
    n = split(list, arr, ",")
    for (i = 1; i <= n; i++) ambient[arr[i]] = 1
  }
  $2 == "trigger-useful" && !($3 in ambient) { c++ }
  END { print c+0 }
' "$WAL_FILE" 2>/dev/null)

trigger_silent_total=$(awk -F'|' -v list="$ambient_list" '
  BEGIN {
    n = split(list, arr, ",")
    for (i = 1; i <= n; i++) ambient[arr[i]] = 1
  }
  $2 == "trigger-silent" && !($3 in ambient) { c++ }
  END { print c+0 }
' "$WAL_FILE" 2>/dev/null)

# silent-applied: trigger-silent but outcome-positive in same session for same file
# (measurable-only — ambient already excluded above)
silent_applied=$(awk -F'|' -v list="$ambient_list" '
  BEGIN {
    n = split(list, arr, ",")
    for (i = 1; i <= n; i++) ambient[arr[i]] = 1
  }
  $2 == "trigger-silent" && !($3 in ambient) { silent[$3 "|" $4] = 1 }
  $2 == "outcome-positive" { positive[$3 "|" $4] = 1 }
  END {
    n = 0
    for (k in silent) if (k in positive) n++
    print n
  }
' "$WAL_FILE" 2>/dev/null)
silent_noise=$((trigger_silent_total - silent_applied))

# Precision over measurable-only denominator
trigger_total=$((trigger_useful + trigger_silent_total))
if [ "$trigger_total" -gt 0 ]; then
  precision_pct=$(awk -v u="$trigger_useful" -v s="$silent_applied" -v t="$trigger_total" \
    'BEGIN { printf "%.0f", (u + s) * 100 / t }')
else
  precision_pct="n/a"
fi

cat > "$OUT" <<EOF
---
type: self-profile
generated: ${TS}
source: derived from .wal + mistakes/ + strategies/
---

# Self-Profile

**Do not edit manually.** This file is generated from WAL metrics.

## Meta-signals
| signal | count |
|---|---|
| total sessions (logged) | ${total_sessions} |
| clean sessions (0 errors) | ${clean_sessions} |
| outcome-positive (mistake not repeated) | ${outcome_pos} |
| outcome-negative (mistake repeated) | ${outcome_neg} |
| strategy-used (clean session + strategy injected) | ${strategy_used} |
| strategy-gap (clean session, no strategy) | ${strategy_gap} |
| ambient activations (rules excluded from precision by design) | ${ambient_activations} |
| trigger-useful measurable (referenced explicitly) | ${trigger_useful} |
| silent-applied measurable (silent + outcome-positive) | ${silent_applied} |
| silent-noise (silent, no application signal — **tuning targets**) | ${silent_noise} |
| **measurable precision** (useful + applied) / (useful + silent) | **${precision_pct}%** |
| evidence-empty rules (unique, cannot match trigger by design) | ${evidence_empty_unique} |
| outcome-new (fresh mistakes recorded — learning rate) | ${outcome_new_count} |

## Strengths (top strategies by success_count)
${strengths:-_no data_}

## Weaknesses (top mistakes by recurrence)
${weaknesses:-_no data_}

🔴 = scope:universal (systemic tendency, not project-specific)

## Calibration (error-prone domains)
${calibration:-_not enough data for calibration_}

---
_Accuracy improves with WAL volume. Refreshed on: clean-session, outcome-positive/negative, strategy-used._
EOF

exit 0
