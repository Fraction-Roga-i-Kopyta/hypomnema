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

// projectCWD resolves the working directory used for the per-project native
// memory dir. The hook contract exports $CLAUDE_PROJECT_CWD; fall back to the
// process cwd so the verb is usable standalone.
func projectCWD() string {
	if d := os.Getenv("CLAUDE_PROJECT_CWD"); d != "" {
		return d
	}
	cwd, _ := os.Getwd()
	return cwd
}

// collectNative lists native files from both the per-project dir and the
// hypomnema-owned global dir (native lacks a global store), tagged with
// their owning project. claudeDir() honours $CLAUDE_HOME, which keeps the
// dir resolution overridable in tests.
func collectNative() ([]native.MemFile, error) {
	return native.Collect(claudeDir(), projectCWD()), nil
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
	files, err := collectNative()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sidecar rebuild: %v\n", err)
		os.Exit(1)
	}
	s, err := sidecar.Open(sidecarPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "sidecar rebuild: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()
	if err := sidecar.Reproject(s, files, filepath.Join(memoryDir(), ".wal"), native.Scope(projectCWD())); err != nil {
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
		fmt.Printf("%-40s type=%-10s ref=%-3d eff=%.2f status=%s\n",
			r.Slug, r.Type, r.RefCount, r.Effectiveness, r.Status)
	}
}
