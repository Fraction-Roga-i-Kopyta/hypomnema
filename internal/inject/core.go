package inject

import (
	"sort"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/rank"
)

// Core groups, in delivery order. A fact lands in the first group it
// qualifies for.
const (
	coreContinuity   = iota // the resolved project's freshest continuity
	corePinnedLocal         // pinned, project-local
	corePinnedGlobal        // pinned, global
	coreFeedback            // project-local feedback
	coreNone
)

// coreGroup returns which core group a fact belongs to, or coreNone. It is
// the single rule behind coreEligible (doctor) and coreFacts (SessionStart).
func coreGroup(f native.MemFile, c rank.Candidate, project string, newestContinuity map[string]string) int {
	switch c.Status {
	case "active", "pinned", "candidate":
	default:
		return coreNone // stale, retired, deleted, unknown
	}
	if c.Hidden || c.Less {
		return coreNone
	}
	typ := c.Type
	if typ == "" {
		typ = f.Type
	}
	// Continuity is core only as the project's freshest one — never through
	// pinned or feedback, never another store's.
	if typ == "continuity" || f.Type == "continuity" {
		if f.Project == project && newestContinuity[project] == f.Slug {
			return coreContinuity
		}
		return coreNone
	}
	if c.Status == "pinned" || f.Status == "pinned" {
		if f.Project == project {
			return corePinnedLocal
		}
		return corePinnedGlobal
	}
	if typ == "feedback" && f.Project == project {
		return coreFeedback
	}
	return coreNone
}

// coreEligible reports whether a fact is part of the SessionStart core for
// project: the freshest project continuity, a pinned fact, or project-local
// feedback — live (active/pinned/candidate) and neither hidden nor marked
// less.
func coreEligible(f native.MemFile, c rank.Candidate, project string, newestContinuity map[string]string) bool {
	return coreGroup(f, c, project, newestContinuity) != coreNone
}

// CoreEligible is coreEligible for callers outside the injection pipeline
// (doctor), which hold a native file plus its status, type and marks rather
// than a ranker candidate.
func CoreEligible(f native.MemFile, status, typ, project string, hidden, less bool, newestContinuity map[string]string) bool {
	return coreEligible(f, rank.Candidate{
		Slug: f.Slug, Project: f.Project, Type: typ, Status: status, Hidden: hidden, Less: less,
	}, project, newestContinuity)
}

// coreFacts selects the SessionStart core from cands (already scoped to the
// project and global stores): continuity, pinned project-local then global,
// project feedback — freshest first within a group (later of created and
// modified), slug tie-break — one fact per slug, project-local winning.
func coreFacts(files []native.MemFile, cands []rank.Candidate, project string) []rank.Scored {
	byKey := make(map[string]native.MemFile, len(files))
	for _, f := range files {
		byKey[native.QKey(f.Project, f.Slug)] = f
	}
	newest := native.NewestContinuity(files)
	type entry struct {
		sc    rank.Scored
		group int
		fresh string
	}
	var es []entry
	for _, c := range cands {
		f, ok := byKey[native.QKey(c.Project, c.Slug)]
		if !ok {
			continue // a sidecar row whose file is gone cannot render
		}
		g := coreGroup(f, c, project, newest)
		if g == coreNone {
			continue
		}
		es = append(es, entry{rank.Scored{Candidate: c}, g, native.FreshnessDate(f.Created, f.Modified)})
	}
	sort.SliceStable(es, func(i, j int) bool {
		a, b := es[i], es[j]
		if a.group != b.group {
			return a.group < b.group
		}
		if a.fresh != b.fresh {
			return a.fresh > b.fresh
		}
		return a.sc.Slug < b.sc.Slug
	})
	out := make([]rank.Scored, len(es))
	for i, e := range es {
		out[i] = e.sc
	}
	return dedupBySlug(out, project)
}
