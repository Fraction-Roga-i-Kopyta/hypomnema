# hooks/lib/rotation.sh
# shellcheck shell=bash
# Memory lifecycle rotation: active → stale → archived.
#
# Per-type decay rates defined below. Every Stop hook calls
# `lifecycle_rotate` once before any session-specific work; pinned files
# are exempt, archived files are inert.
#
# Depends on caller having already sourced:
#   - lib/wal-lock.sh      (wal_append)
#   - lib/stat-helpers.sh  (_stat_mtime)
# and set `$MEMORY_DIR`.

# lifecycle_rotate — one pass over memory subdirs, marks stale / archives
# based on referenced-or-created age and per-type thresholds.
#
#   mistakes   stale @60d  → archive @180d
#   strategies stale @90d  → archive @180d
#   knowledge  stale @90d  → archive @365d
#   feedback   stale @45d  → archive @120d
#   notes      stale @30d  → archive @90d
#   decisions  stale @90d  → archive @365d
#   journal    stale @30d  → archive @90d
#
# `decay_rate: slow|normal|fast` in frontmatter overrides the type default.
# `status: pinned` is exempt from rotation (verified even for malformed
# frontmatter — see C10 below).
#
# Writes `rotation-stale`, `rotation-archive`, `rotation-summary` WAL
# events. Summary is suppressed on idle runs (no changes), avoiding WAL
# spam — the Stop hook fires ~40+ times on an active day.
lifecycle_rotate() {
  local ARCHIVE_DIR="$MEMORY_DIR/archive"
  local NOW_SEC=$(date +%s)

  local all_files=()
  for subdir in mistakes feedback knowledge notes strategies decisions journal; do
    while IFS= read -r f; do
      all_files+=("$f")
    done < <(find "$MEMORY_DIR/$subdir" -name "*.md" -type f 2>/dev/null)
  done
  [ ${#all_files[@]} -gt 0 ] || return 0

  local rotation_today rotation_stale_count=0 rotation_archive_count=0 rotation_checked=${#all_files[@]}
  rotation_today=$(date +%Y-%m-%d)

  # Single-pass awk: extract status, referenced, created for all files.
  # C2: process-substitution keeps the while-loop in the current shell so
  # the counters survive for the rotation-summary WAL event. Piping
  # through `| while` runs the loop in a subshell and the counters are
  # lost. (audit-2026-04-16 C2)
  while IFS=$'\t' read -r f file_status ref_date created_date file_decay; do
    [ "$file_status" = "pinned" ] && continue
    [ "$file_status" = "archived" ] && continue

    [ -z "$ref_date" ] && ref_date="$created_date"
    [ -z "$ref_date" ] && continue

    local ref_sec
    if [[ "$OSTYPE" == darwin* ]]; then
      ref_sec=$(date -j -f "%Y-%m-%d" "$ref_date" +%s 2>/dev/null) || continue
    else
      ref_sec=$(date -d "$ref_date" +%s 2>/dev/null) || continue
    fi
    local age=$(( (NOW_SEC - ref_sec) / 86400 ))

    local stale_days=30 archive_days=90
    case "$file_decay" in
      slow)   stale_days=180; archive_days=365 ;;
      fast)   stale_days=14;  archive_days=45  ;;
      normal|"")
        case "$f" in
          */mistakes/*)   stale_days=60;  archive_days=180 ;;
          */strategies/*) stale_days=90;  archive_days=180 ;;
          */knowledge/*)  stale_days=90;  archive_days=365 ;;
          */feedback/*)   stale_days=45;  archive_days=120 ;;
          */notes/*)      stale_days=30;  archive_days=90  ;;
          */decisions/*)  stale_days=90;  archive_days=365 ;;
          */journal/*)    stale_days=30;  archive_days=90  ;;
        esac
        ;;
    esac

    if [ "$file_status" = "stale" ] && [ "$age" -gt "$archive_days" ]; then
      local rel="${f#$MEMORY_DIR/}"
      mkdir -p "$ARCHIVE_DIR/$(dirname "$rel")"
      if mv "$f" "$ARCHIVE_DIR/$rel" 2>/dev/null; then
        rotation_archive_count=$((rotation_archive_count + 1))
        wal_append "$rotation_today|rotation-archive|$rel|age_${age}d" 2>/dev/null
      fi
      continue
    fi

    if [ "$age" -gt "$stale_days" ] && [ "$file_status" = "active" ]; then
      if perl -pi -e '
        $in_fm = 1 if /^---$/ && !$seen_fm++;
        $in_fm = 0 if /^---$/ && $seen_fm > 1;
        s/^status: active/status: stale/ if $in_fm;
        if (eof) { $seen_fm = 0; $in_fm = 0; }
      ' "$f" 2>/dev/null; then
        local rel="${f#$MEMORY_DIR/}"
        rotation_stale_count=$((rotation_stale_count + 1))
        wal_append "$rotation_today|rotation-stale|$rel|age_${age}d" 2>/dev/null
      fi
    fi
  done < <(awk '
  { gsub(/\r/, "") }
  FNR == 1 {
    if (prev_file != "") printf "%s\t%s\t%s\t%s\t%s\n", prev_file, status, ref, created, decay
    in_fm = 0; n = 0; status = "_empty_"; ref = ""; created = ""; decay = ""; prev_file = FILENAME
  }
  /^---$/ { n++; if (n==1) in_fm=1; if (n==2) in_fm=0 }
  in_fm && /^status:/ { sub(/^status: */, ""); status = $0 }
  in_fm && /^referenced:/ { sub(/^referenced: */, ""); ref = $0 }
  in_fm && /^created:/ { sub(/^created: */, ""); created = $0 }
  in_fm && /^decay_rate:/ { sub(/^decay_rate: */, ""); decay = $0 }
  END { if (prev_file != "") printf "%s\t%s\t%s\t%s\t%s\n", prev_file, status, ref, created, decay }
  ' "${all_files[@]}" 2>/dev/null)

  # Archive files without valid frontmatter if older than 90 days by
  # mtime. C10 (audit-2026-04-16): pinned files are never auto-archived,
  # even with corrupted frontmatter. A one-shot `status: pinned` sniff
  # over the first 20 lines honours that invariant.
  local f_status
  for f in "${all_files[@]}"; do
    head_line=$(head -1 "$f" 2>/dev/null)
    [ "$head_line" = "---" ] && continue
    f_status=$(awk 'NR <= 20 && /^status:[[:space:]]*["\x27]?pinned["\x27]?[[:space:]]*$/ { print "pinned"; exit }' "$f" 2>/dev/null)
    [ "$f_status" = "pinned" ] && continue
    fmtime=$(_stat_mtime "$f")
    local fage=$(( (NOW_SEC - fmtime) / 86400 ))
    if [ "$fage" -gt 90 ]; then
      local rel="${f#$MEMORY_DIR/}"
      mkdir -p "$ARCHIVE_DIR/$(dirname "$rel")"
      if mv "$f" "$ARCHIVE_DIR/$rel" 2>/dev/null; then
        rotation_archive_count=$((rotation_archive_count + 1))
        wal_append "$rotation_today|rotation-archive|$rel|no_frontmatter_age_${fage}d" 2>/dev/null
      fi
    fi
  done

  # Log summary only when something actually happened — idle runs don't
  # flood the WAL.
  if [ "$rotation_stale_count" -gt 0 ] || [ "$rotation_archive_count" -gt 0 ]; then
    wal_append "$rotation_today|rotation-summary|checked_${rotation_checked}|stale_${rotation_stale_count},archived_${rotation_archive_count}" 2>/dev/null
  fi
}
