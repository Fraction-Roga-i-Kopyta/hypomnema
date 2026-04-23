# hooks/lib/health-check.sh
# shellcheck shell=bash
# Runtime health diagnostics surfaced in SessionStart output.
#
# Scans three classes of silent failure that the `exit 0 on internal
# failure` hook contract would otherwise hide from the operator:
#
#   1. Broken symlinks / missing companion scripts — install.sh
#      produced a symlink into a dev-folder that has since moved or
#      been renamed. Detected by checking three required library
#      files exist.
#   2. UserPromptSubmit silent-fail — 50+ injects in the last 200
#      WAL entries with zero trigger-match events. Usually means the
#      reactive-trigger pipeline is hitting its hook timeout or
#      failing to source wal-lock.sh.
#   3. Schema errors — >0 schema-error WAL events in the last 200
#      entries. Surfaces accumulating broken frontmatter.
#
# Depends on caller having set $WAL_FILE and passed the hooks dir
# (typically `dirname "$0"`).
#
# Emits a one-line warning on stdout or nothing. Caller captures via
# command substitution:
#
#   HEALTH_WARNING=$(compute_health_warning "$(dirname "$0")")
compute_health_warning() {
  local hook_dir="$1"
  local warning=""

  # 1. Broken symlinks / missing companion scripts.
  local broken="" req
  for req in "$hook_dir/lib/wal-lock.sh" "$hook_dir/wal-compact.sh" "$hook_dir/regen-memory-index.sh"; do
    [ -f "$req" ] || broken="${broken}${broken:+, }$(basename "$req")"
  done
  if [ -n "$broken" ]; then
    warning="⚠ Memory hooks broken — missing: $broken. Likely cause: symlink target moved. Re-run hypomnema/install.sh."
    printf '%s' "$warning"
    return 0
  fi

  [ -f "$WAL_FILE" ] || return 0

  # 2. UserPromptSubmit silent-fail signal. grep -c outputs "0" with
  # exit 1 when no matches; `$()` strips trailing newline so numeric
  # comparison works directly.
  local wal_tail inj trig
  wal_tail=$(tail -200 "$WAL_FILE" 2>/dev/null)
  inj=$(printf '%s\n' "$wal_tail" | grep -c '|inject|')
  trig=$(printf '%s\n' "$wal_tail" | grep -c '|trigger-match|')
  if [ "${inj:-0}" -gt 50 ] && [ "${trig:-0}" -eq 0 ]; then
    warning="⚠ Memory health: 0 trigger-match events in last 200 WAL entries (${inj} injects). UserPromptSubmit hook may be silently failing — check settings.json timeout and ~/.claude/hooks/lib/wal-lock.sh presence."
    printf '%s' "$warning"
    return 0
  fi

  # 3. Schema errors accumulated.
  local schema_errs err_slugs
  schema_errs=$(printf '%s\n' "$wal_tail" | grep -c '|schema-error|')
  if [ "${schema_errs:-0}" -gt 0 ]; then
    err_slugs=$(printf '%s\n' "$wal_tail" | awk -F'|' '$2=="schema-error"{print $3}' | sort -u | head -5 | tr '\n' ' ')
    warning="⚠ Memory schema: ${schema_errs} malformed frontmatter entries recently (${err_slugs}). Fix closing --- in these files."
    printf '%s' "$warning"
    return 0
  fi
}
