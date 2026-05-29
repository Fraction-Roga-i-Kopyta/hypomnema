package native

import "path/filepath"

// Collect returns every native memory file in scope for a project: the
// per-project store (<home>/.claude/projects/<slug>/memory) plus the global
// store (<home>/.claude/memory-global). claudeHome is <home>/.claude; cwd is
// the project working directory. Missing dirs contribute no files, never an
// error — this is the single read path the v2 consumers (doctor, self-profile,
// dedup, inject, close) share so none re-derive the on-disk layout.
func Collect(claudeHome, cwd string) []MemFile {
	osHome := filepath.Dir(claudeHome)
	proj, _ := List(ProjectMemoryDir(osHome, cwd))
	glob, _ := List(GlobalMemoryDir(osHome))
	return append(proj, glob...)
}
