// Package native is the read-only adapter over Claude Code's native memory
// store. It is the single place that knows the native on-disk layout and
// frontmatter format; everything else in v2 depends on this package rather
// than touching native files directly. Project slug/store resolution (the
// harness-parity sanitizer, canonical git root, and Store type) lives in
// store.go.
package native

import (
	"os"
	"path/filepath"
	"strings"
)

// GlobalMemoryDir returns the hypomnema-owned global store (native format),
// which native memory itself lacks (native is per-project only). Honours the
// HYPOMNEMA_GLOBAL_DIR override; defaults to <home>/.claude/memory-global.
func GlobalMemoryDir(home string) string {
	if d := os.Getenv("HYPOMNEMA_GLOBAL_DIR"); d != "" {
		return d
	}
	return filepath.Join(home, ".claude", "memory-global")
}

// GlobalProject is the sidecar project tag for facts in the global store.
const GlobalProject = "global"

// qkeySep separates the project from the slug in a project-qualified WAL
// target / sidecar identity. ASCII Unit Separator (0x1f) is impossible inside
// a filename-derived slug or a store's project tag (SanitizePath's output
// alphabet is [a-zA-Z0-9-]), so QKey is collision-proof, and it is neither
// the WAL column delimiter ('|') nor a carriage return, so a qualified
// target still passes wal.Validate. Grep still finds a slug: it survives
// intact after the separator.
const qkeySep = "\x1f"

// QKey builds the project-qualified key for a fact. Global facts use the
// GlobalProject tag. The slug is used as-is (callers pass the bare or .md slug
// consistently on both the write and read side).
func QKey(project, slug string) string {
	return project + qkeySep + slug
}

// ParseQKey splits a WAL target into (project, slug). qualified is false for a
// legacy bare slug (no separator) written before project attribution — those
// are grandfathered at reproject time to whichever project currently owns the
// slug.
func ParseQKey(target string) (project, slug string, qualified bool) {
	if i := strings.IndexByte(target, qkeySep[0]); i >= 0 {
		return target[:i], target[i+1:], true
	}
	return "", target, false
}

// KeySet is the set of facts a session list names. A QKey line names one
// fact; a bare slug line (written by a binary that predates project-qualified
// session lists) names that file in every store — the conservative reading,
// so an upgrade mid-session never re-offers or re-logs what the old binary
// already delivered.
type KeySet struct {
	keys map[string]bool // QKey lines
	bare map[string]bool // bare slug lines
}

// NewKeySet builds the set from session-list lines.
func NewKeySet(lines []string) KeySet {
	s := KeySet{keys: map[string]bool{}, bare: map[string]bool{}}
	for _, ln := range lines {
		s.Add(ln)
	}
	return s
}

// Add puts one session-list line (QKey or bare slug) into the set.
func (s KeySet) Add(line string) {
	if _, slug, qualified := ParseQKey(line); qualified {
		s.keys[line] = true
	} else if slug != "" {
		s.bare[slug] = true
	}
}

// Has reports whether the fact (project, slug) is in the set.
func (s KeySet) Has(project, slug string) bool {
	return s.keys[QKey(project, slug)] || s.bare[slug]
}
