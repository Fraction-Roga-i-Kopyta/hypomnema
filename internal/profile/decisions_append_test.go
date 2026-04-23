package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAppendDecisionsPressure_NoDecisionsDirIsNoop is the parity
// guardrail: on fixtures without personal ADRs, Append must leave
// self-profile.md untouched so bash and Go paths match.
func TestAppendDecisionsPressure_NoDecisionsDirIsNoop(t *testing.T) {
	mem := t.TempDir()
	if err := os.WriteFile(filepath.Join(mem, "self-profile.md"),
		[]byte("# Profile\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AppendDecisionsPressure(mem); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(mem, "self-profile.md"))
	if strings.Contains(string(data), "Decisions under pressure") {
		t.Errorf("Append must be a no-op without decisions/, but section was added:\n%s", data)
	}
}

func TestAppendDecisionsPressure_OverdueRendered(t *testing.T) {
	mem := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mem, "decisions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mem, "self-profile.md"),
		[]byte("# Profile\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Calendar trigger already in the past → overdue.
	adr := "---\ntype: decision\nstatus: active\nreview-triggers:\n  - after: \"2020-01-01\"\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(mem, "decisions", "old-adr.md"),
		[]byte(adr), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPOMNEMA_TODAY", "2026-04-23")

	if err := AppendDecisionsPressure(mem); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(mem, "self-profile.md"))
	out := string(data)
	if !strings.Contains(out, "## Decisions under pressure") {
		t.Errorf("missing section header:\n%s", out)
	}
	if !strings.Contains(out, "old-adr") {
		t.Errorf("section should name old-adr:\n%s", out)
	}
}

func TestAppendDecisionsPressure_AllOKLeavesUntouched(t *testing.T) {
	mem := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mem, "decisions"), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "# Profile\nBody.\n"
	if err := os.WriteFile(filepath.Join(mem, "self-profile.md"),
		[]byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	// Future calendar → ok.
	adr := "---\ntype: decision\nstatus: active\nreview-triggers:\n  - after: \"2099-01-01\"\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(mem, "decisions", "future.md"),
		[]byte(adr), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPOMNEMA_TODAY", "2026-04-23")

	if err := AppendDecisionsPressure(mem); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(mem, "self-profile.md"))
	if string(data) != original {
		t.Errorf("file must be unchanged when no triggers fire\noriginal:\n%s\ngot:\n%s", original, data)
	}
}
