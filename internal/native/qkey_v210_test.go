package native

import "testing"

func TestQKeyRoundTrip(t *testing.T) {
	p, s, ok := ParseQKey(QKey("-work-proj", "notes.md"))
	if !ok || p != "-work-proj" || s != "notes.md" {
		t.Errorf("round-trip: got (%q,%q,%v)", p, s, ok)
	}
	// Legacy bare slug — no separator.
	p, s, ok = ParseQKey("notes.md")
	if ok || p != "" || s != "notes.md" {
		t.Errorf("legacy bare: got (%q,%q,%v)", p, s, ok)
	}
	// Slug substring still greppable in the qualified form.
	if q := QKey("global", "x.md"); q[len(q)-4:] != "x.md" {
		t.Errorf("slug not intact at tail: %q", q)
	}
}

// A session-list key set: a QKey line names one fact; a bare line (older
// binary) names that file in every store.
func TestKeySet(t *testing.T) {
	s := NewKeySet([]string{QKey(GlobalProject, "x.md"), "y.md"})
	cases := []struct {
		project, slug string
		want          bool
	}{
		{GlobalProject, "x.md", true},
		{"-p", "x.md", false},
		{"-p", "y.md", true},
		{GlobalProject, "y.md", true},
		{"-p", "z.md", false},
	}
	for _, c := range cases {
		if got := s.Has(c.project, c.slug); got != c.want {
			t.Errorf("Has(%q,%q) = %v, want %v", c.project, c.slug, got, c.want)
		}
	}
	if NewKeySet(nil).Has("-p", "x.md") {
		t.Error("empty set must hold nothing")
	}
}
