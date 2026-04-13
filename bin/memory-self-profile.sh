#!/bin/bash
# Generate ~/.claude/memory/self-profile.md from WAL + mistakes + strategies.
# Honest profile: strengths (strategy success + clean-session domains),
# weaknesses (recurring mistakes + outcome-negative), calibration (error-prone domains).
#
# Idempotent. Regenerates on every call — cheap because it only aggregates.
# Invoked from memory-stop.sh (background) at most once per session.

set -o pipefail

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
WAL_FILE="$MEMORY_DIR/.wal"
OUT="$MEMORY_DIR/self-profile.md"

[ -f "$WAL_FILE" ] || exit 0

TS=$(date '+%Y-%m-%d %H:%M')

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
trigger_useful=$(awk -F'|' '$2 == "trigger-useful"' "$WAL_FILE" 2>/dev/null | wc -l | tr -d ' ')
trigger_silent=$(awk -F'|' '$2 == "trigger-silent"' "$WAL_FILE" 2>/dev/null | wc -l | tr -d ' ')

cat > "$OUT" <<EOF
---
type: self-profile
generated: ${TS}
source: derived from .wal + mistakes/ + strategies/
---

# Self-Profile

**Не вручную.** Этот файл генерируется из WAL-метрик. Не редактировать.

## Meta-signals
| signal | count |
|---|---|
| total sessions (logged) | ${total_sessions} |
| clean sessions (0 errors) | ${clean_sessions} |
| outcome-positive (mistake not repeated) | ${outcome_pos} |
| outcome-negative (mistake repeated) | ${outcome_neg} |
| strategy-used (clean session + strategy injected) | ${strategy_used} |
| strategy-gap (clean session, no strategy) | ${strategy_gap} |
| trigger-useful (injected memory referenced) | ${trigger_useful} |
| trigger-silent (injected memory not referenced) | ${trigger_silent} |

## Strengths (top strategies by success_count)
${strengths:-_нет данных_}

## Weaknesses (top mistakes by recurrence)
${weaknesses:-_нет данных_}

🔴 = scope:universal (системная склонность, не проектная)

## Calibration (error-prone domains)
${calibration:-_мало данных для калибровки_}

---
_Чем больше данных в WAL, тем точнее профиль. Триггеры для обновления: клиновая сессия, outcome-positive/negative, strategy-used._
EOF

exit 0
