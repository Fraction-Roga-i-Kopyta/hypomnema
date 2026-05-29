package ab

import (
	"fmt"
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

	events, _ := ParseWAL(writeWAL(t, b.String()))

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
	if r1.RandomRecall != 0.0 {
		t.Errorf("random recall@1 = %.4f, want 0.0 (seed 42 doesn't pick hot.md from 21 slugs)", r1.RandomRecall)
	}
	if r1.Sessions != 1 {
		t.Errorf("sessions = %d, want 1", r1.Sessions)
	}

	r3 := results[1]
	if r3.K != 3 || r3.Sessions != 1 {
		t.Errorf("K=3 row: K=%d sessions=%d, want 3/1", r3.K, r3.Sessions)
	}
	if r3.RankedRecall != 1.0 {
		t.Errorf("ranked recall@3 = %.2f, want 1.0 (hot.md in top-3)", r3.RankedRecall)
	}
}

func TestReplay_Deterministic(t *testing.T) {
	events, _ := ParseWAL(writeWAL(t, "2026-04-01|inject|a.md|s0\n2026-04-05|trigger-useful|a.md|s1\n2026-04-05|inject|b.md|s1\n"))
	a := Replay(events, []int{1, 2}, 7)
	b := Replay(events, []int{1, 2}, 7)
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("Replay not deterministic at K=%d: %+v vs %+v", a[i].K, a[i], b[i])
		}
	}
}

func TestReplay_ColdStartUsefulSlugMissedByStaticSignals(t *testing.T) {
	// Every slug's only events are ON the session date, so the strict
	// holdout leaves all candidates with zero pre-date signal — they tie on
	// score and break by slug ascending. The useful slug sorts last, so a
	// small-K ranked pick misses it: static signals can't predict a
	// first-time-useful fact. (Honest limitation the harness should surface.)
	wal := "2026-04-10|trigger-useful|zzz.md|s1\n" +
		"2026-04-10|inject|aaa.md|s1\n" +
		"2026-04-10|inject|bbb.md|s1\n" +
		"2026-04-10|inject|ccc.md|s1\n" +
		"2026-04-10|inject|ddd.md|s1\n"
	events, _ := ParseWAL(writeWAL(t, wal))
	res := Replay(events, []int{3}, 1)
	if len(res) != 1 || res[0].Sessions != 1 {
		t.Fatalf("want 1 session, got %+v", res)
	}
	if res[0].RankedRecall != 0.0 {
		t.Errorf("cold-start useful slug (zzz.md sorts last) should be MISSED at K=3; ranked recall=%.2f, want 0.0", res[0].RankedRecall)
	}
}
