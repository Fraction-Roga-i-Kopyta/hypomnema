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
func Execute(p Plan, o ExecOpts) error {
	// 1. Back up the v1 store (once).
	if _, err := os.Stat(o.BackupDir); os.IsNotExist(err) {
		if err := copyTree(o.V1Dir, o.BackupDir); err != nil {
			return fmt.Errorf("migrate.Execute: backup: %w", err)
		}
	}
	// 2. Write kept native files.
	for _, c := range p.Convs {
		if c.Action != "keep" {
			continue
		}
		if err := os.MkdirAll(c.TargetDir, 0o755); err != nil {
			return fmt.Errorf("migrate.Execute: mkdir %s: %w", c.TargetDir, err)
		}
		if err := os.WriteFile(filepath.Join(c.TargetDir, c.Slug), []byte(c.Native), 0o644); err != nil {
			return fmt.Errorf("migrate.Execute: write %s: %w", c.Slug, err)
		}
	}
	// 3. Rebuild the sidecar from the preserved WAL + the new native files.
	files := allNative(p)
	s, err := sidecar.Open(filepath.Join(o.MemoryDir, ".sidecar.db"))
	if err == nil {
		// nil scope: the reconciliation scope derives from the migrated files'
		// own project tags — a one-shot migration owns everything it wrote.
		_ = sidecar.Reproject(s, files, filepath.Join(o.MemoryDir, ".wal"), nil)
		s.Close()
	}
	return nil
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
		return os.WriteFile(target, b, info.Mode())
	})
}
