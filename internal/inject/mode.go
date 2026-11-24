package inject

import (
	"os"
	"strings"
)

// PromptInjectEnv is the environment switch for the UserPromptSubmit push.
// Set to "off" it selects pull-only mode: prompts inject nothing, and memory
// reaches the model the way Claude Code itself intends — through the
// harness-loaded project MEMORY.md, the global index the user imports from
// CLAUDE.md, and the model's own reads. SessionStart and SubagentStart
// injection do not depend on it.
const PromptInjectEnv = "HYPOMNEMA_PROMPT_INJECT"

// PullOnly reports whether a PromptInjectEnv value selects pull-only mode:
// "off" in any letter case, surrounding blanks ignored. Any other value,
// including unset, keeps the push.
func PullOnly(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "off")
}

// PullOnlyFromEnv is PullOnly applied to this process's environment.
func PullOnlyFromEnv() bool { return PullOnly(os.Getenv(PromptInjectEnv)) }
