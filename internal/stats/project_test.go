package stats

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

func pfile(project, slug, typ, status string) native.MemFile {
	return native.MemFile{Slug: slug, Project: project, Name: slug, Type: typ, Status: status, Path: "/s/" + slug}
}

func pstore() native.Store { return native.Store{Dir: "/s", Project: proj} }

func TestBuildProject_Counts(t *testing.T) {
	files := []native.MemFile{
		pfile(proj, "a.md", "feedback", "active"),
		pfile(proj, "b.md", "mistake", "candidate"),
		pfile(proj, "c.md", "", "pinned"),
		pfile(native.GlobalProject, "g.md", "feedback", "active"),
	}
	recs := map[Key]sidecar.Record{{proj, "a.md"}: {Project: proj, Slug: "a.md", Status: "stale"}}
	ps := BuildProject(pstore(), files, recs, nil, nil, map[string]bool{}, Health{}, home)
	if ps.Project != proj || ps.Display != "work-proj" || ps.Visible != 4 || ps.Local != 3 || ps.Global != 1 ||
		ps.Candidates != 1 || ps.Pinned != 1 || ps.Stale != 1 {
		t.Fatalf("counts: %+v", ps)
	}
	if want := map[string]int{"feedback": 2, "mistake": 1, "note": 1}; !reflect.DeepEqual(ps.ByType, want) {
		t.Fatalf("by_type %v", ps.ByType)
	}
	if len(ps.Days) != 7 || ps.Queue == nil || ps.Triage.Noisy == nil || ps.Triage.Candidates == nil ||
		ps.Triage.Stale == nil || ps.Health.Problems == nil {
		t.Fatalf("nil slices / days: %+v", ps)
	}
}

func TestBuildProject_DaysFromProjectSessions(t *testing.T) {
	u := load(t,
		"2026-06-30|inject|"+native.QKey(proj, "a.md")+"|mine",
		"2026-06-30|inject|"+native.QKey(proj, "a.md")+"|other",
		"2026-06-30|cite-useful|"+native.QKey(proj, "a.md")+"|mine:agent",
	)
	ps := BuildProject(pstore(), nil, nil, u, nil, map[string]bool{"mine": true}, Health{}, home)
	last := ps.Days[len(ps.Days)-1]
	if last.Date != today || last.Injected != 1 || last.Useful != 1 {
		t.Fatalf("last day %+v", last)
	}
}

func TestBuildProject_TriageNoisyThreshold(t *testing.T) {
	var lines []string
	for i := 0; i < 4; i++ {
		lines = append(lines, "2026-06-30|inject|"+native.QKey(proj, "four.md")+"|s"+string(rune('a'+i)))
	}
	for i := 0; i < 5; i++ {
		lines = append(lines, "2026-06-30|inject|"+native.QKey(proj, "five.md")+"|s"+string(rune('a'+i)))
	}
	u := load(t, lines...)
	files := []native.MemFile{pfile(proj, "four.md", "note", "active"), pfile(proj, "five.md", "note", "active")}
	ps := BuildProject(pstore(), files, nil, u, nil, map[string]bool{}, Health{}, home)
	if len(ps.Triage.Noisy) != 1 || ps.Triage.Noisy[0].Slug != "five.md" {
		t.Fatalf("noisy: %+v", ps.Triage.Noisy)
	}
}

func TestBuildProject_TriageCap(t *testing.T) {
	var files []native.MemFile
	for i := 0; i < 60; i++ {
		files = append(files, pfile(proj, "c"+string(rune('a'+i/26))+string(rune('a'+i%26))+".md", "note", "candidate"))
	}
	ps := BuildProject(pstore(), files, nil, nil, nil, map[string]bool{}, Health{}, home)
	if len(ps.Triage.Candidates) != 50 || ps.Candidates != 60 {
		t.Fatalf("cap: %d / %d", len(ps.Triage.Candidates), ps.Candidates)
	}
}

func TestBuildProject_Queue(t *testing.T) {
	files := []native.MemFile{pfile(proj, "a.md", "note", "active"), pfile(proj, "b.md", "note", "active")}
	marks := []sidecar.Mark{
		{Project: proj, Slug: "a.md", Directive: "rewrite", Arg: "sha"},
		{Project: proj, Slug: "b.md", Directive: "less"},
	}
	ps := BuildProject(pstore(), files, nil, nil, marks, map[string]bool{}, Health{}, home)
	if len(ps.Queue) != 1 || ps.Queue[0].Slug != "a.md" {
		t.Fatalf("queue: %+v", ps.Queue)
	}
}

func TestProblem_Cut(t *testing.T) {
	if n := len([]rune(Problem(strings.Repeat("я", 200)))); n != 120 {
		t.Fatalf("len %d", n)
	}
}
