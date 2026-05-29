package wal

import "strings"

// SanitizeField replaces the WAL column delimiter and line breaks with '_' so
// a value (slug, session id) cannot forge extra columns or split a line —
// preserves the four-column invariant (FORMAT.md §5; audit-2026-04-16 S6).
func SanitizeField(s string) string {
	return strings.NewReplacer("|", "_", "\n", "_", "\r", "_").Replace(s)
}
