package native

import "path/filepath"

// Collect returns every native memory file in scope for a store: the
// per-project store (st.Dir, tagged st.Project) plus the global store
// (<home>/.claude/memory-global, tagged GlobalProject). claudeDir must be
// hypomnema's own directory (from claudeDir(), NOT configDir()): the global
// store lives beside it for backward compatibility (HYPOMNEMA_GLOBAL_DIR
// overrides). When st is the global store itself (a drill-down into
// "global", a sidecar refresh after an action on a global fact) its files
// are listed once. Missing dirs contribute no files, never an error — the
// single read path every v2 consumer shares.
func Collect(claudeDir string, st Store) []MemFile {
	gdir := GlobalMemoryDir(filepath.Dir(claudeDir))
	glob, _ := List(gdir)
	for i := range glob {
		glob[i].Project = GlobalProject
	}
	if filepath.Clean(st.Dir) == filepath.Clean(gdir) {
		return glob
	}
	proj, _ := List(st.Dir)
	for i := range proj {
		proj[i].Project = st.Project
	}
	return append(proj, glob...)
}
