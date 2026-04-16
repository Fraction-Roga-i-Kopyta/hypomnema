#!/bin/bash
# Context-assembly helpers for SessionStart.
# Source from hook scripts. Functions mutate shared globals — same contract
# as the inline code they replace.
#
# Provides:
#   expand_clusters             — forward/reverse/cross-injected related scan
#                                 + sorted load + provenance.
#                                 Reads:  INJECTED_FILES, KEYWORDS, WAL_SCORES,
#                                         TFIDF_SCORES, MAX_CLUSTER, MEMORY_DIR,
#                                         TODAY, SESSION_ID
#                                 Writes: CLUSTER_MD, CONTRADICTS_WARNINGS,
#                                         _PRIORITY_SLUGS, INJECTED_FILES (append)
#                                 Side:   wal_append cluster-load events
#
#   apply_priority_markers      — adds "[ПРИОРИТЕТ]" to headers of newer
#                                 contradict slugs in section vars.
#                                 Reads:  _PRIORITY_SLUGS
#                                 Writes: MISTAKES_MD, FEEDBACK_MD, KNOWLEDGE_MD,
#                                         STRATEGIES_MD, DECISIONS_MD, NOTES_MD
#
#   assemble_context            — concatenates section vars into the final
#                                 CONTEXT markdown block (CONTINUITY → PROJECT
#                                 → DOMAINS → MISTAKES → FEEDBACK → ... → CLUSTER
#                                 → CONTRADICTS_WARNINGS).
#                                 Reads:  CONTINUITY_MD, PROJECT_MD, DOMAINS,
#                                         MISTAKES_MD, FEEDBACK_MD, KNOWLEDGE_MD,
#                                         STRATEGIES_MD, DECISIONS_MD, NOTES_MD,
#                                         CLUSTER_MD, CONTRADICTS_WARNINGS
#                                 Writes: CONTEXT
#
#   apply_cascade_markers       — scans WAL for cascade-review events <14d old,
#                                 prefixes matching "### slug" headers in CONTEXT
#                                 with "[REVIEW: parent updated YYYY-MM-DD]".
#                                 Reads:  WAL_FILE, CONTEXT
#                                 Writes: CONTEXT

# --- Score-qualifies check used by cluster forward/reverse scans ---
# Returns 0 if file at $1 has at least one keyword/WAL/TF-IDF hit.
_cluster_qualifies() {
  local fpath="$1" slug="$2"
  local fkw
  fkw=$(awk '/^---$/{n++} n==1 && /^keywords:/{sub(/^keywords: *\[?/,""); sub(/\].*/,""); gsub(/, */," "); print; exit}' "$fpath" 2>/dev/null)
  if [ -n "$fkw" ] && [ -n "$KEYWORDS" ]; then
    local k sk
    for k in $fkw; do
      for sk in $KEYWORDS; do
        [ "$k" = "$sk" ] && return 0
      done
    done
  fi
  if [ -n "$WAL_SCORES" ]; then
    case ";${WAL_SCORES};" in *";${slug}:"*) return 0 ;; esac
  fi
  if [ -n "$TFIDF_SCORES" ]; then
    case ";${TFIDF_SCORES};" in *";${slug}:"*) return 0 ;; esac
  fi
  return 1
}

# --- expand_clusters ---
expand_clusters() {
  CLUSTER_MD=""
  CONTRADICTS_WARNINGS=""
  _PRIORITY_SLUGS=" "

  [ ${#INJECTED_FILES[@]} -gt 0 ] || return 0

  # Build space-separated list of injected slugs.
  # R9: filenames with whitespace would make the space-delimited set
  # match word-prefixes of other slugs (e.g. 'file with spaces' makes
  # 'file' and 'with' falsely appear injected). Such filenames are
  # non-idiomatic; skip them from the dedup set rather than fabricate
  # a parallel escaping scheme. They still inject normally; they just
  # won't participate in cluster dedup. (audit-2026-04-16 R9)
  local INJECTED_SLUGS=" "
  local _cf _cs
  for _cf in "${INJECTED_FILES[@]}"; do
    _cs="${_cf##*/}"; _cs="${_cs%.md}"
    case "$_cs" in *[[:space:]]*) continue ;; esac
    INJECTED_SLUGS="${INJECTED_SLUGS}${_cs} "
  done

  local CLUSTER_CANDIDATES=""
  local CLUSTER_PROVENANCE=""

  # --- Forward scan ---
  local _source_slug _rels _rel _target _rtype _tpath _tstatus _prio
  for _cf in "${INJECTED_FILES[@]}"; do
    _source_slug=$(basename "$_cf" .md)
    _rels=$(parse_related "$_cf")
    [ -z "$_rels" ] && continue
    for _rel in $_rels; do
      _target=$(printf '%s' "$_rel" | cut -d: -f1)
      _rtype=$(printf '%s' "$_rel" | cut -d: -f2)
      [ -z "$_target" ] && continue
      CLUSTER_PROVENANCE="${CLUSTER_PROVENANCE}
${_target}|${_source_slug}|${_rtype}"
      case "$INJECTED_SLUGS" in *" ${_target} "*) continue ;; esac
      case "$CLUSTER_CANDIDATES" in *"|${_target}|"*) continue ;; esac
      _tpath=$(find_memory_file "$_target") || continue
      _tstatus=$(awk '/^---$/{n++} n==1 && /^status:/{sub(/^status: */,""); print; exit}' "$_tpath" 2>/dev/null)
      case "$_tstatus" in active|pinned) ;; *) continue ;; esac
      _cluster_qualifies "$_tpath" "$_target" || continue
      case "$_rtype" in
        contradicts) _prio=0 ;;
        reinforces)  _prio=1 ;;
        instance_of) _prio=2 ;;
        supersedes)  _prio=3 ;;
        *)           _prio=2 ;;
      esac
      CLUSTER_CANDIDATES="${CLUSTER_CANDIDATES}
${_prio}|${_target}|${_source_slug}|${_rtype}"
    done
  done

  # --- Reverse scan ---
  # P1: pre-filter to files that actually contain `related:` before paying
  # the awk-per-file cost of parse_related. Typical corpora have related:
  # on <10% of files; grep -l with glob is one process per directory vs
  # one awk per file. (audit-2026-04-16 P1)
  local _rdir _rf _rslug _rrels _rrel _rtarget _rrtype _rstatus _rprio _rel_files
  for _rdir in mistakes feedback strategies knowledge notes decisions; do
    [ -d "$MEMORY_DIR/$_rdir" ] || continue
    _rel_files=$(grep -l -m1 '^related:' "$MEMORY_DIR/$_rdir"/*.md 2>/dev/null) || true
    [ -z "$_rel_files" ] && continue
    while IFS= read -r _rf; do
      [ -f "$_rf" ] || continue
      _rslug="${_rf##*/}"; _rslug="${_rslug%.md}"
      case "$INJECTED_SLUGS" in *" ${_rslug} "*) continue ;; esac
      case "$CLUSTER_CANDIDATES" in *"|${_rslug}|"*) continue ;; esac
      _rrels=$(parse_related "$_rf")
      [ -z "$_rrels" ] && continue
      for _rrel in $_rrels; do
        _rtarget="${_rrel%%:*}"
        _rrtype="${_rrel##*:}"
        case "$INJECTED_SLUGS" in
          *" ${_rtarget} "*)
            _rstatus=$(awk '/^---$/{n++} n==1 && /^status:/{sub(/^status: */,""); print; exit}' "$_rf" 2>/dev/null)
            case "$_rstatus" in active|pinned) ;; *) continue ;; esac
            _cluster_qualifies "$_rf" "$_rslug" || continue
            case "$_rrtype" in
              contradicts) _rprio=0 ;;
              reinforces)  _rprio=1 ;;
              instance_of) _rprio=2 ;;
              supersedes)  _rprio=3 ;;
              *)           _rprio=2 ;;
            esac
            CLUSTER_CANDIDATES="${CLUSTER_CANDIDATES}
${_rprio}|${_rslug}|${_rtarget}|${_rrtype}"
            break
            ;;
        esac
      done
    done <<< "$_rel_files"
  done

  # --- Cross-injected contradicts ---
  local _src_ref _tgt_path _tgt_ref _newer _older
  for _cf in "${INJECTED_FILES[@]}"; do
    _source_slug=$(basename "$_cf" .md)
    _rels=$(parse_related "$_cf")
    [ -z "$_rels" ] && continue
    for _rel in $_rels; do
      _target=$(printf '%s' "$_rel" | cut -d: -f1)
      _rtype=$(printf '%s' "$_rel" | cut -d: -f2)
      [ "$_rtype" = "contradicts" ] || continue
      case "$INJECTED_SLUGS" in
        *" ${_target} "*)
          _src_ref=$(awk '/^---$/{n++} n==1 && /^referenced:/{sub(/^referenced: */,""); print; exit}' "$_cf" 2>/dev/null)
          _tgt_path=$(find_memory_file "$_target") || continue
          _tgt_ref=$(awk '/^---$/{n++} n==1 && /^referenced:/{sub(/^referenced: */,""); print; exit}' "$_tgt_path" 2>/dev/null)
          [ -z "$_src_ref" ] && _src_ref="0000-00-00"
          [ -z "$_tgt_ref" ] && _tgt_ref="0000-00-00"
          if [ "$_tgt_ref" ">" "$_src_ref" ] || [ "$_tgt_ref" = "$_src_ref" ]; then
            _newer="$_target"; _older="$_source_slug"
          else
            _newer="$_source_slug"; _older="$_target"
          fi
          CONTRADICTS_WARNINGS="${CONTRADICTS_WARNINGS}
> ⚠ Конфликт: \`${_older}\` contradicts \`${_newer}\` — проверь актуальность"
          _PRIORITY_SLUGS="${_PRIORITY_SLUGS}${_newer} "
          ;;
      esac
    done
  done

  # --- Sort candidates by priority, take up to MAX_CLUSTER ---
  local _cluster_count=0
  local _cluster_loaded_slugs=" "
  local _slug _source _cpath _cbody _slug_label _src_path
  if [ -n "$CLUSTER_CANDIDATES" ]; then
    while IFS='|' read -r _prio _slug _source _rtype; do
      [ -z "$_slug" ] && continue
      [ "$_cluster_count" -ge "$MAX_CLUSTER" ] && break
      case "$_cluster_loaded_slugs" in *" ${_slug} "*) continue ;; esac
      _cpath=$(find_memory_file "$_slug") || continue
      _cbody=$(sed '1,/^---$/d; 1,/^---$/d' "$_cpath" 2>/dev/null | sed '/^$/d')
      [ -z "$(printf '%s' "$_cbody" | tr -d '[:space:]')" ] && continue

      _slug_label="${_slug}"
      if [ "$_rtype" = "contradicts" ]; then
        _src_path=$(find_memory_file "$_source") || true
        if [ -n "$_src_path" ] && [ -f "$_src_path" ]; then
          _src_ref=$(awk '/^---$/{n++} n==1 && /^referenced:/{sub(/^referenced: */,""); print; exit}' "$_src_path" 2>/dev/null)
          _tgt_ref=$(awk '/^---$/{n++} n==1 && /^referenced:/{sub(/^referenced: */,""); print; exit}' "$_cpath" 2>/dev/null)
          [ -z "$_src_ref" ] && _src_ref="0000-00-00"
          [ -z "$_tgt_ref" ] && _tgt_ref="0000-00-00"
          if [ "$_tgt_ref" ">" "$_src_ref" ] || [ "$_tgt_ref" = "$_src_ref" ]; then
            _newer="$_slug"
          else
            _newer="$_source"
          fi
          if [ "$_newer" = "$_slug" ]; then
            _slug_label="${_slug} [ПРИОРИТЕТ]"
          else
            _PRIORITY_SLUGS="${_PRIORITY_SLUGS}${_newer} "
          fi
          CONTRADICTS_WARNINGS="${CONTRADICTS_WARNINGS}
> ⚠ Конфликт: \`${_source}\` contradicts \`${_slug}\` — проверь актуальность"
        fi
      fi

      CLUSTER_MD="${CLUSTER_MD}
### ${_slug_label} (via ${_source} → ${_rtype})
${_cbody}
"
      INJECTED_FILES+=("$_cpath")
      _cluster_loaded_slugs="${_cluster_loaded_slugs}${_slug} "
      _cluster_count=$((_cluster_count + 1))
      wal_append "$TODAY|cluster-load|$_slug|${SESSION_ID//|/_}" "cluster-load|$_slug|${SESSION_ID//|/_}"
    done < <(printf '%s\n' "$CLUSTER_CANDIDATES" | sort -t'|' -k1,1n | grep -v '^$')
  fi

  # --- Provenance annotations for already-injected related files ---
  local _prov_count=${_cluster_count:-0}
  local _prov_shown="" _ptarget _psource _ptype
  if [ -n "$CLUSTER_PROVENANCE" ]; then
    while IFS='|' read -r _ptarget _psource _ptype; do
      [ -z "$_ptarget" ] && continue
      [ "$_prov_count" -ge "$MAX_CLUSTER" ] && break
      case "$INJECTED_SLUGS" in *" ${_ptarget} "*) ;; *) continue ;; esac
      case "$_prov_shown" in *"|${_ptarget}:${_ptype}|"*) continue ;; esac
      _prov_shown="${_prov_shown}|${_ptarget}:${_ptype}|"
      CLUSTER_MD="${CLUSTER_MD}
- **${_ptarget}** (via ${_psource} → ${_ptype})
"
      wal_append "$TODAY|cluster-load|$_ptarget|${SESSION_ID//|/_}" "cluster-load|$_ptarget|${SESSION_ID//|/_}"
      _prov_count=$((_prov_count + 1))
    done <<< "$CLUSTER_PROVENANCE"
  fi
}

# --- apply_priority_markers ---
apply_priority_markers() {
  [ "$_PRIORITY_SLUGS" = " " ] && return 0
  local _ps _ps_re
  for _ps in $_PRIORITY_SLUGS; do
    [ -z "$_ps" ] && continue
    _ps_re=$(_perl_re_escape "$_ps")
    MISTAKES_MD=$(printf '%s' "$MISTAKES_MD"   | perl -pe "s/^(### ${_ps_re})(\\s|\$)/\$1 [ПРИОРИТЕТ]\$2/")
    FEEDBACK_MD=$(printf '%s' "$FEEDBACK_MD"   | perl -pe "s/^(### ${_ps_re})(\\s|\$)/\$1 [ПРИОРИТЕТ]\$2/")
    KNOWLEDGE_MD=$(printf '%s' "$KNOWLEDGE_MD" | perl -pe "s/^(### ${_ps_re})(\\s|\$)/\$1 [ПРИОРИТЕТ]\$2/")
    STRATEGIES_MD=$(printf '%s' "$STRATEGIES_MD" | perl -pe "s/^(### ${_ps_re})(\\s|\$)/\$1 [ПРИОРИТЕТ]\$2/")
    DECISIONS_MD=$(printf '%s' "$DECISIONS_MD" | perl -pe "s/^(### ${_ps_re})(\\s|\$)/\$1 [ПРИОРИТЕТ]\$2/")
    NOTES_MD=$(printf '%s' "$NOTES_MD"         | perl -pe "s/^(### ${_ps_re})(\\s|\$)/\$1 [ПРИОРИТЕТ]\$2/")
  done
}

# --- assemble_context ---
assemble_context() {
  CONTEXT=""
  [ -n "$CONTINUITY_MD" ] && CONTEXT="${CONTINUITY_MD}"
  [ -n "$PROJECT_MD" ]    && CONTEXT="${CONTEXT}${PROJECT_MD}"
  [ -n "$DOMAINS" ] && CONTEXT="${CONTEXT}
**Active domains:** ${DOMAINS}
"
  [ -n "$MISTAKES_MD" ]   && CONTEXT="${CONTEXT}
## Active Mistakes
${MISTAKES_MD}"
  [ -n "$FEEDBACK_MD" ]   && CONTEXT="${CONTEXT}
## Feedback
${FEEDBACK_MD}"
  [ -n "$KNOWLEDGE_MD" ]  && CONTEXT="${CONTEXT}
## Knowledge
${KNOWLEDGE_MD}"
  [ -n "$STRATEGIES_MD" ] && CONTEXT="${CONTEXT}
## Strategies
${STRATEGIES_MD}"
  [ -n "$DECISIONS_MD" ]  && CONTEXT="${CONTEXT}
## Decisions
${DECISIONS_MD}"
  [ -n "$NOTES_MD" ]      && CONTEXT="${CONTEXT}
## Notes
${NOTES_MD}"
  [ -n "$CLUSTER_MD" ]    && CONTEXT="${CONTEXT}
## Related
${CLUSTER_MD}"
  [ -n "$CONTRADICTS_WARNINGS" ] && CONTEXT="${CONTEXT}
${CONTRADICTS_WARNINGS}"
}

# --- apply_cascade_markers ---
apply_cascade_markers() {
  [ -f "$WAL_FILE" ] || return 0
  local _CASCADE_CUTOFF
  if [[ "$OSTYPE" == darwin* ]]; then
    _CASCADE_CUTOFF=$(date -v-14d +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
  else
    _CASCADE_CUTOFF=$(date -d "14 days ago" +%Y-%m-%d 2>/dev/null || echo "0000-00-00")
  fi
  local _CASCADE_MAP
  _CASCADE_MAP=$(awk -F'|' -v cutoff="$_CASCADE_CUTOFF" '
    $1 >= cutoff && $2 == "cascade-review" {
      slug = $3
      parent = $4
      sub(/^parent:/, "", parent)
      date = $1
      if (!(slug in best) || date > best_date[slug]) {
        best[slug] = parent
        best_date[slug] = date
      }
    }
    END {
      for (s in best) printf "%s|%s|%s\n", s, best[s], best_date[s]
    }
  ' "$WAL_FILE" 2>/dev/null)

  [ -z "$_CASCADE_MAP" ] && return 0
  local _cr_slug _cr_parent _cr_date _cr_marker
  # SECURITY (audit-2026-04-16 S3, CWE-94): _cr_parent originates from WAL
  # field `parent:NAME`. A `/` in NAME would terminate the `s///` delimiter
  # early when interpolated into a shell-built perl script, wiping CONTEXT
  # for the entire session. Pass slug and marker to perl via -s so they are
  # perl-side variables, never textually spliced into the s/// expression.
  while IFS='|' read -r _cr_slug _cr_parent _cr_date; do
    [ -z "$_cr_slug" ] && continue
    _cr_marker="[REVIEW: ${_cr_parent} updated ${_cr_date}]"
    CONTEXT=$(printf '%s' "$CONTEXT" | perl -s -pe 's/^(### \Q$slug\E)( |$)/$1 $marker$2/' -- -slug="$_cr_slug" -marker="$_cr_marker" 2>/dev/null)
  done <<< "$_CASCADE_MAP"
}
