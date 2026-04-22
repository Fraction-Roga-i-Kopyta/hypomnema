// Package fuzzy reproduces rapidfuzz's `token_set_ratio` + default_process in Go.
//
// The bash dedup hook shells out to python3+rapidfuzz for similarity scoring;
// porting it here lets memoryctl handle dedup end-to-end and drops the uv +
// rapidfuzz runtime dependency. Parity is enforced at the score-value level
// (see fuzzy_test.go) against known rapidfuzz outputs on reference fixtures.
//
// Algorithm lifted directly from rapidfuzz/fuzz_py.py `token_set_ratio`:
// three candidate ratios are compared and the max is returned —
//   result        : indel-normalised distance between diff_ab and diff_ba,
//                   normalised on the combined sect+ab / sect+ba lengths.
//   sect_ab_ratio : trivial ratio of sect vs sect+ab (only ab differs).
//   sect_ba_ratio : trivial ratio of sect vs sect+ba.
// This is NOT equivalent to a naive Ratio(t1, t2) on joined strings — the
// normalisation denominators matter, especially when the intersection is
// small.
package fuzzy

import (
	"strings"
	"unicode"
)

// DefaultProcess mirrors rapidfuzz.utils.default_process:
//   1. lowercase
//   2. replace every non-alphanumeric rune with a space (Unicode-aware)
//   3. trim leading/trailing whitespace
// Used implicitly by TokenSetRatio on both operands before tokenisation.
func DefaultProcess(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.TrimSpace(b.String())
}

// Ratio is rapidfuzz's indel-normalised similarity of two strings, scaled 0..100.
//   similarity = 100 * (1 - indel(a, b) / (len(a) + len(b)))
//               = 100 * 2 * LCS(a, b) / (len(a) + len(b))
// Runes, not bytes, so Cyrillic/CJK score correctly.
func Ratio(a, b string) float64 {
	if a == "" && b == "" {
		return 100
	}
	if a == "" || b == "" {
		return 0
	}
	ar := []rune(a)
	br := []rune(b)
	lcs := lcsLen(ar, br)
	return 100.0 * 2.0 * float64(lcs) / float64(len(ar)+len(br))
}

// TokenSetRatio replicates rapidfuzz.fuzz.token_set_ratio with the default
// (default_process) preprocessor applied to both operands.
//
// Steps follow the rapidfuzz source so behaviour and threshold decisions
// match. Score boundaries (≥80 block, ≥50 candidate, <50 allow) are the
// surface used by memoryctl dedup; the fuzzy_test.go parity cases pin the
// thresholds against known rapidfuzz values.
func TokenSetRatio(a, b string) float64 {
	a = DefaultProcess(a)
	b = DefaultProcess(b)

	aSet := uniqSortedFields(a)
	bSet := uniqSortedFields(b)

	// FuzzyWuzzy-compat: empty token set on either side → 0, regardless of
	// how the other side looks. (See rapidfuzz/RapidFuzz#110.)
	if len(aSet) == 0 || len(bSet) == 0 {
		return 0
	}

	intersect := intersectSorted(aSet, bSet)
	diffAB := differenceSorted(aSet, intersect)
	diffBA := differenceSorted(bSet, intersect)

	// Subset short-circuit: if the intersection covers one of the sides
	// completely, the two sentences are considered fully compatible.
	if len(intersect) > 0 && (len(diffAB) == 0 || len(diffBA) == 0) {
		return 100
	}

	sectJoined := strings.Join(intersect, " ")
	diffABJoined := strings.Join(diffAB, " ")
	diffBAJoined := strings.Join(diffBA, " ")

	sectLen := len([]rune(sectJoined))
	abLen := len([]rune(diffABJoined))
	baLen := len([]rune(diffBAJoined))

	// Separator: one space between sect and the diff block, only if sect
	// is non-empty.
	sepAB, sepBA := 0, 0
	if sectLen > 0 {
		sepAB = 1
		sepBA = 1
	}
	sectABLen := sectLen + sepAB + abLen
	sectBALen := sectLen + sepBA + baLen

	// result: indel distance between the two diff-joined strings, normalised
	// on the combined sect+ab / sect+ba lengths.
	var result float64
	if sectABLen+sectBALen > 0 {
		dist := indelDistance(diffABJoined, diffBAJoined)
		result = 100.0 * (1.0 - float64(dist)/float64(sectABLen+sectBALen))
	}

	// Exit early when intersection is empty: the two trivial ratios below
	// are 0 by construction (sect is empty, so sect vs sect+ab degenerates).
	if sectLen == 0 {
		return result
	}

	// sect_ab_ratio: sect string vs (sect + ab). The non-shared part is
	// exactly (sep + ab_len) characters, so distance is trivial.
	sectABDist := sepAB + abLen
	sectABRatio := 100.0 * (1.0 - float64(sectABDist)/float64(sectLen+sectABLen))

	sectBADist := sepBA + baLen
	sectBARatio := 100.0 * (1.0 - float64(sectBADist)/float64(sectLen+sectBALen))

	return maxFloat3(result, sectABRatio, sectBARatio)
}

// indelDistance returns the Levenshtein-indel distance (insertions +
// deletions, no substitutions) between two strings, operating on runes.
// Equivalent to `len(a) + len(b) - 2 * LCS(a, b)`.
func indelDistance(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	la, lb := len(ar), len(br)
	return la + lb - 2*lcsLen(ar, br)
}

// uniqSortedFields tokenises on whitespace, deduplicates, and sorts
// lexicographically — the sort order is what makes token_set_ratio
// stable across input permutations ("foo bar" scores same as "bar foo").
func uniqSortedFields(s string) []string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func intersectSorted(a, b []string) []string {
	var out []string
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}

func differenceSorted(a, inter []string) []string {
	var out []string
	i, j := 0, 0
	for i < len(a) {
		if j >= len(inter) {
			out = append(out, a[i:]...)
			break
		}
		switch {
		case a[i] == inter[j]:
			i++
			j++
		case a[i] < inter[j]:
			out = append(out, a[i])
			i++
		default:
			j++
		}
	}
	return out
}

// lcsLen computes the longest common subsequence length of two rune slices.
// Rolling two-row DP — O(m*n) time, O(min(m,n)) space.
func lcsLen(a, b []rune) int {
	m, n := len(a), len(b)
	if m == 0 || n == 0 {
		return 0
	}
	if m < n {
		a, b = b, a
		m, n = n, m
	}
	prev := make([]int, n+1)
	curr := make([]int, n+1)
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				curr[j] = prev[j-1] + 1
			} else {
				if prev[j] > curr[j-1] {
					curr[j] = prev[j]
				} else {
					curr[j] = curr[j-1]
				}
			}
		}
		prev, curr = curr, prev
		for j := range curr {
			curr[j] = 0
		}
	}
	return prev[n]
}

func maxFloat3(a, b, c float64) float64 {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	return m
}
