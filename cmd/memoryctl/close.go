package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/closer"
)

type stopStdin struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	TranscriptPath string `json:"transcript_path"`
}

// runClose: memoryctl close — the Stop hook (and, with --subagent, the
// SubagentStop hook). Reads the envelope from stdin, exits 0 always
// (fail-safe).
func runClose(args []string) {
	subagent := false
	for _, a := range args {
		switch a {
		case "-h", "--help":
			fmt.Print(usage)
			return
		case "--subagent":
			subagent = true
		default:
			fmt.Fprintf(os.Stderr, "memoryctl close: unknown flag %q\n", a)
			os.Exit(2)
		}
	}
	raw, _ := io.ReadAll(os.Stdin)
	if subagent {
		runSubagentStop(raw)
		os.Exit(0)
	}
	var in stopStdin
	if err := json.Unmarshal(raw, &in); err != nil {
		os.Exit(0)
	}
	_, _ = closer.Run(closer.Input{
		SessionID: in.SessionID, CWD: in.CWD, TranscriptPath: in.TranscriptPath,
		ClaudeHome: claudeDir(), MemoryDir: memoryDir(), Today: today(),
		Store: resolveStore(in.CWD).Store,
	})

	// Age out stale active-skill markers (bounded cleanup, best-effort).
	runtimeDir := filepath.Join(memoryDir(), ".runtime")
	if entries, err := os.ReadDir(runtimeDir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "active-skill-") {
				if info, ierr := e.Info(); ierr == nil && time.Since(info.ModTime()) > 24*time.Hour {
					_ = os.Remove(filepath.Join(runtimeDir, e.Name()))
				}
			}
		}
	}

	os.Exit(0)
}
