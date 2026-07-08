package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/migrate"
)

// runMigrate: memoryctl migrate --dry-run | --execute | --rollback
func runMigrate(args []string) {
	mode := ""
	for _, a := range args {
		switch a {
		case "--dry-run", "--execute", "--rollback":
			mode = a
		case "-h", "--help":
			fmt.Print(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "memoryctl migrate: unknown flag %q\n", a)
			os.Exit(2)
		}
	}
	if mode == "" {
		fmt.Fprintln(os.Stderr, "memoryctl migrate: one of --dry-run | --execute | --rollback required")
		os.Exit(2)
	}
	osHome := filepath.Dir(claudeDir())
	v1 := memoryDir()
	t := today()
	backup := filepath.Join(claudeDir(), "memory.v1-backup-"+t)

	if mode == "--rollback" {
		// Rollback must find the backup from WHENEVER the migration ran, not
		// today — deriving the path from today's date meant rollback only
		// worked on the migration day (review G1). Pick the newest backup;
		// the YYYY-MM-DD suffix makes lexical order chronological.
		rb, err := latestBackup(claudeDir())
		if err != nil {
			fmt.Fprintf(os.Stderr, "rollback failed: %v\n", err)
			os.Exit(1)
		}
		if err := migrate.Rollback(migrate.ExecOpts{V1Dir: v1, BackupDir: rb, MemoryDir: v1}); err != nil {
			fmt.Fprintf(os.Stderr, "rollback failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("rolled back from %s\n", rb)
		return
	}

	exec := migrate.ExecOpts{V1Dir: v1, BackupDir: backup, MemoryDir: v1}

	p, err := migrate.BuildPlan(migrate.Opts{
		V1Dir: v1, GlobalDir: filepath.Join(osHome, ".claude", "memory-global"),
		OSHome: osHome, Projects: loadProjects(v1), Today: t,
		WALPath: filepath.Join(v1, ".wal"),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: plan: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Migration plan: %d keep, %d prune (backup → %s)\n", p.Kept, p.Pruned, backup)
	for _, c := range p.Convs {
		fmt.Printf("  %-6s %-40s %s\n", c.Action, c.Slug, c.Reason)
	}
	if len(p.GlobalFallbacks) > 0 {
		fmt.Fprintln(os.Stderr, "WARNING: some projects routed to global (not in projects.json):")
		for proj, n := range p.GlobalFallbacks {
			fmt.Fprintf(os.Stderr, "  project %q: %d file(s) → global (add to projects.json before --execute to preserve project scoping)\n", proj, n)
		}
	}
	if mode == "--dry-run" {
		fmt.Println("(dry-run — nothing written)")
		return
	}
	rebuilt, err := migrate.Execute(p, exec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: execute: %v\n", err)
		os.Exit(1)
	}
	if rebuilt {
		fmt.Println("Migration complete. Old store backed up; native files written; sidecar rebuilt.")
	} else {
		fmt.Println("Migration complete. Old store backed up; native files written.")
		fmt.Fprintln(os.Stderr, "WARNING: sidecar rebuild did not succeed — run `memoryctl sidecar rebuild` before your next session.")
	}
}

// latestBackup returns the newest memory.v1-backup-* directory under claudeDir.
// The YYYY-MM-DD suffix sorts lexically in chronological order, so the last
// entry is the most recent migration's backup.
func latestBackup(claudeDir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(claudeDir, "memory.v1-backup-*"))
	if err != nil {
		return "", fmt.Errorf("rollback: glob backups: %w", err)
	}
	newest := ""
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && fi.IsDir() && m > newest {
			newest = m
		}
	}
	if newest == "" {
		return "", fmt.Errorf("rollback: no memory.v1-backup-* directory under %s (nothing to roll back to)", claudeDir)
	}
	return newest, nil
}

func loadProjects(memDir string) map[string]string {
	b, err := os.ReadFile(filepath.Join(memDir, "projects.json"))
	if err != nil {
		return nil
	}
	var m map[string]string
	_ = json.Unmarshal(b, &m)
	return m
}
