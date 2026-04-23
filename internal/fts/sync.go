package fts

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Sync brings the FTS5 index into line with the on-disk markdown tree rooted
// at memoryDir. Two stages, mirroring bin/memory-fts-sync.sh:
//
//  1. Drift check: SUM(mtime)+COUNT(*) on disk vs index. Microseconds when nothing changed.
//  2. Reconciliation: delete missing, delete stale, insert new. Transactional.
//
// Returns early if no drift is detected — that's the hot path, invoked before
// every query.
func (ix *Index) Sync(ctx context.Context, memoryDir string) error {
	type diskEntry struct {
		path  string
		mtime int64
	}

	// --- Stage 1: cheap drift check ---
	var disk []diskEntry
	var diskSum int64
	err := filepath.WalkDir(memoryDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// Broken symlinks or unreadable dirs don't abort the sync —
			// bash version also swallows find errors via 2>/dev/null.
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		// Auto-generated derivative views that live at memory root MUST
		// NOT enter the FTS index. Indexing them closes a strange-loop:
		// shadow surfaces "0% precision" from self-profile, Claude treats
		// it as knowledge, writes regen'd memory, self-profile re-indexed.
		// Filter by basename rather than path prefix because the memory
		// root location is caller-controlled via $CLAUDE_MEMORY_DIR.
		if isDerivativeView(filepath.Base(p)) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		m := info.ModTime().Unix()
		disk = append(disk, diskEntry{path: p, mtime: m})
		diskSum += m
		return nil
	})
	if err != nil {
		return fmt.Errorf("fts.Sync: walk: %w", err)
	}

	var idxSum int64
	var idxCount int
	_ = ix.db.QueryRowContext(ctx, `
		SELECT IFNULL(SUM(CAST(mtime AS INTEGER)), 0), COUNT(*) FROM mem
	`).Scan(&idxSum, &idxCount)

	if diskSum == idxSum && len(disk) == idxCount {
		return nil // no drift
	}

	// --- Stage 2: reconcile ---
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("fts.Sync: begin: %w", err)
	}
	defer tx.Rollback()

	// Populate a temp table with the current on-disk inventory. We can't use
	// the `.import` dot-command over database/sql, so load via parametrised
	// inserts inside a transaction — still fast for the few-hundred-file
	// sizes a personal memory store reaches.
	if _, err := tx.ExecContext(ctx, `
		CREATE TEMP TABLE IF NOT EXISTS disk(path TEXT PRIMARY KEY, mtime INTEGER)
	`); err != nil {
		return fmt.Errorf("fts.Sync: create temp: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM disk"); err != nil {
		return fmt.Errorf("fts.Sync: clear temp: %w", err)
	}
	insertDisk, err := tx.PrepareContext(ctx, `INSERT INTO disk(path, mtime) VALUES(?, ?)`)
	if err != nil {
		return fmt.Errorf("fts.Sync: prepare insert-disk: %w", err)
	}
	defer insertDisk.Close()
	for _, d := range disk {
		if _, err := insertDisk.ExecContext(ctx, d.path, d.mtime); err != nil {
			return fmt.Errorf("fts.Sync: insert-disk: %w", err)
		}
	}

	// 1. Drop entries whose files are gone.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM mem WHERE path NOT IN (SELECT path FROM disk)
	`); err != nil {
		return fmt.Errorf("fts.Sync: delete missing: %w", err)
	}
	// 2. Drop entries whose mtime on disk is newer (FTS5 has no UPDATE for UNINDEXED cols).
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM mem WHERE path IN (
			SELECT d.path FROM disk d
			JOIN mem m ON m.path = d.path
			WHERE d.mtime > CAST(m.mtime AS INTEGER)
		)
	`); err != nil {
		return fmt.Errorf("fts.Sync: delete stale: %w", err)
	}
	// 3. Insert missing rows (new files + re-inserts from step 2).
	// readfile() is not available through the pure-Go SQLite driver; read the
	// content in Go and bind it. This is the one place we diverge from the
	// bash pipeline on the SQL side — semantically identical result.
	insertMem, err := tx.PrepareContext(ctx, `
		INSERT INTO mem(path, content, mtime) VALUES(?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("fts.Sync: prepare insert-mem: %w", err)
	}
	defer insertMem.Close()

	rows, err := tx.QueryContext(ctx, `
		SELECT path, mtime FROM disk WHERE path NOT IN (SELECT path FROM mem)
	`)
	if err != nil {
		return fmt.Errorf("fts.Sync: select missing: %w", err)
	}
	var toInsert []diskEntry
	for rows.Next() {
		var d diskEntry
		if err := rows.Scan(&d.path, &d.mtime); err != nil {
			rows.Close()
			return fmt.Errorf("fts.Sync: scan missing: %w", err)
		}
		toInsert = append(toInsert, d)
	}
	rows.Close()

	for _, d := range toInsert {
		content, err := os.ReadFile(d.path)
		if err != nil {
			// File deleted mid-sync — skip it. The next sync will catch up.
			continue
		}
		if _, err := insertMem.ExecContext(ctx, d.path, string(content), d.mtime); err != nil {
			return fmt.Errorf("fts.Sync: insert: %w", err)
		}
	}

	return tx.Commit()
}

// isDerivativeView returns true for auto-generated markdown files that
// hypomnema writes at memory root and that must not enter the FTS index.
// Keep this list in sync with bin/memory-fts-sync.sh's `_EXCLUDES` array.
func isDerivativeView(base string) bool {
	switch base {
	case "self-profile.md", "_agent_context.md", "MEMORY.md":
		return true
	}
	return false
}
