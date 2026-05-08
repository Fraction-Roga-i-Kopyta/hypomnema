package invariants

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupRepo builds a synthetic repo layout (internal/foo/foo.go,
// hooks/foo.sh, …) so tests can probe the checkers in isolation
// without depending on the real codebase state.
func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustMkdir := func(p string) {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustMkdir(filepath.Join(dir, "internal", "pkg"))
	mustMkdir(filepath.Join(dir, "hooks"))
	return dir
}

func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- I3 atomic-write checker ---

func TestAtomicWriteChecker_FlagsRawWriteFile(t *testing.T) {
	dir := setupRepo(t)
	writeFile(t, dir, "internal/pkg/bad.go",
		"package pkg\nimport \"os\"\nfunc Bad() error { return os.WriteFile(\"x\", []byte{}, 0o644) }\n")

	v, err := atomicWriteChecker{}.Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(v), v)
	}
	if v[0].ID != "I3" {
		t.Errorf("ID mismatch: %v", v[0])
	}
	if !strings.Contains(v[0].Path, "internal/pkg/bad.go") {
		t.Errorf("path should mention bad.go: %v", v[0])
	}
}

func TestAtomicWriteChecker_AllowsTestFiles(t *testing.T) {
	dir := setupRepo(t)
	writeFile(t, dir, "internal/pkg/foo_test.go",
		"package pkg\nimport \"os\"\nfunc Test() { _ = os.WriteFile(\"x\", []byte{}, 0o644) }\n")
	v, _ := atomicWriteChecker{}.Check(dir)
	if len(v) != 0 {
		t.Errorf("test file should be exempt, got %+v", v)
	}
}

func TestAtomicWriteChecker_NoMatchOnCleanFile(t *testing.T) {
	dir := setupRepo(t)
	writeFile(t, dir, "internal/pkg/clean.go",
		"package pkg\nimport \"os\"\nfunc Clean() { _, _ = os.CreateTemp(\".\", \"x\") }\n")
	v, _ := atomicWriteChecker{}.Check(dir)
	if len(v) != 0 {
		t.Errorf("CreateTemp file should pass, got %+v", v)
	}
}

// --- I5 hook fail-safe checker ---

func TestHookFailSafeChecker_FlagsBadSetE(t *testing.T) {
	dir := setupRepo(t)
	writeFile(t, dir, "hooks/bad.sh",
		"#!/bin/bash\nset -e\necho ok\n")
	v, err := hookFailSafeChecker{}.Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(v), v)
	}
	if v[0].ID != "I5" {
		t.Errorf("ID mismatch: %v", v[0])
	}
}

func TestHookFailSafeChecker_FlagsCombinedFlags(t *testing.T) {
	dir := setupRepo(t)
	writeFile(t, dir, "hooks/bad-eu.sh", "#!/bin/bash\nset -euo pipefail\necho ok\n")
	v, _ := hookFailSafeChecker{}.Check(dir)
	if len(v) != 1 {
		t.Errorf("expected 1 violation for `set -euo pipefail`, got %+v", v)
	}
}

func TestHookFailSafeChecker_AllowsPipefailOnly(t *testing.T) {
	dir := setupRepo(t)
	writeFile(t, dir, "hooks/good.sh",
		"#!/bin/bash\nset -o pipefail\necho ok\n")
	v, _ := hookFailSafeChecker{}.Check(dir)
	if len(v) != 0 {
		t.Errorf("`set -o pipefail` alone should pass, got %+v", v)
	}
}

func TestHookFailSafeChecker_AllowsApprovedFailFastHook(t *testing.T) {
	dir := setupRepo(t)
	writeFile(t, dir, "hooks/test-memory-hooks.sh",
		"#!/bin/bash\nset -e\nrun_tests\n")
	v, _ := hookFailSafeChecker{}.Check(dir)
	if len(v) != 0 {
		t.Errorf("test-memory-hooks.sh is in approved list, got %+v", v)
	}
}

// --- All() ---

func TestAll_ReturnsCheckers(t *testing.T) {
	all := All()
	if len(all) < 2 {
		t.Errorf("expected at least 2 checkers, got %d", len(all))
	}
	seen := map[string]bool{}
	for _, c := range all {
		if seen[c.ID()] {
			t.Errorf("duplicate checker ID: %s", c.ID())
		}
		seen[c.ID()] = true
	}
}
