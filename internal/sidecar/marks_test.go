package sidecar

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// markWorld is two project stores plus the global store on disk, an empty
// WAL and an open sidecar. dirs maps every tag to its directory, the way
// the callers build it from native.AllStores.
type markWorld struct {
	t       *testing.T
	walPath string
	dirs    map[string]string
	s       *Store
}

func newMarkWorld(t *testing.T) *markWorld {
	t.Helper()
	root := t.TempDir()
	w := &markWorld{t: t, walPath: filepath.Join(root, ".wal"), dirs: map[string]string{
		"projA":              filepath.Join(root, "a"),
		"projB":              filepath.Join(root, "b"),
		native.GlobalProject: filepath.Join(root, "g"),
	}}
	for _, d := range w.dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(w.walPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(root, ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	w.s = s
	return w
}

// fact writes <dir of project>/<stem>.md and returns its content.
func (w *markWorld) fact(project, stem, body string) string {
	w.t.Helper()
	content := "---\ntype: note\nname: " + stem + "\ndescription: d\nstatus: active\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(w.dirs[project], stem+".md"), []byte(content), 0o644); err != nil {
		w.t.Fatal(err)
	}
	return content
}

func (w *markWorld) wal(rows ...string) {
	w.t.Helper()
	f, err := os.OpenFile(w.walPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		w.t.Fatal(err)
	}
	defer f.Close()
	for _, r := range rows {
		if _, err := f.WriteString(r + "\n"); err != nil {
			w.t.Fatal(err)
		}
	}
}

// files lists the native files of the given stores, tagged like Collect does.
func (w *markWorld) files(projects ...string) []native.MemFile {
	w.t.Helper()
	var out []native.MemFile
	for _, p := range projects {
		fs, err := native.List(w.dirs[p])
		if err != nil {
			w.t.Fatal(err)
		}
		for i := range fs {
			fs[i].Project = p
		}
		out = append(out, fs...)
	}
	return out
}

// reproject runs a close-style projection for project (scope project+global)
// with the full dirs map.
func (w *markWorld) reproject(project string, dirs map[string]string) {
	w.t.Helper()
	if err := ReprojectAll(w.s, w.files(project, native.GlobalProject), w.walPath,
		[]string{project, native.GlobalProject}, dirs); err != nil {
		w.t.Fatalf("ReprojectAll: %v", err)
	}
}

// marks renders the mark table as sorted "project/slug/directive/arg@day".
func (w *markWorld) marks() []string {
	w.t.Helper()
	ms, err := w.s.Marks()
	if err != nil {
		w.t.Fatalf("Marks: %v", err)
	}
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Project+"/"+m.Slug+"/"+m.Directive+"/"+m.Arg+"@"+m.Day)
	}
	sort.Strings(out)
	return out
}

func (w *markWorld) wantMarks(want ...string) {
	w.t.Helper()
	sort.Strings(want)
	got := w.marks()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		w.t.Fatalf("mark table:\n got %q\nwant %q", got, want)
	}
}

func markRow(day, project, stem, directive, arg string) string {
	target := native.QKey(project, stem) + ":" + directive
	if arg != "" {
		target += ":" + arg
	}
	return day + "|mark|" + target + "|s1"
}

func TestMarks_ReprojectHideLess(t *testing.T) {
	w := newMarkWorld(t)
	w.fact("projA", "x", "project fact")
	w.fact(native.GlobalProject, "y", "global fact")
	w.wal(
		markRow("2026-11-01", "projA", "x", "hide", "projA"),
		markRow("2026-11-02", "projA", "x", "less", ""),
		markRow("2026-11-03", native.GlobalProject, "y", "hide", "projA"),
		markRow("2026-11-03", native.GlobalProject, "y", "hide", "projB"),
		markRow("2026-11-04", native.GlobalProject, "y", "less", ""),
	)
	w.reproject("projA", w.dirs)
	w.wantMarks(
		"projA/x.md/hide/projA@2026-11-01",
		"projA/x.md/less/@2026-11-02",
		"global/y.md/hide/projA@2026-11-03",
		"global/y.md/hide/projB@2026-11-03",
		"global/y.md/less/@2026-11-04",
	)
}

func TestMarks_ClearRemovesAll(t *testing.T) {
	w := newMarkWorld(t)
	c := w.fact("projA", "x", "body")
	w.fact("projA", "z", "other")
	w.wal(
		markRow("2026-11-01", "projA", "x", "hide", "projA"),
		markRow("2026-11-01", "projA", "x", "less", ""),
		markRow("2026-11-01", "projA", "x", "rewrite", native.BodySHA(c)),
		markRow("2026-11-01", "projA", "z", "less", ""),
		markRow("2026-11-02", "projA", "x", "clear", ""),
	)
	w.reproject("projA", w.dirs)
	w.wantMarks("projA/z.md/less/@2026-11-01")
}

func TestMarks_DoneClosesTasks(t *testing.T) {
	w := newMarkWorld(t)
	c := w.fact("projA", "x", "body")
	sha := native.BodySHA(c)
	w.wal(
		markRow("2026-11-01", "projA", "x", "less", ""),
		markRow("2026-11-01", "projA", "x", "rewrite", sha),
		markRow("2026-11-01", "projA", "x", "review", sha),
		markRow("2026-11-02", "projA", "x", "done", ""),
	)
	w.reproject("projA", w.dirs)
	w.wantMarks("projA/x.md/less/@2026-11-01")

	// A task opened after done is a new, open task.
	w.wal(markRow("2026-11-03", "projA", "x", "split", sha))
	w.reproject("projA", w.dirs)
	w.wantMarks("projA/x.md/less/@2026-11-01", "projA/x.md/split/"+sha+"@2026-11-03")
}

func TestMarks_TaskAutoClosesOnEdit(t *testing.T) {
	w := newMarkWorld(t)
	c := w.fact("projA", "x", "original body")
	sha := native.BodySHA(c)
	w.wal(
		markRow("2026-11-01", "projA", "x", "rewrite", sha),
		markRow("2026-11-01", "projA", "x", "less", ""),
	)
	w.reproject("projA", w.dirs)
	w.wantMarks("projA/x.md/less/@2026-11-01", "projA/x.md/rewrite/"+sha+"@2026-11-01")

	w.fact("projA", "x", "rewritten body") // the task was done by editing the file
	w.reproject("projA", w.dirs)
	w.wantMarks("projA/x.md/less/@2026-11-01")
}

func TestMarks_RetiredFactDropped(t *testing.T) {
	w := newMarkWorld(t)
	w.fact("projA", "x", "body")
	w.wal(
		markRow("2026-11-01", "projA", "x", "less", ""),
		// projZ has no directory in dirs: only the retire rule can drop it.
		markRow("2026-11-01", "projZ", "w", "less", ""),
		markRow("2026-11-01", "projZ", "w", "hide", "projZ"),
		"2026-11-02|retire|"+native.QKey("projA", "x")+"|s1",
		"2026-11-02|retire|"+native.QKey("projZ", "w")+">successor:promoted_to_hook|s1",
	)
	// retire moved the file to .archive/
	if err := os.Remove(filepath.Join(w.dirs["projA"], "x.md")); err != nil {
		t.Fatal(err)
	}
	w.reproject("projA", w.dirs)
	w.wantMarks()
}

func TestMarks_RetireReviveDoesNotResurrect(t *testing.T) {
	w := newMarkWorld(t)
	c := w.fact("projA", "x", "body")
	w.wal(
		markRow("2026-11-01", "projA", "x", "less", ""),
		markRow("2026-11-01", "projA", "x", "hide", "projA"),
		"2026-11-02|retire|"+native.QKey("projA", "x")+"|s1",
		"2026-11-03|revive|"+native.QKey("projA", "x")+"|s1",
	)
	w.reproject("projA", w.dirs) // file is live again after revive
	w.wantMarks()

	sha := native.BodySHA(c)
	w.wal(markRow("2026-11-04", "projA", "x", "review", sha))
	w.reproject("projA", w.dirs)
	w.wantMarks("projA/x.md/review/" + sha + "@2026-11-04")
}

func TestMarks_ForeignProjectSurvivesReproject(t *testing.T) {
	w := newMarkWorld(t)
	w.fact("projA", "a", "a body")
	c := w.fact("projB", "b", "b body")
	w.wal(
		markRow("2026-11-01", "projB", "b", "less", ""),
		markRow("2026-11-01", "projB", "b", "merge", native.BodySHA(c)),
	)
	w.reproject("projA", w.dirs) // close in project A: files and scope are A + global
	w.wantMarks("projB/b.md/less/@2026-11-01", "projB/b.md/merge/"+native.BodySHA(c)+"@2026-11-01")
}

func TestMarks_UnknownStoreKept(t *testing.T) {
	w := newMarkWorld(t)
	w.wal(
		markRow("2026-11-01", "projQ", "q", "less", ""),
		markRow("2026-11-01", "projQ", "q", "rewrite", "deadbeef0000"),
	)
	w.reproject("projA", w.dirs) // projQ is absent from dirs: rows are kept unchecked
	w.wantMarks("projQ/q.md/less/@2026-11-01", "projQ/q.md/rewrite/deadbeef0000@2026-11-01")
}

func TestMarks_NilDirsLeavesTable(t *testing.T) {
	w := newMarkWorld(t)
	w.fact("projA", "x", "body")
	w.wal(markRow("2026-11-01", "projA", "x", "less", ""))
	w.reproject("projA", w.dirs)
	w.wantMarks("projA/x.md/less/@2026-11-01")

	w.wal(markRow("2026-11-02", "projA", "x", "clear", ""))
	if err := Reproject(w.s, w.files("projA", native.GlobalProject), w.walPath, []string{"projA"}); err != nil {
		t.Fatal(err)
	}
	w.wantMarks("projA/x.md/less/@2026-11-01") // nil dirs: mark table untouched
}

func TestMarks_DoNotTouchCreated(t *testing.T) {
	w := newMarkWorld(t)
	c := w.fact("projA", "x", "body without a created date")
	w.wal(
		markRow("2026-10-01", "projA", "x", "less", ""),
		markRow("2026-10-01", "projA", "x", "review", native.BodySHA(c)),
	)
	w.reproject("projA", w.dirs)
	r, ok, err := w.s.Get("x.md")
	if err != nil || !ok {
		t.Fatalf("row: ok=%v err=%v", ok, err)
	}
	if r.Created != "" || r.RefCount != 0 || r.LastInjected != "" {
		t.Fatalf("mark rows fed an aggregate: created=%q ref=%d last=%q", r.Created, r.RefCount, r.LastInjected)
	}
	// Nor may they leave a phantom aggregate under any key (one keyed with
	// the extras stripped would move the fact's WAL-earliest created).
	aggs, rows := readWALAgg(w.walPath)
	if len(aggs) != 0 {
		t.Fatalf("mark rows created aggregates: %v", aggs)
	}
	if len(rows) != 2 {
		t.Fatalf("lifecycle rows = %d, want the 2 mark rows", len(rows))
	}
}

func TestMarks_IgnoresMalformedRows(t *testing.T) {
	w := newMarkWorld(t)
	w.fact("projA", "x", "body")
	w.wal(
		"2026-11-01|mark|x:less|s1", // unqualified target
		markRow("2026-11-01", "projA", "x", "bogus", ""),
		markRow("2026-11-01", "projA", "x", "hide", ""),    // hide needs a project
		markRow("2026-11-01", "projA", "x", "rewrite", ""), // a task needs its anchor
		markRow("2026-11-01", "projQ", "../../etc/x", "less", ""),
		markRow("2026-11-01", "projA", "", "less", ""),
	)
	w.reproject("projA", w.dirs)
	w.wantMarks()
}

func TestIsTask(t *testing.T) {
	for _, d := range []string{"rewrite", "split", "merge", "review"} {
		if !IsTask(d) {
			t.Errorf("IsTask(%q) = false", d)
		}
	}
	for _, d := range []string{"hide", "less", "clear", "done", ""} {
		if IsTask(d) {
			t.Errorf("IsTask(%q) = true", d)
		}
	}
}
