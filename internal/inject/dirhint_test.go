package inject

import (
	"path/filepath"
	"testing"
)

// A session launched from $HOME has no project to hint at; the login name
// it would contribute is in every fact that quotes an absolute path. Any
// project directory, including one nested under $HOME, keeps its hint.
func TestKeywords_DirHintSkipsHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "jdoe")
	t.Setenv("HOME", home)

	cases := []struct {
		cwd     string
		absent  string
		present string
	}{
		{cwd: home, absent: "jdoe"},
		{cwd: home + string(filepath.Separator), absent: "jdoe"},
		{cwd: filepath.Join(home, "billing-api"), present: "billing"},
		{cwd: filepath.Join(t.TempDir(), "jdoe"), present: "jdoe"},
	}
	for _, c := range cases {
		got := keywords(c.cwd, "redis eviction", false)
		if !has(got, "redis") {
			t.Errorf("cwd %q: prompt terms lost, got %v", c.cwd, got)
		}
		if c.absent != "" && has(got, c.absent) {
			t.Errorf("cwd %q: %q must not be a query term, got %v", c.cwd, c.absent, got)
		}
		if c.present != "" && !has(got, c.present) {
			t.Errorf("cwd %q: %q must stay a query term, got %v", c.cwd, c.present, got)
		}
	}
}
