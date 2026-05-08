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

// slugUnsafe replaces WAL-grammar-breaking characters (`|` is the
// 4-column delimiter; `\n` and `\r` would split the line). Bash side
// already enforces this via S6 in audit-2026-04-16; the Go pilot
// drifted from S6 — this restores parity. See external review
// 2026-05-08 finding E1.
var slugUnsafe = strings.NewReplacer("|", "_", "\n", "_", "\r", "_")

// SlugFromPath converts a memory-file path to its frontmatter slug.
// "/abs/path/mistakes/foo-bug.md" → "foo-bug". Portable — uses
// filepath.Base so any OS separator is handled, not just `/`.
//
// Slugs are written into the WAL as the second-to-last column, so any
// `|`/`\n`/`\r` in the source filename would inject extra columns or
// break the line into two. We replace those with `_` defensively.
func SlugFromPath(p string) string {
	base := filepath.Base(p)
	return slugUnsafe.Replace(strings.TrimSuffix(base, ".md"))
}
