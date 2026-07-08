package migrate

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

// ExecOpts configures Execute / Rollback.
type ExecOpts struct {
	V1Dir     string
	BackupDir string
	MemoryDir string // where the WAL + sidecar live (== V1Dir in the normal case)
}

// Execute applies a plan: write kept native files, rebuild the sidecar from
// the (preserved) WAL + native, and back up the v1 store. Idempotent: writing
// the same native files again is a no-op overwrite; backup is skipped if it
// already exists. The old store is NEVER deleted — only copied to BackupDir.
func Execute(p Plan, o ExecOpts) (sidecarRebuilt bool, err error) {
	// 1. Back up the v1 store (once).
	if _, err := os.Stat(o.BackupDir); os.IsNotExist(err) {
		if err := copyTree(o.V1Dir, o.BackupDir); err != nil {
			return false, fmt.Errorf("migrate.Execute: backup: %w", err)
		}
	}
	// 2. Write kept native files.
	for _, c := range p.Convs {
		if c.Action != "keep" {
			continue
		}
		if err := os.MkdirAll(c.TargetDir, 0o755); err != nil {
			return false, fmt.Errorf("migrate.Execute: mkdir %s: %w", c.TargetDir, err)
		}
		if err := writeFileAtomic(filepath.Join(c.TargetDir, c.Slug), []byte(c.Native), 0o644); err != nil {
			return false, fmt.Errorf("migrate.Execute: write %s: %w", c.Slug, err)
		}
	}
	// 3. Rebuild the sidecar from the preserved WAL + the new native files.
	// The sidecar is a rebuildable projection, so a failure here does NOT fail
	// the migration (files are already written + backed up) — but it must be
	// SURFACED, not swallowed, so the CLI doesn't claim "sidecar rebuilt" when
	// it wasn't (review: migrate swallows sidecar rebuild error).
	files := allNative(p)
	s, err := sidecar.Open(filepath.Join(o.MemoryDir, ".sidecar.db"))
	if err != nil {
		return false, nil
	}
	defer s.Close()
	// nil scope: the reconciliation scope derives from the migrated files'
	// own project tags — a one-shot migration owns everything it wrote.
	if rerr := sidecar.Reproject(s, files, filepath.Join(o.MemoryDir, ".wal"), nil); rerr != nil {
		return false, nil
	}
	return true, nil
}

// Rollback restores the v1 store from BackupDir (the new native dirs are left
// in place — harmless; the user re-points settings.json back to v1 hooks).
func Rollback(o ExecOpts) error {
	if _, err := os.Stat(o.BackupDir); err != nil {
		return fmt.Errorf("migrate.Rollback: no backup at %s: %w", o.BackupDir, err)
	}
	return copyTree(o.BackupDir, o.V1Dir)
}

func allNative(p Plan) []native.MemFile {
	var out []native.MemFile
	for _, c := range p.Convs {
		if c.Action != "keep" {
			continue
		}
		mf, _ := parseNativeContent(c.Slug, c.Native)
		out = append(out, mf)
	}
	return out
}

// parseNativeContent extracts a MemFile from converted native content (reuses
// the same minimal frontmatter shape native.List would read off disk).
func parseNativeContent(slug, content string) (native.MemFile, error) {
	fm, body := splitV1(content) // same flat parser; native frontmatter is a subset
	return native.MemFile{Slug: slug, Name: fm["name"], Description: fm["description"], Type: fm["type"], Body: body}, nil
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return writeFileAtomic(target, b, info.Mode())
	})
}

// writeFileAtomic writes data via a same-dir temp file + rename so a crash
// mid-write never leaves a truncated memory file (invariant I3).
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
