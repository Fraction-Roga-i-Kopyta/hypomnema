package inject

import (
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/rank"
)

// A single huge body (e.g. avatar-unified-analysis.md at 106KB) must not be
// injected whole — render caps each body so one record can't dominate context.
func TestRender_CapsLongBodies(t *testing.T) {
	// "Z" appears in neither the header ("# Memory Context") nor the marker.
	big := strings.Repeat("Z", 5000)
	ranked := []rank.Scored{{Candidate: rank.Candidate{Slug: "big.md"}}}
	bySlug := map[string]native.MemFile{
		"big.md": {Slug: "big.md", Name: "Big", Body: big},
	}

	out := render(ranked, bySlug, 500)

	if n := strings.Count(out, "Z"); n > 500 {
		t.Errorf("body not capped: %d body chars emitted (cap 500 bytes)", n)
	}
	if !strings.Contains(out, "truncated") {
		t.Errorf("truncated body must carry a marker; got:\n%s", out)
	}
}

// A body within the cap is emitted verbatim — no marker, no loss.
func TestRender_ShortBodyVerbatim(t *testing.T) {
	ranked := []rank.Scored{{Candidate: rank.Candidate{Slug: "x.md"}}}
	bySlug := map[string]native.MemFile{
		"x.md": {Slug: "x.md", Name: "X", Body: "short body"},
	}

	out := render(ranked, bySlug, 500)

	if !strings.Contains(out, "short body") {
		t.Errorf("short body must survive verbatim; got:\n%s", out)
	}
	if strings.Contains(out, "truncated") {
		t.Errorf("short body must not be marked truncated; got:\n%s", out)
	}
}
