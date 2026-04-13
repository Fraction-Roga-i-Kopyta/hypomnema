#!/bin/bash
# memory-dashboard.sh — TUI for ~/.claude/memory observation and ops
# Spec: docs/specs/2026-04-13-memory-dashboard-design.md

set -u

MEMORY_DIR="${MEMORY_DIR:-$HOME/.claude/memory}"
BIN_DIR="${BIN_DIR:-$HOME/.claude/bin}"
HOOKS_DIR="${HOOKS_DIR:-$HOME/.claude/hooks}"

# parse_frontmatter_field FILE FIELD
# Extracts a top-level YAML field from frontmatter (between --- markers).
# Prints empty string if not found. Strips surrounding quotes from value.
parse_frontmatter_field() {
    local file=$1 field=$2
    awk -v f="$field" '
        BEGIN { in_fm = 0; depth = 0 }
        /^---[[:space:]]*$/ { depth++; in_fm = (depth == 1); next }
        depth >= 2 { exit }
        in_fm {
            if (match($0, "^" f ":[[:space:]]*")) {
                v = substr($0, RLENGTH+1)
                gsub(/^["'\''"]|["'\'']$/, "", v)
                gsub(/[[:space:]]+$/, "", v)
                print v
                exit
            }
        }
    ' "$file" 2>/dev/null
}

# Categories scanned for note counts
MEMORY_CATEGORIES=(mistakes notes projects feedback knowledge continuity strategies seeds decisions journal)

count_notes_by_category() {
    local root=$1 cat=$2
    local dir="$root/$cat"
    [[ -d "$dir" ]] || { echo 0; return; }
    find "$dir" -maxdepth 1 -name "*.md" -type f 2>/dev/null | wc -l | awk '{print $1}'
}

count_notes_total() {
    local root=$1 total=0
    for cat in "${MEMORY_CATEGORIES[@]}"; do
        total=$((total + $(count_notes_by_category "$root" "$cat")))
    done
    echo "$total"
}

format_age() {
    local secs=$1
    if   (( secs < 60 ));     then echo "just now"
    elif (( secs < 3600 ));   then echo "$((secs / 60))m ago"
    elif (( secs < 86400 ));  then echo "$((secs / 3600))h ago"
    else                           echo "$((secs / 86400))d ago"
    fi
}

# wal_events_in_window WAL_DIR DAYS
# Prints all JSONL lines from .wal/ files modified within last DAYS days.
wal_events_in_window() {
    local wal_dir=$1 days=$2
    [[ -d "$wal_dir" ]] || return 0
    find "$wal_dir" -name "*.jsonl" -type f -mtime -"$days" 2>/dev/null \
        | sort \
        | xargs -I{} cat {} 2>/dev/null
}

# wal_count_event WAL_DIR DAYS EVENT_TYPE
wal_count_event() {
    local wal_dir=$1 days=$2 evt=$3
    wal_events_in_window "$wal_dir" "$days" \
        | grep -c "\"event\":\"$evt\"" 2>/dev/null \
        | awk '{print $1+0}'
}

# top_triggered WAL_DIR DAYS LIMIT
top_triggered() {
    local wal_dir=$1 days=$2 limit=$3
    wal_events_in_window "$wal_dir" "$days" \
        | grep '"event":"trigger"' \
        | sed -E 's/.*"note":"([^"]+)".*/\1/' \
        | sort | uniq -c | sort -rn \
        | awk -v lim="$limit" 'NR<=lim {printf "%s\t%d\n", $2, $1}'
}

# recurring_mistakes ROOT
recurring_mistakes() {
    local root=$1
    local dir="$root/mistakes"
    [[ -d "$dir" ]] || return 0
    while IFS= read -r f; do
        [[ -f "$f" ]] || continue
        local rec=$(parse_frontmatter_field "$f" recurrence)
        [[ -n "$rec" && "$rec" -ge 2 ]] 2>/dev/null || continue
        printf "%s\t%s\n" "$(basename "$f" .md)" "$rec"
    done < <(find "$dir" -maxdepth 1 -name "*.md" -type f 2>/dev/null) \
        | sort -t$'\t' -k2 -rn
}

# recently_created ROOT LIMIT
recently_created() {
    local root=$1 limit=$2
    for cat in "${MEMORY_CATEGORIES[@]}"; do
        local d="$root/$cat"
        [[ -d "$d" ]] || continue
        while IFS= read -r f; do
            [[ -f "$f" ]] || continue
            local cr=$(parse_frontmatter_field "$f" created)
            [[ -n "$cr" ]] || continue
            printf "%s\t%s\n" "$(basename "$f" .md)" "$cr"
        done < <(find "$d" -maxdepth 1 -name "*.md" -type f 2>/dev/null)
    done | sort -t$'\t' -k2 -r | head -n "$limit"
}

# stale_notes ROOT DAYS_THRESHOLD
# A note is stale if ref_count == 0 AND referenced is older than DAYS_THRESHOLD.
stale_notes() {
    local root=$1 threshold=$2
    local today_epoch; today_epoch=$(date +%s)
    for cat in "${MEMORY_CATEGORIES[@]}"; do
        local d="$root/$cat"
        [[ -d "$d" ]] || continue
        while IFS= read -r f; do
            [[ -f "$f" ]] || continue
            local rc=$(parse_frontmatter_field "$f" ref_count)
            [[ "$rc" == "0" ]] || continue
            local ref=$(parse_frontmatter_field "$f" referenced)
            [[ -n "$ref" ]] || continue
            local ref_epoch=""
            ref_epoch=$(date -j -f "%Y-%m-%d" "$ref" +%s 2>/dev/null) \
                || ref_epoch=$(date -d "$ref" +%s 2>/dev/null) \
                || continue
            local age_days=$(( (today_epoch - ref_epoch) / 86400 ))
            (( age_days > threshold )) || continue
            printf "%s\t%dd\n" "$(basename "$f" .md)" "$age_days"
        done < <(find "$d" -maxdepth 1 -name "*.md" -type f 2>/dev/null)
    done
}

active_project() {
    local f="$1/_agent_context.md"
    [[ -f "$f" ]] || { printf '%s\n' "-"; return; }
    local v; v=$(awk '/\*\*Active project:\*\*/ {sub(/.*\*\*[[:space:]]*/, ""); gsub(/[[:space:]]+$/, ""); print; exit}' "$f")
    printf '%s\n' "${v:--}"
}

active_domains() {
    local f="$1/_agent_context.md"
    [[ -f "$f" ]] || { printf '%s\n' "-"; return; }
    local v; v=$(awk '/\*\*Active domains:\*\*/ {sub(/.*\*\*[[:space:]]*/, ""); gsub(/[[:space:]]+$/, ""); print; exit}' "$f")
    printf '%s\n' "${v:--}"
}

TUI_WIDTH=80
COL_WIDTH=37   # inner-col width; "| <37> <2sp> <37> |" variant with padding = 80

pad() {
    local s=$1 w=$2
    printf "%-${w}s" "$s"
}

truncate() {
    local s=$1 w=$2
    if (( ${#s} > w )); then
        printf "%s" "${s:0:$w}"
    else
        printf "%s" "$s"
    fi
}

# render_two_col LEFT RIGHT
# Produces: "| <LEFT_37>  <RIGHT_37> |"  (total 80 chars)
render_two_col() {
    local left right
    left=$(pad "$(truncate "$1" $COL_WIDTH)" $COL_WIDTH)
    right=$(pad "$(truncate "$2" $COL_WIDTH)" $COL_WIDTH)
    printf "| %s  %s |" "$left" "$right"
}

# draw_top TITLE TIMESTAMP
# "+-- TITLE -- ... -- TIMESTAMP --+"  total 80
draw_top() {
    local title=$1 ts=$2
    local body=" $title "
    local right=" $ts "
    local fill=$(( TUI_WIDTH - 2 - ${#body} - ${#right} - 4 ))
    local dashes
    dashes=$(printf -- '-%.0s' $(seq 1 $fill))
    printf "+--%s%s%s--+\n" "$body" "$dashes" "$right"
}

draw_sep() {
    local dashes
    dashes=$(printf -- '-%.0s' $(seq 1 $((TUI_WIDTH - 2))))
    printf "+%s+\n" "$dashes"
}

draw_bottom() {
    local hint=$1
    local body=" $hint "
    local fill=$(( TUI_WIDTH - 2 - ${#body} - 2 ))
    local dashes
    dashes=$(printf -- '-%.0s' $(seq 1 $fill))
    printf "+--%s%s+\n" "$body" "$dashes"
}

# draw_tab_header ACTIVE_TAB  ("overview" or "ops")
draw_tab_header() {
    local active=$1 left=""
    if [[ "$active" == "overview" ]]; then
        left="[ Overview ]  Ops"
    else
        left="  Overview  [ Ops ]"
    fi
    render_two_col "$left" "                              q: exit"
    echo
}

# render_overview ROOT
# Composes the Overview tab content (between header and footer).
render_overview() {
    local root=$1

    # Gather data
    local total; total=$(count_notes_total "$root")
    local mist; mist=$(count_notes_by_category "$root" mistakes)
    local note; note=$(count_notes_by_category "$root" notes)
    local proj; proj=$(count_notes_by_category "$root" projects)
    local fb;   fb=$(count_notes_by_category "$root" feedback)
    local kn;   kn=$(count_notes_by_category "$root" knowledge)
    local cont; cont=$(count_notes_by_category "$root" continuity)
    local strat; strat=$(count_notes_by_category "$root" strategies)
    local seed; seed=$(count_notes_by_category "$root" seeds)

    local wal_lines=0 wal_size="-"
    if [[ -d "$root/.wal" ]]; then
        wal_lines=$(find "$root/.wal" -name "*.jsonl" -exec cat {} \; 2>/dev/null | wc -l | awk '{print $1}')
        wal_size=$(du -sh "$root/.wal" 2>/dev/null | awk '{print $1}')
    fi

    local proj_act dom_act
    proj_act=$(active_project "$root")
    dom_act=$(active_domains "$root")

    # Right column data
    local top; top=$(top_triggered "$root/.wal" 7 4)
    local rec; rec=$(recurring_mistakes "$root" | head -3)
    local recent; recent=$(recently_created "$root" 3)
    local stale; stale=$(stale_notes "$root" 90 | head -3)

    # Build left column lines
    local L=()
    L+=("Storage")
    L+=("-------")
    L+=("Total notes:    $(printf '%4d' "$total")")
    L+=("  mistakes:     $(printf '%4d' "$mist")")
    L+=("  notes:        $(printf '%4d' "$note")")
    L+=("  projects:     $(printf '%4d' "$proj")")
    L+=("  feedback:     $(printf '%4d' "$fb")")
    L+=("  knowledge:    $(printf '%4d' "$kn")")
    L+=("  continuity:   $(printf '%4d' "$cont")")
    L+=("  strategies:   $(printf '%4d' "$strat")")
    L+=("  seeds:        $(printf '%4d' "$seed")")
    L+=("")
    L+=("WAL: $wal_lines entries ($wal_size)")
    L+=("")
    L+=("Active project: $proj_act")
    L+=("Active domains: $dom_act")

    # Build right column lines
    local R=()
    R+=("Top Triggered (7d)")
    R+=("------------------")
    if [[ -z "$top" ]]; then
        R+=("(no WAL data)")
    else
        while IFS=$'\t' read -r _rname _rcnt; do
            R+=("$(printf '%-25s x%d' "$(truncate "$_rname" 25)" "$_rcnt")")
        done < <(printf '%s\n' "$top")
    fi
    R+=("")
    R+=("Recurring Mistakes")
    R+=("------------------")
    if [[ -z "$rec" ]]; then
        R+=("(none)")
    else
        while IFS=$'\t' read -r _rname _rcnt; do
            R+=("$(printf '%-25s [x%d]' "$(truncate "$_rname" 25)" "$_rcnt")")
        done < <(printf '%s\n' "$rec")
    fi
    R+=("")
    R+=("Recently Created")
    R+=("----------------")
    if [[ -z "$recent" ]]; then
        R+=("(empty)")
    else
        while IFS=$'\t' read -r _rname _rdt; do
            R+=("$(printf '%-25s (%s)' "$(truncate "$_rname" 25)" "$_rdt")")
        done < <(printf '%s\n' "$recent")
    fi
    R+=("")
    R+=("Stale (>90d, no refs)")
    R+=("---------------------")
    if [[ -z "$stale" ]]; then
        R+=("(none)")
    else
        while IFS=$'\t' read -r _rname _rage; do
            R+=("$(printf '%-25s %s' "$(truncate "$_rname" 25)" "$_rage")")
        done < <(printf '%s\n' "$stale")
    fi

    # Pair lines
    local n_left=${#L[@]} n_right=${#R[@]}
    local n_max=$(( n_left > n_right ? n_left : n_right ))
    local i
    for ((i = 0; i < n_max; i++)); do
        render_two_col "${L[i]:-}" "${R[i]:-}"
        echo
    done
}

# stat_mtime FILE — portable mtime in epoch seconds
stat_mtime() {
    local f=$1
    [[ -e "$f" ]] || { echo ""; return; }
    stat -f %m "$f" 2>/dev/null || stat -c %Y "$f" 2>/dev/null
}

# hook_last_run HOOK_PATH -> "HH:MM" (today) / "Nd ago" (older) / "-" (missing)
hook_last_run() {
    local f=$1
    local mt; mt=$(stat_mtime "$f")
    [[ -n "$mt" ]] || { echo "-"; return; }
    local now; now=$(date +%s)
    local age=$(( now - mt ))
    if (( age < 86400 )); then
        date -r "$mt" '+%H:%M' 2>/dev/null || date -d "@$mt" '+%H:%M' 2>/dev/null || echo "?"
    else
        echo "$((age / 86400))d ago"
    fi
}

# health_check_frontmatter ROOT -> "valid/total"
# A note is "valid" if frontmatter has both 'type' and 'created'.
health_check_frontmatter() {
    local root=$1 valid=0 total=0
    for cat in "${MEMORY_CATEGORIES[@]}"; do
        local d="$root/$cat"
        [[ -d "$d" ]] || continue
        while IFS= read -r f; do
            [[ -f "$f" ]] || continue
            total=$((total + 1))
            local t; t=$(parse_frontmatter_field "$f" type)
            local c; c=$(parse_frontmatter_field "$f" created)
            [[ -n "$t" && -n "$c" ]] && valid=$((valid + 1))
        done < <(find "$d" -maxdepth 1 -name "*.md" -type f 2>/dev/null)
    done
    echo "$valid/$total"
}

# health_check_orphans MEMORY_MD -> count of broken .md links
# Parses "(../../../.claude/memory/.../foo.md)" style links and checks existence.
health_check_orphans() {
    local memory_md=$1
    [[ -f "$memory_md" ]] || { echo 0; return; }
    local broken=0
    local md_dir; md_dir=$(dirname "$memory_md")
    while IFS= read -r link; do
        local resolved
        if [[ "$link" = /* ]]; then
            resolved="$link"
        else
            resolved="$md_dir/$link"
        fi
        [[ -f "$resolved" ]] || broken=$((broken + 1))
    done < <(grep -oE '\([^)]+\.md\)' "$memory_md" 2>/dev/null | tr -d '()')
    echo "$broken"
}

main() {
    echo "memory-dashboard: not implemented yet"
}

[[ "${SOURCE_ONLY:-0}" == "1" ]] && return 0
main "$@"
