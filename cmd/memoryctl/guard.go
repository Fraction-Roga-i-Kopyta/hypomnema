package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/secrets"
)

// runGuard: memoryctl guard — PreToolUse(Write). Blocks (exit 2) a memory
// write that embeds a plaintext credential. Fail-open: anything uncertain
// → exit 0 (allow). The only non-zero exit is a definite secret hit.
func runGuard(args []string) {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Print(usage)
			return
		}
		fmt.Fprintf(os.Stderr, "memoryctl guard: unknown flag %q\n", a)
		os.Exit(2)
	}
	raw, _ := io.ReadAll(os.Stdin)
	var in struct {
		ToolInput struct {
			FilePath  string `json:"file_path"`
			Content   string `json:"content"`
			NewString string `json:"new_string"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		os.Exit(0)
	}
	if os.Getenv("HYPOMNEMA_ALLOW_SECRETS") == "1" {
		os.Exit(0)
	}
	fp := in.ToolInput.FilePath
	if fp == "" {
		os.Exit(0)
	}
	memDir := memoryDir()
	if fp != memDir && !strings.HasPrefix(fp, memDir+"/") {
		os.Exit(0)
	}
	rel := strings.TrimPrefix(fp, memDir+"/")
	if secrets.IgnoreMatch(memDir, rel) {
		os.Exit(0)
	}
	content := in.ToolInput.Content
	if content == "" {
		content = in.ToolInput.NewString
	}
	if content == "" {
		os.Exit(0)
	}
	hits := secrets.Scan(content)
	if len(hits) == 0 {
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "Blocked: %s would write a plaintext secret-looking token.\n", fp)
	fmt.Fprintln(os.Stderr, "Matches (line : fragment):")
	for _, h := range hits {
		fmt.Fprintf(os.Stderr, "  %s\n", h)
	}
	fmt.Fprintln(os.Stderr, "If intentional, set HYPOMNEMA_ALLOW_SECRETS=1 for this single invocation and retry.")
	os.Exit(2)
}
