#!/bin/bash
# memory-fts-sync.sh — lazy FTS5 index reconciliation
#
# Keeps ~/.claude/memory/index.db in sync with ~/.claude/memory/**/*.md.
# Safe to call before any FTS5 query. Two-stage:
#   1. drift check via SUM(mtime) + COUNT(*) — microseconds if nothing changed
#   2. diff reconciliation via temp table + FTS5 delete/insert — ~100ms for ~100 files

set -euo pipefail

MEM="${CLAUDE_MEMORY_DIR:-$HOME/.claude/memory}"
DB="$MEM/index.db"

mkdir -p "$(dirname "$DB")"

# Idempotent schema
sqlite3 "$DB" <<'SQL' >/dev/null
CREATE VIRTUAL TABLE IF NOT EXISTS mem USING fts5(
  path UNINDEXED,
  content,
  mtime UNINDEXED,
  tokenize='trigram'
);
SQL

# --- Stage 1: drift check (cheap) ---
# SUM(mtime) shifts for any add/remove/modify. COUNT guards the rare
# case where two files swap mtimes and the sum is preserved.
disk_sum=$(find "$MEM" -name '*.md' -type f -exec stat -f '%m' {} + 2>/dev/null | awk '{s+=$1} END{print s+0}')
disk_count=$(find "$MEM" -name '*.md' -type f 2>/dev/null | wc -l | tr -d ' ')

read -r idx_sum idx_count <<<"$(sqlite3 -separator ' ' "$DB" \
  "SELECT IFNULL(SUM(CAST(mtime AS INTEGER)),0), COUNT(*) FROM mem;")"

if [[ "$disk_sum" == "$idx_sum" && "$disk_count" == "$idx_count" ]]; then
  exit 0
fi

# --- Stage 2: reconcile (drift detected) ---
DISK_LIST=$(mktemp)
trap 'rm -f "$DISK_LIST"' EXIT

# pipe delimiter: path collisions with '|' are impossible under ~/.claude/memory
find "$MEM" -name '*.md' -type f -exec stat -f '%N|%m' {} + > "$DISK_LIST"

sqlite3 "$DB" <<SQL
.output /dev/null
PRAGMA busy_timeout = 5000;
.output stdout
BEGIN IMMEDIATE;

CREATE TEMP TABLE disk(path TEXT PRIMARY KEY, mtime INTEGER);
.mode list
.separator |
.import '$DISK_LIST' disk

-- 1. Drop entries whose files are gone
DELETE FROM mem WHERE path NOT IN (SELECT path FROM disk);

-- 2. Drop entries with stale mtime (FTS5 has no UPDATE for UNINDEXED cols)
DELETE FROM mem WHERE path IN (
  SELECT d.path
  FROM disk d JOIN mem m ON m.path = d.path
  WHERE d.mtime > CAST(m.mtime AS INTEGER)
);

-- 3. Insert missing rows (new files + re-inserts from step 2)
INSERT INTO mem(path, content, mtime)
SELECT d.path, readfile(d.path), d.mtime
FROM disk d
WHERE d.path NOT IN (SELECT path FROM mem);

COMMIT;
SQL
