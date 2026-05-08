#!/bin/bash
# scripts/parity-check.sh — runs bash and Go shadow on identical fixtures,
# diffs the resulting WAL event(s). Prints PASS/FAIL with actionable diff
# on mismatch. Invoked by `make parity` and by the CI job.
#
# Contract: both implementations must produce the same shadow-miss events
# (same slugs, same session id) for the same (fixture + prompt + injected)
# tuple. BM25 tie-breaking may differ between implementations; the fixture
# is small enough that scoring is deterministic.
set -euo pipefail

REPO=$(cd "$(dirname "$0")/.." && pwd)
BASH_SHADOW="$REPO/bin/memory-fts-shadow.sh"
GO_SHADOW="$REPO/bin/memoryctl"

[ -x "$BASH_SHADOW" ] || { echo "missing $BASH_SHADOW"; exit 1; }
[ -x "$GO_SHADOW"   ] || { echo "missing $GO_SHADOW (run: make build)"; exit 1; }

# Bash wal-lock.sh must be reachable at $HOME/.claude/hooks/lib/ — that's
# how the shadow script sources it. Install.sh makes that symlink; here
# we tolerate missing installs by seeding a throwaway HOME.
if [ ! -f "$HOME/.claude/hooks/lib/wal-lock.sh" ]; then
  echo "note: $HOME/.claude/hooks/lib/wal-lock.sh missing — run ./install.sh first for an honest parity check." >&2
  exit 2
fi

# _seed_default_fixture: the canonical fixture used by the original four
# parity cases — two markdown files (a portability mistake + a feedback
# rule) under $1=memdir.
_seed_default_fixture() {
  local MEM="$1"
  cat > "$MEM/mistakes/sed-bsd-gnu.md" <<'EOF'
---
type: mistake
status: active
severity: major
---
BSD sed behaves differently from GNU sed on 1,/pat/ range addresses.
EOF
  cat > "$MEM/feedback/portability-rule.md" <<'EOF'
---
type: feedback
status: active
---
Always test shell scripts under both BSD and GNU coreutils before release.
EOF
}

# _seed_edge_slug_pipe_fixture: legitimate slugs alongside one whose
# basename includes characters the WAL grammar treats as separators
# (`|` and `\n`). Post-v1.0.1, `pathutil.SlugFromPath` replaces those
# with `_` before WAL emission; this fixture verifies bash and Go agree
# on the sanitised slug. The actual filename on disk uses safe ASCII —
# we synthesise the hostile path by writing a file whose stem looks
# malicious; bash and Go must both swap the separator chars out before
# the slug reaches `.wal`.
_seed_edge_slug_pipe_fixture() {
  local MEM="$1"
  _seed_default_fixture "$MEM"
  # Create a file whose stem contains chars that would break WAL grammar
  # if not sanitised. Filesystem allows `|` in names on macOS/Linux.
  cat > "$MEM/mistakes/foo|outcome-positive|bar.md" <<'EOF'
---
type: mistake
status: active
severity: major
---
Hostile slug — pretends to inject a fake outcome-positive event via
filename. Sanitiser must replace `|` with `_` before WAL write.
EOF
}

# _seed_large_wal_fixture: pre-fill WAL with > 256 KiB of historical
# events. The dedup tail-cap (`internal/wal/append.go::dedupTailBytes`)
# only scans the trailing 256 KiB; bash side scans the whole WAL.
# Parity holds because the tail-cap optimisation is observationally
# equivalent in this fixture: the duplicate would-be-emit lives inside
# the tail window, so both implementations correctly suppress it.
_seed_large_wal_fixture() {
  local MEM="$1"
  _seed_default_fixture "$MEM"
  # ~260 KiB of synthetic but valid WAL events (slug rotation across
  # five existing fixture filenames so dedup keys vary).
  local i=0 lines=()
  while [ "$i" -lt 4000 ]; do
    lines+=("2026-04-01|inject|sed-bsd-gnu|sess-pad-${i}")
    i=$((i + 1))
  done
  printf '%s\n' "${lines[@]}" > "$MEM/.wal"
}

_run_case() {
  local name="$1" prompt="$2"; shift 2
  local injected="${1:-}"
  local seed_fn="${2:-_seed_default_fixture}"

  local TMP
  TMP=$(mktemp -d)
  local MEM="$TMP/memory"
  mkdir -p "$MEM"/{mistakes,feedback,strategies,knowledge,notes,seeds,.runtime}

  "$seed_fn" "$MEM"

  # Snapshot any pre-seeded .wal so Go runs against the same starting
  # state as bash (not against bash's post-run mutations). Fixtures
  # that don't pre-fill .wal get an empty INITIAL_WAL_SNAPSHOT and the
  # original behaviour is preserved.
  local INITIAL_WAL_SNAPSHOT="$TMP/.wal.initial"
  [ -f "$MEM/.wal" ] && cp "$MEM/.wal" "$INITIAL_WAL_SNAPSHOT" || : > "$INITIAL_WAL_SNAPSHOT"

  # Bash run — capture stderr so diagnostic output is available on mismatch.
  local BASH_ERR="$TMP/bash.err"
  CLAUDE_MEMORY_DIR="$MEM" bash "$BASH_SHADOW" "$prompt" "parity-$name" "$injected" >/dev/null 2>"$BASH_ERR"
  local BASH_WAL="$TMP/bash.wal"
  # diff bash-emitted events only, not the pre-seeded prefix — otherwise
  # large-WAL fixtures swamp the signal in identical filler.
  if [ -s "$INITIAL_WAL_SNAPSHOT" ]; then
    diff <(sort "$INITIAL_WAL_SNAPSHOT") <(sort "$MEM/.wal") | grep '^>' | sed 's/^> //' | sort > "$BASH_WAL" 2>/dev/null || true
  else
    sort "$MEM/.wal" > "$BASH_WAL" 2>/dev/null || true
  fi
  rm -f "$MEM/.wal" "$MEM/index.db"
  # Restore initial state so Go runs against the same starting WAL.
  cp "$INITIAL_WAL_SNAPSHOT" "$MEM/.wal"

  # Go run on the same fixture tree.
  local GO_ERR="$TMP/go.err"
  CLAUDE_MEMORY_DIR="$MEM" "$GO_SHADOW" fts shadow "$prompt" "parity-$name" "$injected" >/dev/null 2>"$GO_ERR"
  local GO_WAL="$TMP/go.wal"
  if [ -s "$INITIAL_WAL_SNAPSHOT" ]; then
    diff <(sort "$INITIAL_WAL_SNAPSHOT") <(sort "$MEM/.wal") | grep '^>' | sed 's/^> //' | sort > "$GO_WAL" 2>/dev/null || true
  else
    sort "$MEM/.wal" > "$GO_WAL" 2>/dev/null || true
  fi

  if diff -q "$BASH_WAL" "$GO_WAL" >/dev/null; then
    echo "  ✓ $name"
  else
    echo "  ✗ $name"
    echo "    -- bash wal --";  sed 's/^/    /' "$BASH_WAL"
    echo "    -- go wal --";    sed 's/^/    /' "$GO_WAL"
    if [ -s "$BASH_ERR" ]; then
      echo "    -- bash stderr --"; sed 's/^/    /' "$BASH_ERR"
    fi
    if [ -s "$GO_ERR" ]; then
      echo "    -- go stderr --"; sed 's/^/    /' "$GO_ERR"
    fi
    # Dump fixture + env state to help diagnose env-specific failures.
    echo "    -- env --"
    echo "    MEM=$MEM"
    echo "    sqlite3=$(command -v sqlite3) $(sqlite3 -version 2>/dev/null | head -1)"
    echo "    index.db after runs:"
    [ -f "$MEM/index.db" ] && sqlite3 "$MEM/index.db" "SELECT COUNT(*) FROM mem;" 2>&1 | sed 's/^/      rows=/' || echo "      (no index.db)"
    rm -rf "$TMP"
    return 1
  fi
  rm -rf "$TMP"
}

echo "=== parity: bash vs Go shadow ==="
_run_case "portability-query"       "help me with GNU sed portability"  ""
_run_case "cyrillic-query"          "помоги с портативностью sed"       ""
_run_case "dedup-injected-skipped"  "portability test"                  "sed-bsd-gnu"
_run_case "empty-prompt"            ""                                  ""
_run_case "edge-slug-pipe"          "hostile slug test query"           "" "_seed_edge_slug_pipe_fixture"
_run_case "large-wal-256k-prefilled" "shadow query against tail-cap"    "" "_seed_large_wal_fixture"

# --- self-profile parity -------------------------------------------------
# Verifies bin/memory-self-profile.sh and `memoryctl self-profile` produce
# byte-for-byte identical self-profile.md against the same fixture. Forces
# PATH to exclude ~/.claude/bin so the bash script does NOT delegate to the
# Go binary — otherwise we'd be comparing Go against itself.

_run_profile_case() {
  local name="$1"

  local TMP MEM
  TMP=$(mktemp -d)
  MEM="$TMP/memory"
  mkdir -p "$MEM"/{mistakes,strategies}

  # WAL fixture mixes format-v1 and format-v2 session-metrics rows so
  # both readers' demuxers are exercised in parity. v1 keeps domains
  # in $3 and the metrics blob in $4 with no session_id; v2 carries
  # `domains:<csv>,error_count:N,...` in $3 and session_id in $4.
  cat > "$MEM/.wal" <<'WAL'
2026-04-01|session-metrics|backend,testing|error_count:3,tool_calls:20,duration:120s
2026-04-02|session-metrics|domains:backend,error_count:0,tool_calls:10,duration:60s|sess1
2026-04-02|clean-session|unknown|sess1
2026-04-03|session-metrics|frontend|error_count:1,tool_calls:5,duration:30s
2026-04-04|outcome-positive|foo-mistake|sess2
2026-04-04|trigger-silent|foo-mistake|sess2
2026-04-04|outcome-negative|bar-mistake|sess3
2026-04-05|trigger-useful|bar-mistake|sess4
2026-04-06|strategy-used|unknown|sess5
2026-04-06|strategy-gap|unknown|sess6
2026-04-07|trigger-useful|ambient-rule|sess7
2026-04-07|trigger-silent|noise-rule|sess7
2026-04-08|evidence-empty|bar-mistake|sess8
2026-04-08|evidence-empty|baz-mistake|sess9
2026-04-09|outcome-new|new-mistake|sess10
WAL

  # Filename stem = slug — matches the WAL event for ambient-rule so both
  # bash and Go correctly classify its trigger-useful/trigger-silent hits
  # as ambient activations (not as measurable precision denominator).
  cat > "$MEM/ambient-rule.md" <<'AMB'
---
type: feedback
status: active
precision_class: ambient
---
body
AMB
  cat > "$MEM/mistakes/bar-mistake.md" <<'M1'
---
type: mistake
recurrence: 5
scope: universal
severity: major
---
body
M1
  cat > "$MEM/mistakes/foo-mistake.md" <<'M2'
---
type: mistake
recurrence: 2
scope: domain
severity: minor
---
body
M2
  cat > "$MEM/strategies/s1.md" <<'S1'
---
type: strategy
success_count: 4
---
body
S1
  cat > "$MEM/strategies/s2.md" <<'S2'
---
type: strategy
success_count: 1
---
body
S2

  # bash run: strip the memoryctl install dir from PATH so the script's
  # `exec memoryctl self-profile` delegation does NOT fire — we want the
  # shell path, not Go-against-itself.
  local PATH_NO_MEMCTL
  PATH_NO_MEMCTL=$(printf '%s' "$PATH" | tr ':' '\n' | grep -v 'claude/bin' | tr '\n' ':')
  PATH="$PATH_NO_MEMCTL" HYPOMNEMA_NOW="2026-04-22 12:34" \
    CLAUDE_MEMORY_DIR="$MEM" bash "$REPO/bin/memory-self-profile.sh"
  local BASH_OUT="$TMP/bash.md"
  mv "$MEM/self-profile.md" "$BASH_OUT"

  # Go run on the same fixture.
  HYPOMNEMA_NOW="2026-04-22 12:34" CLAUDE_MEMORY_DIR="$MEM" \
    "$GO_SHADOW" self-profile
  local GO_OUT="$TMP/go.md"
  mv "$MEM/self-profile.md" "$GO_OUT"

  if diff -q "$BASH_OUT" "$GO_OUT" >/dev/null; then
    echo "  ✓ $name"
  else
    echo "  ✗ $name"
    diff -u "$BASH_OUT" "$GO_OUT" | sed 's/^/    /'
    rm -rf "$TMP"
    return 1
  fi
  rm -rf "$TMP"
}

echo "=== parity: bash vs Go self-profile ==="
_run_profile_case "fixture-mixed-events"

# --- tfidf parity --------------------------------------------------------
# Verifies hooks/memory-index.sh (awk path) and `memoryctl tfidf rebuild`
# produce the same .tfidf-index on a Latin-only fixture. Cyrillic / CJK
# bodies are intentionally OUT of scope — the bash tokenizer drops them,
# that's the whole reason the Go port exists. The awk path delegates to
# memoryctl when it's on PATH, so force the shell path by stripping the
# install dir (same trick as the self-profile case).
#
# Per-line posting order is undefined in bash (directory-read order is
# filesystem-dependent), so normalise by expanding each "term\tp1\tp2..."
# row into "term\tp1", "term\tp2" ... pairs before sort+diff.

_run_tfidf_case() {
  local name="$1"

  local TMP MEM
  TMP=$(mktemp -d)
  MEM="$TMP/memory"
  mkdir -p "$MEM"/{mistakes,feedback,knowledge,strategies,notes}

  # Three Latin-only docs with overlapping tokens → some idf > 0.1, so
  # the emitted index isn't empty. Keywords absent so the TF-IDF path
  # actually processes them (has_kw skip otherwise).
  cat > "$MEM/mistakes/a.md" <<'EOF'
---
type: mistake
---
connection timeout when querying external api under heavy load events
EOF
  cat > "$MEM/mistakes/b.md" <<'EOF'
---
type: mistake
---
database connection pool exhausted during peak traffic events
EOF
  cat > "$MEM/mistakes/c.md" <<'EOF'
---
type: mistake
---
mocked tests passed but production deployment failed
EOF

  # Normaliser: expand each TSV row into (term, posting) pairs, sort.
  _normalize_tfidf() {
    awk -F'\t' '{ for (i=2; i<=NF; i++) print $1 "\t" $i }' "$1" | sort
  }

  # Bash path — force by stripping ~/.claude/bin from PATH.
  local PATH_NO_MEMCTL
  PATH_NO_MEMCTL=$(printf '%s' "$PATH" | tr ':' '\n' | grep -v 'claude/bin' | tr '\n' ':')
  PATH="$PATH_NO_MEMCTL" CLAUDE_MEMORY_DIR="$MEM" bash "$REPO/hooks/memory-index.sh"
  local BASH_OUT="$TMP/bash.idx"
  _normalize_tfidf "$MEM/.tfidf-index" > "$BASH_OUT"
  rm -f "$MEM/.tfidf-index"

  # Go path.
  CLAUDE_MEMORY_DIR="$MEM" "$GO_SHADOW" tfidf rebuild
  local GO_OUT="$TMP/go.idx"
  _normalize_tfidf "$MEM/.tfidf-index" > "$GO_OUT"

  if diff -q "$BASH_OUT" "$GO_OUT" >/dev/null; then
    echo "  ✓ $name"
  else
    echo "  ✗ $name"
    diff -u "$BASH_OUT" "$GO_OUT" | sed 's/^/    /'
    rm -rf "$TMP"
    return 1
  fi
  rm -rf "$TMP"
}

echo "=== parity: bash vs Go tfidf (Latin-only) ==="
_run_tfidf_case "latin-only-fixture"

echo "=== all parity cases passed ==="
