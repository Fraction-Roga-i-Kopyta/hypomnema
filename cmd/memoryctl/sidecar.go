package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

// sidecarPath is the derived SQLite projection, alongside the WAL + fts index.
func sidecarPath() string { return filepath.Join(memoryDir(), ".sidecar.db") }

// collectNative lists the resolved store's files plus the global store.
func collectNative(st native.Store) []native.MemFile {
	return native.Collect(claudeDir(), st)
}

func runSidecarRebuild(args []string) {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Print(usage)
			return
		}
		fmt.Fprintf(os.Stderr, "memoryctl sidecar rebuild: unknown flag %q\n", a)
		os.Exit(2)
	}
	r := resolveStore("")
	files := collectNative(r.Store)
	s, err := sidecar.Open(sidecarPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "sidecar rebuild: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()
	if err := sidecar.Reproject(s, files, filepath.Join(memoryDir(), ".wal"), r.Scope()); err != nil {
		fmt.Fprintf(os.Stderr, "sidecar rebuild: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("rebuilt %s: %d file(s)\n", sidecarPath(), len(files))
}

func runSidecarShow(args []string) {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Print(usage)
			return
		}
		fmt.Fprintf(os.Stderr, "memoryctl sidecar show: unknown flag %q\n", a)
		os.Exit(2)
	}
	s, err := sidecar.Open(sidecarPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "sidecar show: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()
	recs, err := s.All()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sidecar show: %v\n", err)
		os.Exit(1)
	}
	for _, r := range recs {
		useful := r.LastUseful
		if useful == "" {
			useful = "-"
		}
		fmt.Printf("%-40s type=%-10s ref=%-3d eff=%.2f status=%s useful=%s\n",
			r.Slug, r.Type, r.RefCount, r.Effectiveness, r.Status, useful)
	}
}
