#!/bin/bash
# memory-fts-query.sh — FTS5/BM25 search over ~/.claude/memory/
#
# Usage: memory-fts-query.sh "free-form query" [limit]
# Output (TSV, sorted best-first): /abs/path/to/file.md<TAB>bm25_score
#
# Lazy-syncs the FTS5 index first (cheap if nothing changed).
# Exits 0 with no output on empty/invalid query — caller treats that as "no hits".

set -euo pipefail

QUERY="${1:?query string required as first argument}"
LIMIT="${2:-10}"
DB="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}/index.db"
BIN_DIR="$(cd "$(dirname "$0")" && pwd)"

# Keep the index honest before querying.
"$BIN_DIR/memory-fts-sync.sh" >/dev/null 2>&1 || exit 0

# Replace every non-alnum char with space (works for Cyrillic under UTF-8 locale),
# split into tokens, OR-join without phrase-quoting so trigram substring match fires.
# Case-folding is handled by FTS5 trigram tokenizer itself (Cyrillic included).
FTS_QUERY=$(LC_ALL=en_US.UTF-8 printf '%s' "$QUERY" | awk '
  BEGIN { n = 0 }
  {
    gsub(/[^[:alnum:]_]/, " ")
    m = split($0, parts, /[ \t]+/)
    for (i = 1; i <= m; i++) if (parts[i] != "") tokens[++n] = parts[i]
  }
  END {
    # Drop FTS5 reserved keywords — they hijack the query parser otherwise.
    out_i = 0
    for (i = 1; i <= n; i++) {
      tu = toupper(tokens[i])
      if (tu == "AND" || tu == "OR" || tu == "NOT" || tu == "NEAR") continue
      printf (out_i > 0 ? " OR " : "") "%s", tokens[i]
      out_i++
    }
  }
')

[ -z "$FTS_QUERY" ] && exit 0

QUERY_ESC="${FTS_QUERY//\'/\'\'}"

sqlite3 -separator $'\t' "$DB" "
  SELECT path, ROUND(bm25(mem), 3)
  FROM mem
  WHERE mem MATCH '$QUERY_ESC'
  ORDER BY bm25(mem)
  LIMIT $LIMIT;
" 2>/dev/null || true
