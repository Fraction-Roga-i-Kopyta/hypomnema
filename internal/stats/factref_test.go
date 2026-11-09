package stats

import (
	"reflect"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

const home = "/Users/x"

var proj = native.SanitizePath(home + "/work/proj")

func memFile(status string) native.MemFile {
	return native.MemFile{
		Slug: "a.md", Path: "/s/a.md", Project: proj, Name: "alpha", Type: "feedback",
		Description: "d", Created: "2026-06-01", Status: status,
	}
}

func TestBuildFactRef_NilRecordDefaults(t *testing.T) {
	for _, tc := range []struct{ fm, want string }{
		{"", "active"}, {"pinned", "pinned"}, {"candidate", "candidate"},
	} {
		r := BuildFactRef(memFile(tc.fm), nil, nil, nil, home)
		if r.RefCount != 0 || r.Effectiveness != 0.5 || r.Status != tc.want {
			t.Fatalf("fm %q: %+v", tc.fm, r)
		}
		if r.Marks == nil || len(r.Marks) != 0 {
			t.Fatalf("marks must be empty, non-nil: %#v", r.Marks)
		}
	}
}

func TestBuildFactRef_Fields(t *testing.T) {
	u := load(t,
		"2026-06-30|inject|"+native.QKey(proj, "a.md")+"|s1",
		"2026-06-29|inject|"+native.QKey(proj, "a.md")+"|s2",
		"2026-06-29|cite-useful|"+native.QKey(proj, "a.md")+"|s2",
	)
	rec := &sidecar.Record{Slug: "a.md", Project: proj, RefCount: 7, Effectiveness: 0.75,
		Status: "stale", LastInjected: "2026-06-30", LastUseful: "2026-06-29"}
	marks := []sidecar.Mark{
		{Project: proj, Slug: "a.md", Directive: "rewrite", Arg: "sha1"},
		{Project: proj, Slug: "a.md", Directive: "less"},
		{Project: proj, Slug: "a.md", Directive: "hide", Arg: proj},
		{Project: proj, Slug: "a.md", Directive: "rewrite", Arg: "sha2"}, // stale anchor duplicate
		{Project: proj, Slug: "b.md", Directive: "less"},                 // other fact
		{Project: native.GlobalProject, Slug: "a.md", Directive: "less"}, // same basename, other store
	}
	got := BuildFactRef(memFile("active"), rec, u, marks, home)
	want := FactRef{
		Project: proj, Display: "work-proj", Slug: "a.md", Name: "alpha", Type: "feedback",
		Status: "stale", Description: "d", Path: "/s/a.md", Created: "2026-06-01",
		RefCount: 7, Effectiveness: 0.75, LastInjected: "2026-06-30", LastUseful: "2026-06-29",
		Injected30d: 2, Useful30d: 1,
		Marks: []string{"hide:" + proj, "less", "task:rewrite"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("\n got %+v\nwant %+v", got, want)
	}
}

func TestBuildFactRef_NameTypeFallback(t *testing.T) {
	f := native.MemFile{Slug: "bare.md", Path: "/s/bare.md", Project: native.GlobalProject}
	r := BuildFactRef(f, nil, nil, nil, home)
	if r.Name != "bare.md" || r.Type != "note" || r.Display != "global" {
		t.Fatalf("%+v", r)
	}
}

func TestBuildFactRef_CreatedFallsBackToRecord(t *testing.T) {
	f := memFile("")
	f.Created = ""
	r := BuildFactRef(f, &sidecar.Record{Created: "2026-05-05", Effectiveness: 0.5}, nil, nil, home)
	if r.Created != "2026-05-05" || r.Status != "active" {
		t.Fatalf("%+v", r)
	}
}

func TestKeyRef_MissingFile(t *testing.T) {
	k := Key{Project: proj, Slug: "gone.md"}
	u := load(t, "2026-06-30|inject|"+native.QKey(proj, "gone.md")+"|s1")
	r := KeyRef(k, nil, u, nil, home)
	if r.Path != "" || r.Status != "" || r.Type != "" || r.Name != "gone.md" || r.Display != "work-proj" ||
		r.Effectiveness != 0.5 || r.Injected30d != 1 || r.Marks == nil {
		t.Fatalf("no record: %+v", r)
	}
	rec := &sidecar.Record{Slug: "gone.md", Project: proj, Name: "gone", Type: "note",
		Description: "was here", Status: "retired", RefCount: 3, Effectiveness: 0.4}
	r = KeyRef(k, rec, u, nil, home)
	if r.Path != "" || r.Status != "retired" || r.Name != "gone" || r.RefCount != 3 || r.Effectiveness != 0.4 {
		t.Fatalf("with record: %+v", r)
	}
}

func TestIndexRecords(t *testing.T) {
	m := IndexRecords([]sidecar.Record{
		{Slug: "a.md", Project: proj, RefCount: 1},
		{Slug: "a.md", Project: native.GlobalProject, RefCount: 2},
	})
	if m[Key{proj, "a.md"}].RefCount != 1 || m[Key{native.GlobalProject, "a.md"}].RefCount != 2 {
		t.Fatalf("%+v", m)
	}
}

func TestUsage_SessionFact(t *testing.T) {
	a := native.QKey(proj, "a.md")
	u := load(t,
		"2026-06-30|inject|"+a+"|s1",
		"2026-06-30|inject|"+a+"|s1:ag",
		"2026-06-30|inject|"+a+"|s2",
		"2026-06-30|cite-silent|"+a+"|s1",
		"2026-06-30|cite-useful|"+a+"|s1:ag",
	)
	if inj, use := u.SessionFact("s1", Key{proj, "a.md"}); inj != 2 || use != 1 {
		t.Fatalf("s1: %d %d", inj, use)
	}
	if inj, use := u.SessionFact("s2", Key{proj, "a.md"}); inj != 1 || use != 0 {
		t.Fatalf("s2: %d %d", inj, use)
	}
}

func TestEnrichTop(t *testing.T) {
	a, g := native.QKey(proj, "a.md"), native.QKey(proj, "gone.md")
	u := load(t,
		"2026-06-30|inject|"+a+"|s1",
		"2026-06-30|inject|"+a+"|s1",
		"2026-06-30|inject|"+g+"|s1",
		"2026-06-30|cite-useful|"+a+"|s1",
	)
	s := u.Session("s1")
	recs := IndexRecords([]sidecar.Record{{Slug: "a.md", Project: proj, RefCount: 9, Effectiveness: 0.6, Status: "active"}})
	marks := []sidecar.Mark{{Project: proj, Slug: "a.md", Directive: "less"}}
	EnrichTop(&s, []native.MemFile{memFile("active")}, recs, u, marks, home)
	if len(s.Top) != 2 {
		t.Fatalf("top %+v", s.Top)
	}
	top := s.Top[0]
	if top.Slug != "a.md" || top.Name != "alpha" || top.RefCount != 9 || top.Injected != 2 || top.Useful != 1 ||
		!reflect.DeepEqual(top.Marks, []string{"less"}) || top.Path != "/s/a.md" {
		t.Fatalf("enriched: %+v", top)
	}
	if gone := s.Top[1]; gone.Slug != "gone.md" || gone.Path != "" || gone.Injected != 1 || gone.Display != "work-proj" {
		t.Fatalf("missing: %+v", gone)
	}
}
