package native

import (
	"path/filepath"
	"testing"
)

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
