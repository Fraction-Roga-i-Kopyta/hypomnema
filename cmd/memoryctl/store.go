package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/pathutil"
)

// anchorKind records where the project anchor came from.
type anchorKind string

const (
	anchorExplicit anchorKind = "CLAUDE_PROJECT_CWD" // hypomnema-only manual/test override
	anchorHarness  anchorKind = "CLAUDE_PROJECT_DIR" // set by Claude Code for every hook
	anchorPin      anchorKind = "session-pin"        // CLI verb inside a session (Bash tool)
	anchorCWD      anchorKind = "cwd"                // last resort: drifts with cd
)

// resolved is a store plus the anchor it was resolved from.
type resolved struct {
	native.Store
	Anchor string
	Kind   anchorKind
}

// resolveStore resolves this invocation's native store. Order:
// CLAUDE_PROJECT_DIR (every hook carries it; it wins so a globally exported
// CLAUDE_PROJECT_CWD cannot collapse sessions into one store) →
// CLAUDE_PROJECT_CWD (hypomnema-only override for CLI use) → the session pin
// written at SessionStart (found via HYPOMNEMA_SESSION_ID /
// CLAUDE_CODE_SESSION_ID, which the Bash tool exports) → stdinCWD →
// os.Getwd().
func resolveStore(stdinCWD string) resolved {
	if d := os.Getenv("CLAUDE_PROJECT_DIR"); d != "" {
		return resolved{Store: native.StoreFor(configDir(), d), Anchor: d, Kind: anchorHarness}
	}
	if d := os.Getenv("CLAUDE_PROJECT_CWD"); d != "" {
		return resolved{Store: native.StoreFor(configDir(), d), Anchor: d, Kind: anchorExplicit}
	}
	if r, ok := readPin(sessionIDFromEnv()); ok {
		return r
	}
	cwd := stdinCWD
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return resolved{Store: native.StoreFor(configDir(), cwd), Anchor: cwd, Kind: anchorCWD}
}

func sessionIDFromEnv() string {
	if s := os.Getenv("HYPOMNEMA_SESSION_ID"); s != "" {
		return s
	}
	return os.Getenv("CLAUDE_CODE_SESSION_ID")
}

func pinPath(sessionID string) string {
	return filepath.Join(memoryDir(), ".runtime", "project-"+pathutil.SafeFileName(sessionID)+".json")
}

type pinFile struct {
	Dir     string `json:"dir"`
	Project string `json:"project"`
	Root    string `json:"root"`
	Source  string `json:"source"`
	Anchor  string `json:"anchor"`
}

// writePin records the session's store so CLI verbs run later in the session
// resolve the same store without CLAUDE_PROJECT_DIR. Best-effort, atomic.
func writePin(sessionID string, r resolved) {
	if sessionID == "" || r.Dir == "" {
		return
	}
	if err := os.MkdirAll(filepath.Join(memoryDir(), ".runtime"), 0o755); err != nil {
		return
	}
	b, err := json.Marshal(pinFile{Dir: r.Dir, Project: r.Project, Root: r.Root, Source: r.Source, Anchor: r.Anchor})
	if err != nil {
		return
	}
	_ = pathutil.WriteFileAtomic(pinPath(sessionID), b, 0o600)
}

// readPin loads a pin; missing, unparsable or implausible pins are ignored.
func readPin(sessionID string) (resolved, bool) {
	if sessionID == "" {
		return resolved{}, false
	}
	b, err := os.ReadFile(pinPath(sessionID))
	if err != nil {
		return resolved{}, false
	}
	var p pinFile
	if json.Unmarshal(b, &p) != nil || p.Project == "" || !filepath.IsAbs(p.Dir) {
		return resolved{}, false
	}
	return resolved{
		Store:  native.Store{Dir: p.Dir, Project: p.Project, Root: p.Root, Source: p.Source},
		Anchor: p.Anchor, Kind: anchorPin,
	}, true
}
