package migrate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecuteAndRollback(t *testing.T) {
	home := t.TempDir()
	v1 := filepath.Join(home, ".claude", "memory")
	globalDir := filepath.Join(home, ".claude", "memory-global")
	writeV1(t, v1, "feedback", "f1.md", "---\ntype: feedback\nproject: global\ncreated: 2026-04-01\nstatus: active\nref_count: 5\n---\nrule body\n")
	os.WriteFile(filepath.Join(v1, ".wal"), []byte("2026-04-01|inject|f1.md|s0\n"), 0o644)

	p, _ := BuildPlan(Opts{V1Dir: v1, GlobalDir: globalDir, OSHome: home, Today: "2026-05-29"})
	backup := filepath.Join(home, ".claude", "memory.v1-backup")
	if _, err := Execute(p, ExecOpts{V1Dir: v1, BackupDir: backup, MemoryDir: v1}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// native file written to global dir
	if _, err := os.Stat(filepath.Join(globalDir, "f1.md")); err != nil {
		t.Errorf("native f1.md not written: %v", err)
	}
	// old store backed up (not deleted)
	if _, err := os.Stat(filepath.Join(backup, "feedback", "f1.md")); err != nil {
		t.Errorf("old store not backed up: %v", err)
	}
	// idempotent: second run must not error
	p2, _ := BuildPlan(Opts{V1Dir: v1, GlobalDir: globalDir, OSHome: home, Today: "2026-05-29"})
	if _, err := Execute(p2, ExecOpts{V1Dir: v1, BackupDir: backup, MemoryDir: v1}); err != nil {
		t.Fatalf("second Execute (idempotent) failed: %v", err)
	}

	// rollback restores from backup
	if err := Rollback(ExecOpts{V1Dir: v1, BackupDir: backup, MemoryDir: v1}); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, err := os.Stat(filepath.Join(v1, "feedback", "f1.md")); err != nil {
		t.Errorf("rollback did not restore old store: %v", err)
	}
}

func TestExecute_SidecarFailureSurfacedNotSwallowed(t *testing.T) { // review: migrate swallow
	home := t.TempDir()
	v1 := filepath.Join(home, ".claude", "memory")
	globalDir := filepath.Join(home, ".claude", "memory-global")
	writeV1(t, v1, "feedback", "f1.md", "---\ntype: feedback\nproject: global\ncreated: 2026-04-01\nstatus: active\nref_count: 5\n---\nrule\n")
	os.WriteFile(filepath.Join(v1, ".wal"), []byte("2026-04-01|inject|f1.md|s0\n"), 0o644)
	// Make .sidecar.db a DIRECTORY so sidecar.Open fails.
	os.MkdirAll(filepath.Join(v1, ".sidecar.db"), 0o755)

	p, _ := BuildPlan(Opts{V1Dir: v1, GlobalDir: globalDir, OSHome: home, Today: "2026-05-29"})
	rebuilt, err := Execute(p, ExecOpts{V1Dir: v1, BackupDir: filepath.Join(home, ".claude", "memory.v1-backup"), MemoryDir: v1})
	if err != nil {
		t.Fatalf("migration itself must still succeed (files written): %v", err)
	}
	if rebuilt {
		t.Error("sidecar rebuild should have failed (path is a directory) — Execute reported rebuilt=true")
	}
	// The migration's native file was still written despite the sidecar failure.
	if _, err := os.Stat(filepath.Join(globalDir, "f1.md")); err != nil {
		t.Errorf("native file not written: %v", err)
	}
}
