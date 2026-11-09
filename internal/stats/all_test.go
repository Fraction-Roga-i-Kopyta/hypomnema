package stats

import (
	"reflect"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

const projB = "-Users-x-other"

func allStores() []native.Store {
	return []native.Store{
		{Dir: "/a", Project: proj},
		{Dir: "/b", Project: projB},
		{Dir: "/g", Project: native.GlobalProject},
	}
}

func TestBuildAll_Funnel(t *testing.T) {
	u := load(t,
		"2026-06-30|inject|"+native.QKey(proj, "a.md")+"|s1",
		"2026-06-30|inject|"+native.QKey(proj, "a.md")+"|s2",
		"2026-06-25|inject|"+native.QKey(proj, "b.md")+"|s3",
		"2026-06-30|cite-useful|"+native.QKey(proj, "a.md")+"|s1",
	)
	files := map[string][]native.MemFile{
		"/a": {pfile(proj, "a.md", "note", "active"), pfile(proj, "b.md", "note", "active"), pfile(proj, "c.md", "mistake", "active")},
		"/g": {pfile(native.GlobalProject, "g.md", "feedback", "active")},
	}
	all := BuildAll(allStores(), files, nil, u, nil, home)
	if all.Stores != 3 || all.Facts != 4 {
		t.Fatalf("counts: %+v", all)
	}
	if want := (Funnel{Stored: 4, Injected7d: 2, Useful7d: 1, NotInjected30d: 2}); all.Funnel != want {
		t.Fatalf("funnel %+v want %+v", all.Funnel, want)
	}
	if want := map[string]int{"note": 2, "mistake": 1, "feedback": 1}; !reflect.DeepEqual(all.ByType, want) {
		t.Fatalf("by_type %v", all.ByType)
	}
	if all.Triage.Noisy == nil || all.Triage.Candidates == nil || all.Triage.Stale == nil || all.Projects == nil {
		t.Fatalf("nil slices: %+v", all)
	}
}

func TestBuildAll_ProjectsOrder(t *testing.T) {
	u := load(t,
		"2026-06-30|inject|"+native.QKey(projB, "x.md")+"|s1",
		"2026-06-30|cite-useful|"+native.QKey(projB, "x.md")+"|s1")
	files := map[string][]native.MemFile{
		"/a": {pfile(proj, "a.md", "note", "active")},
		"/b": {pfile(projB, "x.md", "note", "active"), pfile(projB, "y.md", "note", "active")},
		"/g": {pfile(native.GlobalProject, "g.md", "note", "active")},
	}
	all := BuildAll(allStores(), files, nil, u, nil, home)
	var tags []string
	for _, p := range all.Projects {
		tags = append(tags, p.Project)
	}
	// facts desc (projB has 2), then tag asc among the 1-fact stores.
	want := []string{projB, proj, native.GlobalProject}
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("order %v want %v", tags, want)
	}
	if all.Projects[0].Facts != 2 || all.Projects[0].Useful7d != 1 || all.Projects[0].Display != "other" {
		t.Fatalf("row %+v", all.Projects[0])
	}
}

func TestBuildAll_TriageAcrossStores(t *testing.T) {
	var lines []string
	for _, k := range []Key{{proj, "less.md"}, {proj, "hidea.md"}, {projB, "hidea.md"}, {projB, "plain.md"}} {
		for i := 0; i < 6; i++ {
			lines = append(lines, "2026-06-30|inject|"+native.QKey(k.Project, k.Slug)+"|s"+string(rune('a'+i)))
		}
	}
	u := load(t, lines...)
	files := map[string][]native.MemFile{
		"/a": {pfile(proj, "less.md", "note", "active"), pfile(proj, "hidea.md", "note", "active"), pfile(proj, "cand.md", "note", "candidate")},
		"/b": {pfile(projB, "hidea.md", "note", "active"), pfile(projB, "plain.md", "note", "active")},
	}
	marks := []sidecar.Mark{
		{Project: proj, Slug: "less.md", Directive: "less"},
		{Project: proj, Slug: "hidea.md", Directive: "hide", Arg: proj},  // own project: handled
		{Project: projB, Slug: "hidea.md", Directive: "hide", Arg: proj}, // hidden elsewhere: still noisy
	}
	all := BuildAll(allStores(), files, nil, u, marks, home)
	var got []string
	for _, r := range all.Triage.Noisy {
		got = append(got, r.Project+"/"+r.Slug)
	}
	want := []string{projB + "/hidea.md", projB + "/plain.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("noisy %v want %v", got, want)
	}
	if len(all.Triage.Candidates) != 1 || all.Triage.Candidates[0].Display != "work-proj" {
		t.Fatalf("candidates %+v", all.Triage.Candidates)
	}
}

// TestBuildAll_FunnelIgnoresFileless: WAL keys without a file (deleted,
// retired, or in a store outside the list) do not count, so useful <= injected
// <= stored and the project rows sum to the funnel.
func TestBuildAll_FunnelIgnoresFileless(t *testing.T) {
	u := load(t,
		"2026-06-30|inject|"+native.QKey(proj, "a.md")+"|s1",
		"2026-06-30|cite-useful|"+native.QKey(proj, "a.md")+"|s1",
		"2026-06-30|inject|"+native.QKey(proj, "ghost.md")+"|s1",
		"2026-06-30|cite-useful|"+native.QKey(proj, "ghost.md")+"|s1",
		"2026-06-30|inject|"+native.QKey("-elsewhere", "z.md")+"|s1",
		"2026-06-30|cite-useful|"+native.QKey("-elsewhere", "z.md")+"|s1",
	)
	files := map[string][]native.MemFile{
		"/a": {pfile(proj, "a.md", "note", "active")},
		"/g": {pfile(native.GlobalProject, "g.md", "note", "active")},
	}
	all := BuildAll(allStores(), files, nil, u, nil, home)
	if want := (Funnel{Stored: 2, Injected7d: 1, Useful7d: 1, NotInjected30d: 1}); all.Funnel != want {
		t.Fatalf("funnel %+v want %+v", all.Funnel, want)
	}
	sum := 0
	for _, p := range all.Projects {
		sum += p.Useful7d
	}
	if sum != all.Funnel.Useful7d {
		t.Fatalf("rows sum %d != funnel useful %d", sum, all.Funnel.Useful7d)
	}
}

// TestBuildAll_UsefulWithinDelivered: stage 2 counts inject or recall
// deliveries, stage 3 only useful facts inside that set.
func TestBuildAll_UsefulWithinDelivered(t *testing.T) {
	u := load(t,
		"2026-06-30|recall|"+native.QKey(proj, "rec.md")+"|s1",
		"2026-06-30|cite-useful|"+native.QKey(proj, "rec.md")+"|s1",
		"2026-06-30|cite-useful|"+native.QKey(proj, "undelivered.md")+"|s1",
		"2026-06-30|inject|"+native.QKey(proj, "inj.md")+"|s1",
	)
	files := map[string][]native.MemFile{
		"/a": {pfile(proj, "rec.md", "note", "active"), pfile(proj, "undelivered.md", "note", "active"), pfile(proj, "inj.md", "note", "active")},
	}
	all := BuildAll(allStores(), files, nil, u, nil, home)
	if all.Funnel.Stored != 3 || all.Funnel.Injected7d != 2 || all.Funnel.Useful7d != 1 {
		t.Fatalf("funnel %+v", all.Funnel)
	}
	sum := 0
	for _, p := range all.Projects {
		sum += p.Useful7d
	}
	if sum != all.Funnel.Useful7d {
		t.Fatalf("rows sum %d != funnel useful %d", sum, all.Funnel.Useful7d)
	}
}
