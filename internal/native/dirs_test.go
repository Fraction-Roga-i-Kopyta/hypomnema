package native

import (
	"path/filepath"
	"testing"
)

func TestSlugFromCWD(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Encoding verified against a real Claude Code install; path is
		// neutralized (no real username) for the public repo.
		{"repo path", "/home/user/Development/hypomnema", "-home-user-Development-hypomnema"},
		{"tmp path", "/tmp/cap-test", "-tmp-cap-test"},
		{"root", "/", "-"},
		// Contract lock: spaces are NOT encoded today. If that changes, this
		// assertion must change deliberately.
		{"space not encoded", "/home/user/My Project", "-home-user-My Project"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SlugFromCWD(tc.in); got != tc.want {
				t.Errorf("SlugFromCWD(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestProjectMemoryDir(t *testing.T) {
	got := ProjectMemoryDir("/home/u", "/tmp/proj")
	want := filepath.Join("/home/u", ".claude", "projects", "-tmp-proj", "memory")
	if got != want {
		t.Errorf("ProjectMemoryDir = %q, want %q", got, want)
	}
}

func TestGlobalMemoryDir_Default(t *testing.T) {
	t.Setenv("HYPOMNEMA_GLOBAL_DIR", "")
	got := GlobalMemoryDir("/home/u")
	want := filepath.Join("/home/u", ".claude", "memory-global")
	if got != want {
		t.Errorf("GlobalMemoryDir = %q, want %q", got, want)
	}
}

func TestGlobalMemoryDir_Override(t *testing.T) {
	t.Setenv("HYPOMNEMA_GLOBAL_DIR", "/custom/global")
	if got := GlobalMemoryDir("/home/u"); got != "/custom/global" {
		t.Errorf("GlobalMemoryDir override = %q, want /custom/global", got)
	}
}
