package ab

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplay_RankedBeatsRandomWhenSignalPredicts(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "2026-04-01|inject|cold%02d.md|s0\n", i)
	}
	b.WriteString("2026-04-01|inject-agg|hot.md|50\n")
	b.WriteString("2026-04-01|outcome-positive|hot.md|s0\n")
	b.WriteString("2026-04-10|inject|hot.md|s1\n")
	b.WriteString("2026-04-10|trigger-useful|hot.md|s1\n")

	p := filepath.Join(t.TempDir(), ".wal")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	events, _ := ParseWAL(p)

	results := Replay(events, []int{1, 3}, 42)
	if len(results) != 2 {
		t.Fatalf("expected 2 K-rows, got %d", len(results))
	}
	r1 := results[0]
	if r1.K != 1 {
		t.Fatalf("first row K=%d, want 1", r1.K)
	}
	if r1.RankedRecall != 1.0 {
		t.Errorf("ranked recall@1 = %.2f, want 1.0 (hot.md ranks first)", r1.RankedRecall)
	}
	if r1.RandomRecall >= r1.RankedRecall {
		t.Errorf("random recall@1 (%.2f) should be < ranked (%.2f)", r1.RandomRecall, r1.RankedRecall)
	}
	if r1.Sessions != 1 {
		t.Errorf("sessions = %d, want 1", r1.Sessions)
	}
}

func TestReplay_Deterministic(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".wal")
	os.WriteFile(p, []byte("2026-04-01|inject|a.md|s0\n2026-04-05|trigger-useful|a.md|s1\n2026-04-05|inject|b.md|s1\n"), 0o644)
	events, _ := ParseWAL(p)
	a := Replay(events, []int{1, 2}, 7)
	b := Replay(events, []int{1, 2}, 7)
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("Replay not deterministic at K=%d: %+v vs %+v", a[i].K, a[i], b[i])
		}
	}
}
