package native

import "path/filepath"

// Collect returns every native memory file in scope for a store: the
// per-project store (st.Dir, tagged st.Project) plus the global store
// (<home>/.claude/memory-global, tagged GlobalProject). claudeDir is the
// Claude Code config dir; the global store stays beside it for backward
// compatibility (HYPOMNEMA_GLOBAL_DIR overrides). Missing dirs contribute no
// files, never an error — the single read path every v2 consumer shares.
func Collect(claudeDir string, st Store) []MemFile {
	proj, _ := List(st.Dir)
	for i := range proj {
		proj[i].Project = st.Project
	}
	glob, _ := List(GlobalMemoryDir(filepath.Dir(claudeDir)))
	for i := range glob {
		glob[i].Project = GlobalProject
	}
	return append(proj, glob...)
}
