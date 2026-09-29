package closer

import (
	"reflect"
	"strings"
	"testing"
)

func TestCitations(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"single", `x <cc-memory filenames="docker.md">used it</cc-memory> y`, []string{"docker.md"}},
		{"several, spaces, dedup, order", `<cc-memory filenames=" b.md , a.md,b.md ">s</cc-memory><cc-memory filenames="c.md">t</cc-memory>`, []string{"b.md", "a.md", "c.md"}},
		{"tag name variants and case", `<CC_MEMORY filenames="u.md">s</CC_MEMORY><ccmemory FILENAMES="v.md">t</ccmemory>`, []string{"u.md", "v.md"}},
		{"paths reduced to basename", `<cc-memory filenames="/Users/x/.claude/projects/p/memory/w.md,memory/z.md">s</cc-memory>`, []string{"w.md", "z.md"}},
		{"missing .md added", `<cc-memory filenames="plain">s</cc-memory>`, []string{"plain.md"}},
		{"unclosed tag still counts", `<cc-memory filenames="open.md">s`, []string{"open.md"}},
		{"no filenames attribute", `<cc-memory>s</cc-memory>`, nil},
		{"closing tag only", `</cc-memory>`, nil},
		{"not a tag", `cc-memory filenames="n.md"`, nil},
		{"empty", ``, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Citations(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Citations(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestCitations_Capped(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString(`<cc-memory filenames="f`)
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(`.md">s</cc-memory>`)
	}
	for i := 0; i < 300; i++ {
		b.WriteString(`<cc-memory filenames="n` + strings.Repeat("y", i) + `.md">s</cc-memory>`)
	}
	if got := len(Citations(b.String())); got > 256 {
		t.Errorf("citations must be capped at 256, got %d", got)
	}
}
