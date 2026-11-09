package stats

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

const today = "2026-06-30"

func q(slug string) string { return native.QKey("p", slug) }

func load(t *testing.T, lines ...string) *Usage {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".wal")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := LoadUsage(p, today)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestLoadUsage_MissingWAL(t *testing.T) {
	u, err := LoadUsage(filepath.Join(t.TempDir(), "nope"), today)
	if err != nil || u == nil {
		t.Fatalf("got %v %v", u, err)
	}
	if s := u.Session("x"); s.Injected != 0 || s.Delivered == nil || s.Top == nil || s.UsefulFacts == nil {
		t.Fatalf("bad empty session %+v", s)
	}
}

// A WAL line longer than the reader's limit is skipped, not fatal: the rows
// on both sides of it still count (a hand-edited or corrupt WAL must not
// leave the panel dead).
func TestLoadUsage_SkipsOverLongLine(t *testing.T) {
	huge := today + "|inject|" + q("x.md") + "|" + strings.Repeat("s", maxWALLine+10)
	u := load(t,
		today+"|inject|"+q("a.md")+"|s1",
		huge,
		today+"|inject|"+q("b.md")+"|s1",
		today+"|cite-useful|"+q("b.md")+"|s1",
	)
	s := u.Session("s1")
	if s.Injected != 2 || s.Useful != 1 {
		t.Fatalf("rows around the long line lost: %+v", s)
	}
	if inj, _ := u.Fact(Key{"p", "x.md"}); inj != 0 {
		t.Fatalf("the over-long row was read: injected=%d", inj)
	}
}

// A last line without a newline still counts.
func TestLoadUsage_LastLineWithoutNewline(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".wal")
	body := today + "|inject|" + q("a.md") + "|s1\n" + today + "|inject|" + q("b.md") + "|s1"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := LoadUsage(p, today)
	if err != nil {
		t.Fatal(err)
	}
	if s := u.Session("s1"); s.Injected != 2 {
		t.Fatalf("injected = %d, want 2", s.Injected)
	}
}

func TestUsage_SessionIncludesSubagents(t *testing.T) {
	u := load(t,
		"2026-06-30|inject|"+q("a.md")+"|s1",
		"2026-06-30|inject|"+q("b.md")+"|s1:ag1",
		"2026-06-30|inject|"+q("c.md")+"|s10",
		"2026-06-30|recall|"+q("d.md")+"|s1",
	)
	s := u.Session("s1")
	if s.Injected != 2 || s.InjectedFacts != 2 || s.Recalled != 1 || len(s.Delivered) != 3 {
		t.Fatalf("%+v", s)
	}
	if s.Delivered[0].Slug != "d.md" {
		t.Fatalf("latest first: %+v", s.Delivered)
	}
}

func TestUsage_UsefulShare(t *testing.T) {
	u := load(t,
		"2026-06-30|cite-useful|"+q("a.md")+"|s1",
		"2026-06-30|cite-silent|"+q("b.md")+"|s1",
		"2026-06-30|cite-silent|"+q("c.md")+"|s1",
		"2026-06-30|cite-silent|"+q("d.md")+"|s1",
		"2026-06-30|cite-undelivered|"+q("e.md")+"|s1",
	)
	s := u.Session("s1")
	if s.Useful != 1 || s.Silent != 3 || s.Undelivered != 1 || s.UsefulShare != 0.25 {
		t.Fatalf("%+v", s)
	}
	if z := u.Session("none"); z.UsefulShare != 0 {
		t.Fatal("zero share")
	}
}

func TestUsage_UsefulWinsOverSilent(t *testing.T) {
	u := load(t,
		"2026-06-30|cite-silent|"+q("a.md")+"|s1",
		"2026-06-30|cite-useful|"+q("a.md")+"|s1",
		"2026-06-30|cite-silent|"+q("a.md")+"|s1",
	)
	s := u.Session("s1")
	if s.Useful != 1 || s.Silent != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestUsage_UsefulFactsComplete(t *testing.T) {
	var l []string
	for i := 0; i < 12; i++ {
		l = append(l, fmt.Sprintf("2026-06-30|cite-useful|%s|s1", q(fmt.Sprintf("f%02d.md", i))))
	}
	s := load(t, l...).Session("s1")
	if len(s.UsefulFacts) != 12 || s.Useful != 12 {
		t.Fatalf("useful facts %d", len(s.UsefulFacts))
	}
}

func TestUsage_TopTenOrder(t *testing.T) {
	var l []string
	for i := 0; i < 12; i++ {
		for j := 0; j <= i%3; j++ {
			l = append(l, fmt.Sprintf("2026-06-30|inject|%s|s1", q(fmt.Sprintf("f%02d.md", i))))
		}
	}
	l = append(l, "2026-06-30|cite-useful|"+q("f02.md")+"|s1")
	top := load(t, l...).Session("s1").Top
	if len(top) != 10 {
		t.Fatalf("len %d", len(top))
	}
	if top[0].Slug != "f02.md" || top[0].Injected != 3 || top[0].Useful != 1 {
		t.Fatalf("%+v", top[0])
	}
	if top[1].Slug != "f05.md" || top[3].Slug != "f11.md" || top[4].Slug != "f01.md" {
		t.Fatalf("order %v %v %v", top[1].Slug, top[3].Slug, top[4].Slug)
	}
}

func TestUsage_Days7OldestFirst(t *testing.T) {
	u := load(t,
		"2026-06-24|inject|"+q("a.md")+"|s1",
		"2026-06-30|inject|"+q("a.md")+"|s1",
		"2026-06-30|inject|"+q("b.md")+"|s2",
		"2026-06-29|cite-useful|"+q("a.md")+"|s1",
		"2026-06-29|cite-silent|"+q("b.md")+"|s1",
		"2026-06-23|inject|"+q("a.md")+"|s1",
	)
	d := u.Days(nil, 7)
	if len(d) != 7 || d[0].Date != "2026-06-24" || d[6].Date != "2026-06-30" {
		t.Fatalf("%+v", d)
	}
	if d[0].Injected != 1 || d[6].Injected != 2 || d[5].Useful != 1 || d[5].Silent != 1 {
		t.Fatalf("%+v", d)
	}
	d = u.Days(map[string]bool{"s2": true}, 7)
	if d[6].Injected != 1 || d[0].Injected != 0 {
		t.Fatalf("filtered %+v", d)
	}
}

func TestUsage_Fact30dWindow(t *testing.T) {
	u := load(t,
		"2026-05-30|inject|"+q("a.md")+"|s0", // 31 days old
		"2026-06-01|inject|"+q("a.md")+"|s1",
		"2026-06-30|inject|"+q("a.md")+"|s2",
		"2026-05-30|cite-useful|"+q("a.md")+"|s0",
		"2026-06-30|cite-useful|"+q("a.md")+"|s2",
	)
	inj, use := u.Fact(Key{"p", "a.md"})
	if inj != 2 || use != 1 {
		t.Fatalf("%d %d", inj, use)
	}
	// several keys: useful-wins per session, unknown key, legacy unqualified
	u = load(t,
		"2026-06-29|inject|"+q("b.md")+"|s1",
		"2026-06-29|inject|"+q("b.md")+"|s1",
		"2026-06-29|cite-silent|"+q("b.md")+"|s1",
		"2026-06-30|cite-useful|"+q("b.md")+"|s1",
		"2026-06-30|cite-useful|"+q("b.md")+"|s2",
		"2026-06-30|cite-silent|"+q("c.md")+"|s1",
		"2026-06-30|inject|d.md|s1",
		"2026-05-01|cite-useful|"+q("e.md")+"|s1",
	)
	for k, want := range map[Key][2]int{
		{"p", "b.md"}: {2, 2}, {"p", "c.md"}: {0, 0}, {"", "d.md"}: {1, 0},
		{"p", "e.md"}: {0, 0}, {"p", "zzz.md"}: {0, 0},
	} {
		if i, us := u.Fact(k); i != want[0] || us != want[1] {
			t.Fatalf("%v: got %d %d want %v", k, i, us, want)
		}
	}
}

func TestUsage_DistinctCounts(t *testing.T) {
	u := load(t,
		"2026-06-30|inject|"+q("a.md")+"|s1",
		"2026-06-29|inject|"+q("a.md")+"|s2",
		"2026-06-20|inject|"+q("b.md")+"|s2",
		"2026-06-30|cite-useful|"+q("a.md")+"|s1",
		"2026-06-30|cite-useful|"+native.QKey("p2", "c.md")+"|s1",
		"2026-06-10|cite-useful|"+q("b.md")+"|s3",
	)
	if n := u.DistinctInjected(7); n != 1 {
		t.Fatal(n)
	}
	if n := u.DistinctInjected(30); n != 2 {
		t.Fatal(n)
	}
	if n := u.DistinctUseful(7); n != 2 {
		t.Fatal(n)
	}
	m := u.UsefulByProject(30)
	if m["p"] != 2 || m["p2"] != 1 {
		t.Fatalf("%v", m)
	}
}

func TestDisplay(t *testing.T) {
	home := "/Users/x"
	if got := Display(native.SanitizePath(home)+"-proj", home); got != "proj" {
		t.Fatal(got)
	}
	if Display("global", home) != "global" {
		t.Fatal("global")
	}
}
