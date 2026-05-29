package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/inject"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/wal"
)

type hookStdin struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	Prompt    string `json:"prompt"`
}

func runInject(args []string) {
	event := "SessionStart"
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--event="):
			event = strings.TrimPrefix(a, "--event=")
		case a == "-h" || a == "--help":
			fmt.Print(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "memoryctl inject: unknown flag %q\n", a)
			os.Exit(2)
		}
	}
	if event != "SessionStart" && event != "UserPromptSubmit" {
		event = "SessionStart"
	}
	raw, _ := io.ReadAll(os.Stdin)
	var in hookStdin
	if err := json.Unmarshal(raw, &in); err != nil {
		os.Exit(0) // fail-safe
	}
	res, err := inject.Run(inject.Input{
		Event: event, SessionID: in.SessionID, CWD: in.CWD, Prompt: in.Prompt,
		ClaudeHome: claudeDir(), MemoryDir: memoryDir(), Today: today(), MaxK: 8,
	})
	if err != nil {
		os.Exit(0)
	}
	if len(res.Injected) > 0 && in.SessionID != "" {
		persistInjected(res.Injected, in.SessionID)
	}
	emitEnvelope(event, res.Markdown)
	os.Exit(0)
}

func persistInjected(slugs []string, sessionID string) {
	day := today()
	sid := wal.SanitizeField(sessionID)
	for _, slug := range slugs {
		line := fmt.Sprintf("%s|inject|%s|%s", day, wal.SanitizeField(slug), sid)
		wal.Append(memoryDir(), line, "")
	}
	runtimeDir := filepath.Join(memoryDir(), ".runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(runtimeDir, "injected-"+sessionID+".list"),
		[]byte(strings.Join(slugs, "\n")+"\n"), 0o600)
}

func emitEnvelope(event, markdown string) {
	type hookOut struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	}
	env := struct {
		HookSpecificOutput hookOut `json:"hookSpecificOutput"`
	}{HookSpecificOutput: hookOut{HookEventName: event, AdditionalContext: markdown}}
	b, err := json.Marshal(env)
	if err != nil {
		return
	}
	fmt.Println(string(b))
}
