package native

import (
	"os"
	"path/filepath"
	"testing"
)

// Collect is the single place the rest of v2 reads "all native memory for this
// project": the per-project store plus the global store. doctor, inject,
// and close all depend on it rather than re-deriving the layout.
func TestCollect_MergesProjectAndGlobal(t *testing.T) {
	home := t.TempDir()
	claudeHome := filepath.Join(home, ".claude")
	projDir := StoreFor(claudeHome, "/work/proj").Dir
	globDir := GlobalMemoryDir(home)
	for _, d := range []string{projDir, globDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(dir, slug, typ string) {
		body := "---\nname: " + slug + "\ntype: " + typ + "\n---\nbody\n"
		if err := os.WriteFile(filepath.Join(dir, slug+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(projDir, "proj-one", "mistake")
	write(globDir, "glob-one", "feedback")

	got := Collect(claudeHome, StoreFor(claudeHome, "/work/proj"))

	if len(got) != 2 {
		t.Fatalf("want 2 files (1 project + 1 global), got %d: %+v", len(got), got)
	}
	byName := map[string]string{}
	for _, f := range got {
		byName[f.Name] = f.Type
	}
	if byName["proj-one"] != "mistake" || byName["glob-one"] != "feedback" {
		t.Errorf("unexpected merge: %v", byName)
	}
}

// A project with no native dir yet still returns the global store, no error.
func TestCollect_MissingProjectDirIsSafe(t *testing.T) {
	home := t.TempDir()
	claudeHome := filepath.Join(home, ".claude")
	globDir := GlobalMemoryDir(home)
	if err := os.MkdirAll(globDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globDir, "g.md"),
		[]byte("---\nname: g\ntype: note\n---\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Collect(claudeHome, StoreFor(claudeHome, "/never/created"))

	if len(got) != 1 || got[0].Name != "g" {
		t.Errorf("want only the global file, got %+v", got)
	}
}

// The global store asked for itself (a drill-down into "global", a sidecar
// refresh after an action on a global fact) lists each global file once.
func TestCollect_GlobalStoreListedOnce(t *testing.T) {
	t.Setenv("HYPOMNEMA_GLOBAL_DIR", "")
	home := t.TempDir()
	claudeHome := filepath.Join(home, ".claude")
	globDir := GlobalMemoryDir(home)
	if err := os.MkdirAll(globDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"gone", "gtwo"} {
		if err := os.WriteFile(filepath.Join(globDir, slug+".md"),
			[]byte("---\nname: "+slug+"\ntype: note\n---\nb\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for _, dir := range []string{globDir, globDir + string(filepath.Separator)} {
		got := Collect(claudeHome, Store{Dir: dir, Project: GlobalProject})
		if len(got) != 2 {
			t.Fatalf("Collect(global %q): want 2 files, got %d: %+v", dir, len(got), got)
		}
		for _, f := range got {
			if f.Project != GlobalProject {
				t.Errorf("%s tagged %q, want %q", f.Slug, f.Project, GlobalProject)
			}
		}
	}
}
