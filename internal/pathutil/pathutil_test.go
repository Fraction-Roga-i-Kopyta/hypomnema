package pathutil

import "testing"

func TestSlugFromPath_DropsControlChars(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"pipe in basename", "/abs/path/mistakes/foo|bar.md", "foo_bar"},
		{"newline in basename", "/abs/path/mistakes/foo\nbar.md", "foo_bar"},
		{"carriage return in basename", "/abs/path/mistakes/\rfoo.md", "_foo"},
		{"all three combined", "/abs/path/mistakes/a|b\nc\rd.md", "a_b_c_d"},
		{"plain slug unchanged", "/abs/path/mistakes/foo-bar.md", "foo-bar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SlugFromPath(tc.in)
			if got != tc.want {
				t.Errorf("SlugFromPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
