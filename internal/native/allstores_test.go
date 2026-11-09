package native

import (
	"os"
	"path/filepath"
	"testing"
)

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAllStores_ListsProjectsAndGlobal(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".claude")
	t.Setenv("HYPOMNEMA_GLOBAL_DIR", filepath.Join(home, "g"))
	mkdirs(t,
		filepath.Join(cfg, "projects", "-b", "memory"),
		filepath.Join(cfg, "projects", "-a", "memory"),
		filepath.Join(cfg, "projects", "x"),
		filepath.Join(home, "g"),
	)
	got := AllStores(cfg, cfg, Store{})
	if len(got) != 3 {
		t.Fatalf("got %d stores, want 3: %+v", len(got), got)
	}
	if got[0].Project != "-a" || got[1].Project != "-b" || got[2].Project != GlobalProject {
		t.Fatalf("order/projects wrong: %+v", got)
	}
	if got[0].Dir != filepath.Join(cfg, "projects", "-a", "memory") {
		t.Fatalf("dir wrong: %s", got[0].Dir)
	}
}

func TestAllStores_IncludesOverrideStore(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".claude")
	t.Setenv("HYPOMNEMA_GLOBAL_DIR", filepath.Join(home, "g"))
	mkdirs(t, filepath.Join(cfg, "projects", "-a", "memory"))
	over := Store{Dir: filepath.Join(home, "elsewhere"), Project: "ovr"}
	got := AllStores(cfg, cfg, over)
	var found bool
	for _, s := range got {
		if s.Dir == over.Dir && s.Project == "ovr" {
			found = true
		}
	}
	if !found {
		t.Fatalf("override store missing: %+v", got)
	}
	if got[len(got)-1].Project != GlobalProject {
		t.Fatalf("global not last: %+v", got)
	}
	// a current store inside the projects tree is not duplicated
	in := Store{Dir: filepath.Join(cfg, "projects", "-a", "memory"), Project: "-a"}
	n := 0
	for _, s := range AllStores(cfg, cfg, in) {
		if s.Dir == in.Dir {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("dup store: %d", n)
	}
}

func TestStoreDirs(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".claude")
	t.Setenv("HYPOMNEMA_GLOBAL_DIR", filepath.Join(home, "g"))
	mkdirs(t, filepath.Join(cfg, "projects", "-a", "memory"))
	over := Store{Dir: filepath.Join(home, "elsewhere"), Project: "ovr"}
	got := StoreDirs(cfg, cfg, over)
	want := map[string]string{
		"-a":          filepath.Join(cfg, "projects", "-a", "memory"),
		"ovr":         over.Dir,
		GlobalProject: filepath.Join(home, "g"),
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("dirs[%q] = %q, want %q", k, got[k], v)
		}
	}
	if StoreDirs("", cfg, over) != nil || StoreDirs(cfg, "", over) != nil {
		t.Fatal("an unknown root must yield nil dirs")
	}
}
