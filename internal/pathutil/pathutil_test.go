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

func TestSafeFileName(t *testing.T) {
	cases := map[string]string{
		"abc-123_X.y":       "abc-123_X.y",
		"../../etc/passwd":  ".._.._etc_passwd",
		"a|b\nc":            "a_b_c",
		"sess/with/slashes": "sess_with_slashes",
		"":                  "_",
	}
	for in, want := range cases {
		if got := SafeFileName(in); got != want {
			t.Errorf("SafeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}
