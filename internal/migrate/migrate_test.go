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

func TestBuildPlan_SlugCollisionDisambiguated(t *testing.T) {
	v1 := t.TempDir()
	writeV1(t, v1, "projects", "akasha.md", "---\ntype: project\nproject: global\ncreated: 2026-04-01\nstatus: active\nref_count: 3\n---\nproject desc\n")
	writeV1(t, v1, "continuity", "akasha.md", "---\ntype: continuity\nproject: global\ncreated: 2026-04-01\nstatus: active\nref_count: 3\n---\nwhere we left off\n")
	p, _ := BuildPlan(Opts{V1Dir: v1, GlobalDir: "/g", OSHome: "/home/u", Today: "2026-05-29"})
	// both kept, distinct target filenames (no clobber)
	targets := map[string]int{}
	for _, c := range p.Convs {
		if c.Action == "keep" {
			targets[filepath.Join(c.TargetDir, c.Slug)]++
		}
	}
	for path, n := range targets {
		if n > 1 {
			t.Errorf("collision: %s written %d times", path, n)
		}
	}
	if len(targets) != 2 {
		t.Errorf("expected 2 distinct native targets, got %d: %v", len(targets), targets)
	}
}

func TestBuildPlan_WALActivePreventsProune(t *testing.T) {
	v1 := t.TempDir()
	// Dead-weight by frontmatter (ref_count=0, old, not pinned) — normally pruned.
	writeV1(t, v1, "notes", "n1.md", "---\ntype: note\nproject: global\ncreated: 2026-01-01\nstatus: active\nref_count: 0\n---\nunused\n")
	// WAL has an inject event for n1 → must NOT prune.
	walPath := filepath.Join(v1, ".wal")
	os.WriteFile(walPath, []byte("2026-04-01|inject|n1|s1\n"), 0o644)

	p, _ := BuildPlan(Opts{V1Dir: v1, GlobalDir: "/g", OSHome: "/home/u", Today: "2026-05-29", WALPath: walPath})
	for _, c := range p.Convs {
		if c.Slug == "n1.md" && c.Action == "prune" {
			t.Error("n1.md has WAL activity — must NOT be pruned")
		}
	}
}

func TestBuildPlan_GlobalFallbacksReported(t *testing.T) {
	v1 := t.TempDir()
	// File with a project that isn't in projects.json → routes to global, reports fallback.
	writeV1(t, v1, "feedback", "f1.md", "---\ntype: feedback\nproject: unknown-proj\ncreated: 2026-04-01\nstatus: active\nref_count: 5\n---\nrule\n")
	p, _ := BuildPlan(Opts{V1Dir: v1, GlobalDir: "/g", OSHome: "/home/u", Today: "2026-05-29"})
	if p.GlobalFallbacks == nil || p.GlobalFallbacks["unknown-proj"] != 1 {
		t.Errorf("expected GlobalFallbacks[unknown-proj]=1, got %v", p.GlobalFallbacks)
	}
}

func TestBuildPlan_QuotedNameDescription(t *testing.T) {
	v1 := t.TempDir()
	// Name and description contain colons — must be quoted in native output.
	writeV1(t, v1, "knowledge", "k1.md", "---\ntype: knowledge\nproject: global\ncreated: 2026-04-01\nstatus: active\nref_count: 2\nname: \"API: auth flow\"\ndescription: \"How to: get a token\"\n---\nbody\n")
	p, _ := BuildPlan(Opts{V1Dir: v1, GlobalDir: "/g", OSHome: "/home/u", Today: "2026-05-29"})
	var k1 FileConv
	for _, c := range p.Convs {
		if c.Slug == "k1.md" {
			k1 = c
		}
	}
	if k1.Action != "keep" {
		t.Fatalf("k1 should be kept: %+v", k1)
	}
	// name and description values must be quoted
	if !strings.Contains(k1.Native, `name: "API: auth flow"`) {
		t.Errorf("name with colon must be quoted:\n%s", k1.Native)
	}
	if !strings.Contains(k1.Native, `description: "How to: get a token"`) {
		t.Errorf("description with colon must be quoted:\n%s", k1.Native)
	}
}
