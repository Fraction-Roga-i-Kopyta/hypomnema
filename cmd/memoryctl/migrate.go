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
	backup := filepath.Join(claudeDir(), "memory.v1-backup")
	exec := migrate.ExecOpts{V1Dir: v1, BackupDir: backup, MemoryDir: v1}

	if mode == "--rollback" {
		if err := migrate.Rollback(exec); err != nil {
			fmt.Fprintf(os.Stderr, "rollback failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("rolled back from %s\n", backup)
		return
	}

	p, err := migrate.BuildPlan(migrate.Opts{
		V1Dir: v1, GlobalDir: filepath.Join(osHome, ".claude", "memory-global"),
		OSHome: osHome, Projects: loadProjects(v1), Today: today(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: plan: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Migration plan: %d keep, %d prune (backup → %s)\n", p.Kept, p.Pruned, backup)
	for _, c := range p.Convs {
		fmt.Printf("  %-6s %-40s %s\n", c.Action, c.Slug, c.Reason)
	}
	if mode == "--dry-run" {
		fmt.Println("(dry-run — nothing written)")
		return
	}
	if err := migrate.Execute(p, exec); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: execute: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Migration complete. Old store backed up; native files written; sidecar rebuilt.")
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
