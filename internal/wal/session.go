package wal

import "strings"

// ParentSession maps a subagent observation key "<session>:<agent>" to its
// parent session id; any other session id comes back unchanged. Counters
// that ask "in how many sessions" use it so one orchestrating session and
// its subagents count once — their observations are correlated.
func ParentSession(s string) string {
	p, _, _ := strings.Cut(s, ":")
	return p
}
