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
		// A relative value (e.g. "." from a shell alias) must be made
		// absolute before it reaches CanonicalRoot/SanitizePath — a bare
		// "." sanitizes to "-", colliding every relative-CWD invocation
		// onto one store. No existence check: a nonexistent absolute path
		// is still used as given (existing, intentional behaviour).
		if abs, err := filepath.Abs(d); err == nil {
			return resolved{Store: native.StoreFor(configDir(), abs), Anchor: abs, Kind: anchorExplicit}
		}
		// filepath.Abs fails only if os.Getwd() fails (e.g. the process's
		// cwd was removed); fall through to the pin/cwd steps below rather
		// than resolve against a relative path.
	}
	if r, ok := readPin(sessionIDFromEnv()); ok {
		return r
	}
	cwd := stdinCWD
	if cwd == "" {
		// os.Getwd() failure leaves cwd == "" here; StoreFor(cfg, "") is
		// already fail-safe (CanonicalRoot("") returns "", producing the
		// harness's own root-store fallback), so no special-casing needed.
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
	Anchor string `json:"anchor"`
}

// writePin records the session's anchor so CLI verbs run later in the session
// resolve the same store without CLAUDE_PROJECT_DIR. The anchor alone is
// stored; the store is re-derived on every read via native.StoreFor. This
// caps the pin's power at CLAUDE_PROJECT_CWD-level trust: a forged pin file
// can only redirect to a directory an attacker already controls (the anchor
// itself), not inject false project metadata. Best-effort, atomic.
func writePin(sessionID string, r resolved) {
	if sessionID == "" || r.Anchor == "" || !filepath.IsAbs(r.Anchor) {
		return
	}
	if err := os.MkdirAll(filepath.Join(memoryDir(), ".runtime"), 0o755); err != nil {
		return
	}
	b, err := json.Marshal(pinFile{Anchor: r.Anchor})
	if err != nil {
		return
	}
	_ = pathutil.WriteFileAtomic(pinPath(sessionID), b, 0o600)
}

// readPin loads a pin and re-derives the store from its anchor. Missing,
// unparsable or implausible pins are ignored. Any extra JSON fields (from
// old or forged pins) are silently discarded.
func readPin(sessionID string) (resolved, bool) {
	if sessionID == "" {
		return resolved{}, false
	}
	b, err := os.ReadFile(pinPath(sessionID))
	if err != nil {
		return resolved{}, false
	}
	var p pinFile
	if json.Unmarshal(b, &p) != nil || p.Anchor == "" || !filepath.IsAbs(p.Anchor) {
		return resolved{}, false
	}
	return resolved{
		Store:  native.StoreFor(configDir(), p.Anchor),
		Anchor: p.Anchor, Kind: anchorPin,
	}, true
}
