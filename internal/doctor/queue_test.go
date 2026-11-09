package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

func findQueueCheck(r Report) Check {
	for _, c := range r.Checks {
		if c.Name == "memory_queue" {
			return c
		}
	}
	return Check{Name: "<missing>"}
}

func TestDoctor_MemoryQueueStaleWarn(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	st := native.StoreFor(claude, cwd)
	if err := os.MkdirAll(st.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: oldfact\ntype: knowledge\ndescription: d\nstatus: active\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(st.Dir, "oldfact.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	wal := "2026-10-01|mark|" + native.QKey(st.Project, "oldfact") + ":rewrite:" + native.BodySHA(body) + "|cli\n"
	walPath := filepath.Join(mem, ".wal")
	if err := os.WriteFile(walPath, []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := sidecar.Open(filepath.Join(mem, ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	files := native.Collect(claude, st)
	if err := sidecar.ReprojectAll(s, files, walPath, st.Scope(), native.StoreDirs(claude, claude, st)); err != nil {
		t.Fatal(err)
	}
	s.Close()

	t.Setenv("HYPOMNEMA_TODAY", "2026-11-09")
	c := findQueueCheck(Run(claude, mem, st, cwd))
	if c.Status != WARN || !strings.Contains(c.Detail, "1 open task(s) older than 14 days: oldfact") {
		t.Fatalf("want WARN, got %+v", c)
	}
	t.Setenv("HYPOMNEMA_TODAY", "2026-10-10")
	if c := findQueueCheck(Run(claude, mem, st, cwd)); c.Status != OK {
		t.Fatalf("want OK for fresh task, got %+v", c)
	}
}
