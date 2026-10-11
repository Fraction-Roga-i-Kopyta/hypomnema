package usefulness

import (
	"math"
	"testing"
)

func TestEffectiveness_Table(t *testing.T) {
	cases := []struct {
		pos, neg int
		want     float64
	}{
		{0, 0, 0.5},
		{0, 1, 1 / 2.25},
		{0, 3, 1 / 2.75},
		{0, 10, 1 / 4.5},
		{1, 4, 0.5},
		{1, 1, 2 / 3.25},
		{3, 0, 0.8},
	}
	for _, c := range cases {
		if got := Effectiveness(c.pos, c.neg); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Effectiveness(%d,%d) = %v, want %v", c.pos, c.neg, got, c.want)
		}
	}
}

// More silence never raises eff; more useful citations never lower it.
func TestEffectiveness_Monotone(t *testing.T) {
	for pos := 0; pos < 6; pos++ {
		for neg := 0; neg < 20; neg++ {
			if Effectiveness(pos, neg+1) > Effectiveness(pos, neg) {
				t.Fatalf("eff rose with silence at pos=%d neg=%d", pos, neg)
			}
			if Effectiveness(pos+1, neg) < Effectiveness(pos, neg) {
				t.Fatalf("eff fell with a useful citation at pos=%d neg=%d", pos, neg)
			}
		}
	}
}
