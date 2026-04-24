// Package pathutil carries tiny path helpers shared across the
// hypomnema internal/ tree. Extracted in v0.15 after the same slug
// derivation logic showed up in three places with three subtly
// different implementations — one of them relying on `/` as the
// directory separator, which would break on non-Unix paths.
//
// Keep this package small. If a helper grows past ~15 lines or
// needs package-level state, it probably belongs in its own domain
// package, not here.
package pathutil

import (
	"path/filepath"
	"strings"
)

// SlugFromPath converts a memory-file path to its frontmatter slug.
// "/abs/path/mistakes/foo-bug.md" → "foo-bug". Portable — uses
// filepath.Base so any OS separator is handled, not just `/`.
func SlugFromPath(p string) string {
	base := filepath.Base(p)
	return strings.TrimSuffix(base, ".md")
}
