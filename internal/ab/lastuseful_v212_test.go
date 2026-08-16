package ab

import "testing"

func TestSignalsBefore_LastUseful(t *testing.T) {
	events := []Event{
		{Date: "2026-04-01", Kind: "inject", Slug: "a.md", Field: "s1"},
		{Date: "2026-04-01", Kind: "trigger-useful", Slug: "a.md", Field: "s1"},
		{Date: "2026-04-03", Kind: "recall", Slug: "a.md", Field: "s2"},         // delivery, not use
		{Date: "2026-04-05", Kind: "trigger-useful", Slug: "a.md", Field: "s3"}, // on/after cutoff: excluded
		{Date: "2026-04-02", Kind: "trigger-silent", Slug: "b.md", Field: "s1"},
	}
	sig := SignalsBefore(events, "2026-04-05")
	if sig["a.md"].LastUseful != "2026-04-01" {
		t.Errorf("a.md LastUseful = %q, want 2026-04-01 (recall does not count; cutoff is strict)", sig["a.md"].LastUseful)
	}
	if sig["a.md"].RefCount != 1 {
		t.Errorf("a.md RefCount = %d, want 1 (replay accounting otherwise unchanged)", sig["a.md"].RefCount)
	}
	if sig["b.md"].LastUseful != "" {
		t.Errorf("b.md LastUseful = %q, want empty (silent only)", sig["b.md"].LastUseful)
	}
}
