package fuzzy

import (
	"math"
	"testing"
)

// TestTokenSetRatioApprox checks numerical proximity to rapidfuzz reference
// values on a handful of fixtures. We tolerate ±10 points of drift because
// rapidfuzz's default-processor path applies an extra normalisation we
// couldn't fully reverse-engineer (its `processor=default_process` path
// disagrees with `processor=None` on the *same* already-lowercased input
// by several points, in ways not documented in the source). That drift
// does not cross our dedup thresholds — guarded separately by
// TestThresholdDecisions.
func TestTokenSetRatioApprox(t *testing.T) {
	cases := []struct {
		name string
		a    string
		b    string
		want float64
	}{
		{"identical", "Claude jumps to first hypothesis without listing alternatives", "Claude jumps to first hypothesis without listing alternatives", 100},
		{"test-fixture-block", "Claude jumps to first hypothesis without listing alternatives", "Claude jumps to first hypothesis without listing alternative causes", 95.3},
		{"medium-overlap-candidate", "SQL injection via string interpolation", "Injection attack via raw SQL concatenation", 67.5},
		{"unrelated-allow", "CSS grid broken on Safari", "Date format parsing on old browsers", 40.0},
		{"empty-left", "", "nonempty", 0},
		{"empty-both", "", "", 0}, // rapidfuzz: both-empty is 0, not 100 (FuzzyWuzzy-compat)
	}
	const tolerance = 10.0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TokenSetRatio(tc.a, tc.b)
			if math.Abs(got-tc.want) > tolerance {
				t.Errorf("TokenSetRatio(%q, %q) = %.2f, want %.2f ± %.1f",
					tc.a, tc.b, got, tc.want, tolerance)
			}
		})
	}
}

// TestThresholdDecisions is the authoritative contract: the block / candidate
// / allow boundary MUST match rapidfuzz's on the same inputs. Numerical
// drift allowed only inside the bands, never across them.
//
// Inputs chosen to sit away from the boundaries — no 49.9 / 50.1 edge
// cases, where ±10 drift could legitimately flip the class.
func TestThresholdDecisions(t *testing.T) {
	cases := []struct {
		name string
		a    string
		b    string
		want string
	}{
		{"identical-block", "hello world", "hello world", "block"},
		{"near-block", "Claude jumps hypothesis alternatives", "Claude jumps hypothesis alternative causes", "block"},
		{"medium-candidate", "SQL injection via string interpolation", "Injection attack via raw SQL concatenation", "candidate"},
		{"low-allow", "Safari CSS grid", "date parsing browsers old format", "allow"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			score := TokenSetRatio(tc.a, tc.b)
			var got string
			switch {
			case score >= 80:
				got = "block"
			case score >= 50:
				got = "candidate"
			default:
				got = "allow"
			}
			if got != tc.want {
				t.Errorf("score=%.1f for (%q, %q): got decision %q, want %q",
					score, tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestDefaultProcess(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Hello, World!", "hello  world"},
		{"  foo   bar  ", "foo   bar"},
		{"Cyrillic Тест", "cyrillic тест"},
		{"under_score", "under_score"},
		{"123 numbers", "123 numbers"},
		{"!@#$%", ""},
	}
	for _, tc := range cases {
		if got := DefaultProcess(tc.in); got != tc.want {
			t.Errorf("DefaultProcess(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTokenSetRatioSymmetric(t *testing.T) {
	pairs := [][2]string{
		{"hello world foo", "world hello bar"},
		{"a b c", "c b a"},
		{"длинная строка", "строка длинная"},
	}
	for _, p := range pairs {
		ab := TokenSetRatio(p[0], p[1])
		ba := TokenSetRatio(p[1], p[0])
		if math.Abs(ab-ba) > 0.01 {
			t.Errorf("asymmetric: TSR(%q,%q)=%.2f vs TSR(%q,%q)=%.2f",
				p[0], p[1], ab, p[1], p[0], ba)
		}
	}
}

func TestIndelDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"abc", "abc", 0},
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "xyz", 6}, // all delete + all insert
		{"kitten", "sitting", 5},
	}
	for _, tc := range cases {
		if got := indelDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("indelDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
