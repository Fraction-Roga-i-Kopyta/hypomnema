package native

import "path/filepath"

// Collect returns every native memory file in scope for a project: the
// per-project store (<home>/.claude/projects/<slug>/memory) plus the global
// store (<home>/.claude/memory-global). claudeHome is <home>/.claude; cwd is
// the project working directory. Missing dirs contribute no files, never an
// error — this is the single read path the v2 consumers (doctor, self-profile,
// dedup, inject, close) share so none re-derive the on-disk layout.
//
// Each file is tagged with its owning project — the cwd slug or
// GlobalProject. The tag drives sidecar scoping: the sidecar DB is shared
// across all projects, so a session must only reconcile (and inject) rows
// inside its own project ∪ global, never another project's.
func Collect(claudeHome, cwd string) []MemFile {
	osHome := filepath.Dir(claudeHome)
	proj, _ := List(ProjectMemoryDir(osHome, cwd))
	slug := SlugFromCWD(cwd)
	for i := range proj {
		proj[i].Project = slug
	}
	glob, _ := List(GlobalMemoryDir(osHome))
	for i := range glob {
		glob[i].Project = GlobalProject
	}
	return append(proj, glob...)
}
