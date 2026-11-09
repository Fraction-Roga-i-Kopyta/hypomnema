package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

func sidecarRec(t *testing.T, memDir, slug string) sidecar.Record {
	t.Helper()
	s, err := sidecar.Open(filepath.Join(memDir, ".sidecar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, ok, err := s.Get(slug)
	if err != nil || !ok {
		t.Fatalf("sidecar row %s: ok=%v err=%v", slug, ok, err)
	}
	return r
}

func TestPin_SetsPinnedAndWAL(t *testing.T) {
	env, _, memDir, factPath := retireFixture(t)
	_, errOut, code := run(t, env, "pin", "old-rule")
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut)
	}
	b, _ := os.ReadFile(factPath)
	if !strings.Contains(string(b), "status: pinned") {
		t.Fatalf("file not pinned:\n%s", b)
	}
	w, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if !strings.Contains(string(w), "|pin|") {
		t.Fatalf("pin WAL row missing: %s", w)
	}
	if r := sidecarRec(t, memDir, "old-rule.md"); r.Status != "pinned" {
		t.Fatalf("sidecar status=%q", r.Status)
	}
}

func TestUnpin_SetsActive(t *testing.T) {
	env, _, memDir, factPath := retireFixture(t)
	if _, e, c := run(t, env, "pin", "old-rule"); c != 0 {
		t.Fatal(e)
	}
	if _, e, c := run(t, env, "unpin", "old-rule"); c != 0 {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(factPath)
	if !strings.Contains(string(b), "status: active") {
		t.Fatalf("file:\n%s", b)
	}
	w, _ := os.ReadFile(filepath.Join(memDir, ".wal"))
	if !strings.Contains(string(w), "|unpin|") {
		t.Fatalf("unpin row missing: %s", w)
	}
	if r := sidecarRec(t, memDir, "old-rule.md"); r.Status != "active" {
		t.Fatalf("sidecar status=%q", r.Status)
	}
}

func TestPin_MissingFact(t *testing.T) {
	env, _, _, _ := retireFixture(t)
	_, errOut, code := run(t, env, "pin", "nope")
	if code != 1 || !strings.Contains(errOut, "no fact named") {
		t.Fatalf("exit=%d stderr=%s", code, errOut)
	}
}

func TestPin_WALFailureRestores(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	env, _, memDir, factPath := retireFixture(t)
	walPath := filepath.Join(memDir, ".wal")
	if err := os.WriteFile(walPath, nil, 0o444); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(factPath)
	_, errOut, code := run(t, env, "pin", "old-rule")
	if code != 1 {
		t.Fatalf("exit=%d stderr=%s", code, errOut)
	}
	after, _ := os.ReadFile(factPath)
	if string(before) != string(after) {
		t.Fatalf("fact changed:\n%s", after)
	}
}

func TestPin_KeepsOtherFactsStale(t *testing.T) {
	env, store, memDir, _ := retireFixture(t)
	other := filepath.Join(store, "other.md")
	if err := os.WriteFile(other, []byte("---\ntype: note\nname: other\ndescription: d\nstatus: active\n---\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, ".wal"), []byte("2025-01-01|inject|other.md|s0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, e, c := run(t, env, "pin", "old-rule"); c != 0 {
		t.Fatal(e)
	}
	if r := sidecarRec(t, memDir, "other.md"); r.Status != "stale" {
		t.Fatalf("precondition: other status=%q", r.Status)
	}
	if _, e, c := run(t, env, "unpin", "old-rule"); c != 0 {
		t.Fatal(e)
	}
	if r := sidecarRec(t, memDir, "other.md"); r.Status != "stale" {
		t.Fatalf("other fact lost stale: %q", r.Status)
	}
}

func TestPin_DoesNotTouchCreated(t *testing.T) {
	env, store, memDir, _ := retireFixture(t)
	if err := os.WriteFile(filepath.Join(store, "nodate.md"),
		[]byte("---\ntype: note\nname: nodate\ndescription: d\nstatus: active\n---\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, e, c := run(t, env, "pin", "nodate"); c != 0 {
		t.Fatal(e)
	}
	if _, e, c := run(t, env, "unpin", "nodate"); c != 0 {
		t.Fatal(e)
	}
	if r := sidecarRec(t, memDir, "nodate.md"); r.Created != "" {
		t.Fatalf("created=%q, want empty", r.Created)
	}
}
