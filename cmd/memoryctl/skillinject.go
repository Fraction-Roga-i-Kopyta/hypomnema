package main

import (
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/inject"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/rank"
)

// learningsForSkill returns up to k skill-learning facts bound to `skill`,
// ranked by effectiveness+recency (no query → overlap is 0 for all).
func learningsForSkill(skill string, k int) ([]rank.Scored, map[string]native.MemFile) {
	cwd := projectCWD()
	files := native.Collect(claudeDir(), cwd)
	bySlug := make(map[string]native.MemFile, len(files))
	for _, f := range files {
		bySlug[f.Slug] = f
	}
	// Empty terms → Candidates returns every file with Overlap 0 but
	// effectiveness/refcount populated from the sidecar/WAL.
	cands := inject.Candidates(memoryDir(), cwd, files, nil)
	matched := cands[:0:0]
	for _, c := range cands {
		mf, ok := bySlug[c.Slug]
		if !ok || mf.Type != "skill-learning" || mf.Skill != skill {
			continue
		}
		matched = append(matched, c)
	}
	q := rank.Query{
		Project:      native.SlugFromCWD(cwd),
		Today:        today(),
		IncludeStale: true,
	}
	return rank.Rank(q, matched, k), bySlug
}
