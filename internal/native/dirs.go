// Package native is the read-only adapter over Claude Code's native memory
// store. It is the single place that knows the native on-disk layout and
// frontmatter format; everything else in v2 depends on this package rather
// than touching native files directly.
package native

import (
	"os"
	"path/filepath"
	"strings"
)

// SlugFromCWD encodes an absolute working-directory path into the project
// slug Claude Code uses for its per-project memory dir: every "/" becomes
// "-". /home/user/Development/hypomnema -> -home-user-Development-hypomnema
// (encoding verified against a real Claude Code install).
//
// We replace the literal "/" rather than os.PathSeparator: the slug encodes
// Claude Code's logical (POSIX) path, not the host OS separator. If a future
// Claude Code version encodes other characters (dots, spaces), extend here —
// this is the single chokepoint.
func SlugFromCWD(cwd string) string {
	return strings.ReplaceAll(cwd, "/", "-")
}

// ProjectMemoryDir returns the native per-project memory directory for cwd:
// <home>/.claude/projects/<slug>/memory. `home` is the single customization
// point (callers resolve it, honouring CLAUDE_HOME). The asymmetry with
// GlobalMemoryDir's HYPOMNEMA_GLOBAL_DIR override is intentional — the global
// store lives outside the standard projects tree.
func ProjectMemoryDir(home, cwd string) string {
	return filepath.Join(home, ".claude", "projects", SlugFromCWD(cwd), "memory")
}

// GlobalMemoryDir returns the hypomnema-owned global store (native format),
// which native memory itself lacks (native is per-project only). Honours the
// HYPOMNEMA_GLOBAL_DIR override; defaults to <home>/.claude/memory-global.
func GlobalMemoryDir(home string) string {
	if d := os.Getenv("HYPOMNEMA_GLOBAL_DIR"); d != "" {
		return d
	}
	return filepath.Join(home, ".claude", "memory-global")
}
