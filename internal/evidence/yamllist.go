package evidence

import "strings"

// ParseYAMLListItem unquotes a single `- "value"` / `- 'value'` /
// `- value` line in a YAML list. Returns (value, true) if the line
// begins with `- ` (after trimming); ("", false) otherwise — the
// caller decides whether a non-match ends the block or is a blank
// line to tolerate.
//
// Does NOT apply case folding; callers that need lowercase keys for
// dedup do `strings.ToLower(val)` themselves. This was extracted in
// v0.15 after the same seven lines appeared in three places (one of
// which had drifted and was lower-casing unconditionally, breaking a
// surrounding key-preservation invariant).
func ParseYAMLListItem(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "- ") {
		return "", false
	}
	v := strings.TrimSpace(strings.TrimPrefix(t, "- "))
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') ||
			(v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
	}
	return v, true
}
