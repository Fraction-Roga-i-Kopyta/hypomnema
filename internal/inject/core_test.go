package inject

import (
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/rank"
)

const coreProj = "-tmp-proj"

// coreCands builds one sidecar-like candidate per file: status from the
// frontmatter (pinned/candidate) or active.
func coreCands(files []native.MemFile) []rank.Candidate {
	out := make([]rank.Candidate, 0, len(files))
	for _, f := range files {
		st := f.Status
		if st == "" {
			st = "active"
		}
		out = append(out, rank.Candidate{Slug: f.Slug, Type: f.Type, Project: f.Project, Status: st})
	}
	return out
}

func coreKeys(sc []rank.Scored) string {
	parts := make([]string, len(sc))
	for i, s := range sc {
		parts[i] = s.Project + "/" + s.Slug
	}
	return strings.Join(parts, ",")
}

func TestCoreFacts_OrderContinuityPinnedFeedback(t *testing.T) {
	g := native.GlobalProject
	files := []native.MemFile{
		{Slug: "gfb.md", Type: "feedback", Project: g, Created: "2026-11-20"},
		{Slug: "kn.md", Type: "knowledge", Project: coreProj, Created: "2026-11-20"},
		{Slug: "fb.md", Type: "feedback", Project: coreProj, Created: "2026-11-01"},
		{Slug: "gpin.md", Type: "knowledge", Project: g, Status: "pinned", Created: "2026-11-23"},
		{Slug: "ppin.md", Type: "project", Project: coreProj, Status: "pinned", Created: "2026-01-01"},
		{Slug: "continuity.md", Type: "continuity", Project: coreProj, Created: "2026-11-02"},
	}
	got := coreKeys(coreFacts(files, coreCands(files), coreProj))
	want := coreProj + "/continuity.md," + coreProj + "/ppin.md," + g + "/gpin.md," + coreProj + "/fb.md"
	if got != want {
		t.Errorf("core order\n got %s\nwant %s", got, want)
	}
}

// Continuity is core only as the resolved project's freshest one: never
// through "pinned", never another store's.
func TestCoreFacts_OnlyProjectContinuity(t *testing.T) {
	files := []native.MemFile{
		{Slug: "continuity.md", Type: "continuity", Project: coreProj, Created: "2026-11-20"},
		{Slug: "continuity-old.md", Type: "continuity", Project: coreProj, Created: "2026-10-01"},
		{Slug: "gcont.md", Type: "continuity", Project: native.GlobalProject, Status: "pinned", Created: "2026-11-23"},
		{Slug: "qcont.md", Type: "continuity", Project: "-tmp-other", Created: "2026-11-23"},
		{Slug: "fbcont.md", Type: "continuity", Project: coreProj, Status: "pinned", Created: "2026-09-01"},
	}
	got := coreKeys(coreFacts(files, coreCands(files), coreProj))
	if want := coreProj + "/continuity.md"; got != want {
		t.Errorf("got %s, want only %s", got, want)
	}
}

func TestCoreFacts_FreshestFirstWithinGroup(t *testing.T) {
	files := []native.MemFile{
		{Slug: "a.md", Type: "feedback", Project: coreProj, Created: "2026-10-01"},
		{Slug: "d.md", Type: "feedback", Project: coreProj, Created: "2026-11-01"},
		{Slug: "b.md", Type: "feedback", Project: coreProj, Created: "2026-11-01"},
		{Slug: "c.md", Type: "feedback", Project: coreProj, Created: "2026-10-01", Modified: "2026-11-10"},
	}
	got := coreKeys(coreFacts(files, coreCands(files), coreProj))
	want := coreProj + "/c.md," + coreProj + "/b.md," + coreProj + "/d.md," + coreProj + "/a.md"
	if got != want {
		t.Errorf("freshest (later of created/modified) first, slug tie-break\n got %s\nwant %s", got, want)
	}
}

func TestCoreFacts_SlugCollisionProjectWins(t *testing.T) {
	files := []native.MemFile{
		{Slug: "x.md", Type: "feedback", Project: coreProj, Created: "2026-11-01"},
		{Slug: "x.md", Type: "knowledge", Project: native.GlobalProject, Status: "pinned", Created: "2026-11-01"},
	}
	got := coreFacts(files, coreCands(files), coreProj)
	if len(got) != 1 || got[0].Slug != "x.md" || got[0].Project != coreProj {
		t.Errorf("one x.md, project-local; got %s", coreKeys(got))
	}
}

// Marks and lifecycle status take a fact out of the core.
func TestCoreEligible_MarksAndStatus(t *testing.T) {
	fb := native.MemFile{Slug: "fb.md", Type: "feedback", Project: coreProj}
	newest := map[string]string{}
	cases := []struct {
		name         string
		status       string
		hidden, less bool
		want         bool
	}{
		{"active", "active", false, false, true},
		{"candidate", "candidate", false, false, true},
		{"stale", "stale", false, false, false},
		{"retired", "retired", false, false, false},
		{"hidden", "active", true, false, false},
		{"less", "active", false, true, false},
	}
	for _, c := range cases {
		if got := CoreEligible(fb, c.status, fb.Type, coreProj, c.hidden, c.less, newest); got != c.want {
			t.Errorf("%s: CoreEligible = %v, want %v", c.name, got, c.want)
		}
	}
	gfb := fb
	gfb.Project = native.GlobalProject
	if CoreEligible(gfb, "active", "feedback", coreProj, false, false, newest) {
		t.Errorf("global feedback is not core")
	}
}
