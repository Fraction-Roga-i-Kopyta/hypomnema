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
