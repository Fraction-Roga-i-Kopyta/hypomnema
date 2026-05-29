package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeV1(t *testing.T, dir, sub, name, content string) {
	t.Helper()
	d := filepath.Join(dir, sub)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPlan(t *testing.T) {
	v1 := t.TempDir()
	// global mistake, kept; root-cause/prevention should land in native body.
	writeV1(t, v1, "mistakes", "m1.md", "---\ntype: mistake\nproject: global\ncreated: 2026-04-01\nstatus: active\nref_count: 12\nroot-cause: \"used getattr wrong\"\nprevention: \"use column_attrs\"\n---\n## Context\nbody here\n")
	// project feedback (akasha) → routes to akasha native dir.
	writeV1(t, v1, "feedback", "f1.md", "---\ntype: feedback\nproject: akasha\ncreated: 2026-04-01\nstatus: active\nref_count: 5\ndescription: do X\n---\nrule body\n")
	// dead weight: ref_count 0, old, not pinned → pruned.
	writeV1(t, v1, "notes", "n1.md", "---\ntype: note\nproject: global\ncreated: 2026-01-01\nstatus: active\nref_count: 0\n---\nunused\n")

	projects := map[string]string{"/home/u/dev/akasha": "akasha"}
	p, err := BuildPlan(Opts{
		V1Dir: v1, GlobalDir: "/home/u/.claude/memory-global",
		OSHome: "/home/u", Projects: projects, Today: "2026-05-29",
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if p.Kept != 2 || p.Pruned != 1 {
		t.Fatalf("kept=%d pruned=%d, want 2/1", p.Kept, p.Pruned)
	}
	byslug := map[string]FileConv{}
	for _, c := range p.Convs {
		byslug[c.Slug] = c
	}
	// m1: kept, global, fine type preserved, root-cause/prevention in body
	m1 := byslug["m1.md"]
	if m1.Action != "keep" || m1.Type != "mistake" {
		t.Errorf("m1 = %+v", m1)
	}
	if !strings.Contains(m1.Native, "type: mistake") {
		t.Errorf("m1 native must carry fine type:\n%s", m1.Native)
	}
	if !strings.Contains(m1.Native, "used getattr wrong") || !strings.Contains(m1.Native, "use column_attrs") {
		t.Errorf("m1 native body must preserve root-cause/prevention:\n%s", m1.Native)
	}
	if m1.TargetDir != "/home/u/.claude/memory-global" {
		t.Errorf("m1 global routing = %q", m1.TargetDir)
	}
	// f1: project akasha → routes to akasha native project dir
	f1 := byslug["f1.md"]
	if f1.TargetDir != "/home/u/.claude/projects/-home-u-dev-akasha/memory" {
		t.Errorf("f1 project routing = %q", f1.TargetDir)
	}
	// n1: pruned
	if byslug["n1.md"].Action != "prune" {
		t.Errorf("n1 should be pruned: %+v", byslug["n1.md"])
	}
}
