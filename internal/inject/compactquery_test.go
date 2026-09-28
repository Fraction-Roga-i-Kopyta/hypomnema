package inject

import (
	"fmt"
	"strings"
	"testing"
)

func TestCompactQuery_FocusSections(t *testing.T) {
	summary := strings.Join([]string{
		"This session is being continued from a previous conversation.",
		"Summary:",
		"1. Primary Request and Intent:",
		"   The user asked to fix the pgmigration lock.",
		"2. Key Technical Concepts:",
		"   - dockerfile buildkit layers",
		"3. Files and Code Sections:",
		"   - dockerfile dockerfile compose compose registry",
		"7. Pending Tasks:",
		"   - rerun pgmigration against staging",
		"8. Current Work:",
		"   Retrying pgmigration after the postgres lock timeout.",
		"9. Optional Next Step:",
		"   Verify postgres advisory lock release.",
	}, "\n")
	q := " " + CompactQuery(summary) + " "
	for _, want := range []string{"pgmigration", "postgres", "lock"} {
		if !strings.Contains(q, " "+want+" ") {
			t.Errorf("focus-section term %q missing from %q", want, q)
		}
	}
	for _, noise := range []string{"dockerfile", "compose", "registry", "buildkit"} {
		if strings.Contains(q, " "+noise+" ") {
			t.Errorf("inventory-section term %q leaked into %q", noise, q)
		}
	}
}

func TestCompactQuery_MarkdownHeaders(t *testing.T) {
	// Styles seen in live summaries; the inventory must never leak in.
	styles := []struct{ primary, files, current string }{
		{"1. **Primary Request and Intent:**", "3. **Files and Code Sections:**", "8. **Current Work:**"},
		{"## 1. Primary Request and Intent", "## 3. Files and Code Sections", "## 8. Current Work"},
		{"1. Primary Request and Intent (verbatim):", "3. Files and Code Sections:", "7. Pending Tasks (all awaiting review):"},
	}
	for _, st := range styles {
		summary := strings.Join([]string{
			st.primary, "   fix the pgmigration lock",
			st.files, "   dockerfile dockerfile compose compose registry",
			st.current, "   retry pgmigration postgres",
		}, "\n")
		q := " " + CompactQuery(summary) + " "
		if !strings.Contains(q, " pgmigration ") || !strings.Contains(q, " postgres ") {
			t.Errorf("%q: focus terms missing from %q", st.primary, q)
		}
		for _, noise := range []string{"dockerfile", "compose", "registry"} {
			if strings.Contains(q, " "+noise+" ") {
				t.Errorf("%q: inventory term %q leaked into %q", st.primary, noise, q)
			}
		}
	}
}

func TestCompactQuery_NoHeadersUsesWholeSummary(t *testing.T) {
	if q := CompactQuery("free-form recap about pgmigration and postgres"); !strings.Contains(q, "pgmigration") {
		t.Errorf("headerless summary must fall back to its whole text, got %q", q)
	}
}

func TestCompactQuery_CapsTermsByFrequency(t *testing.T) {
	var b strings.Builder
	b.WriteString("8. Current Work:\n")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "uniqterm%03d ", i)
	}
	b.WriteString(strings.Repeat("hotterm ", 5))
	q := strings.Fields(CompactQuery(b.String()))
	if len(q) != 40 {
		t.Fatalf("query must be capped at 40 terms, got %d", len(q))
	}
	if q[0] != "hotterm" {
		t.Errorf("most frequent term must come first, got %q", q[0])
	}
}

func TestCompactQuery_Empty(t *testing.T) {
	if q := CompactQuery(""); q != "" {
		t.Errorf("empty summary → empty query, got %q", q)
	}
}
