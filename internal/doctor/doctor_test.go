package doctor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newFixture builds a complete "healthy" claude+memory pair under tmp.
// Returns claudeDir, memoryDir. Individual tests degrade specific parts
// to exercise each failure mode.
func newFixture(t *testing.T) (string, string) {
	t.Helper()
	claude := t.TempDir()
	mem := filepath.Join(claude, "memory")
	for _, sub := range []string{
		"hooks", "bin", "memory/mistakes", "memory/strategies",
		"memory/feedback", "memory/knowledge",
	} {
		if err := os.MkdirAll(filepath.Join(claude, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A minimal settings.json that lists every hypomnema hook install.sh
	// registers, so checkSettings is happy out of the box.
	var hookLines []string
	for _, cmd := range requiredHookCommands {
		hookLines = append(hookLines, `"command": "~/.claude/hooks/`+cmd+`"`)
	}
	settings := `{"hooks":{"_stub":[` + strings.Join(hookLines, ",") + `]}}`
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	// memoryctl binary stub — doctor only checks existence / non-dir.
	if err := os.WriteFile(filepath.Join(claude, "bin", "memoryctl"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return claude, mem
}

func TestRun_CleanFixtureNoFails(t *testing.T) {
	claude, mem := newFixture(t)
	r := Run(claude, mem)
	_, _, fail := r.Counts()
	if fail != 0 {
		var buf bytes.Buffer
		r.Print(&buf)
		t.Errorf("expected 0 FAIL on clean fixture, got %d\n%s", fail, buf.String())
	}
	if code := r.ExitCode(); code != 0 {
		t.Errorf("expected ExitCode 0, got %d", code)
	}
}

func TestRun_MissingMemoryDirFails(t *testing.T) {
	claude := t.TempDir()
	// No memory dir created.
	r := Run(claude, filepath.Join(claude, "memory"))
	mustFindCheck(t, r, "memory_dir_exists", FAIL)
}

func TestRun_SettingsMissingHookCommandFails(t *testing.T) {
	claude, mem := newFixture(t)
	// Rewrite settings.json to omit memory-dedup.sh — reproduces the exact
	// pre-061c3e6 install.sh bug this whole subcommand exists to catch.
	var lines []string
	for _, cmd := range requiredHookCommands {
		if cmd == "memory-dedup.sh" {
			continue
		}
		lines = append(lines, `"command": "~/.claude/hooks/`+cmd+`"`)
	}
	broken := `{"hooks":{"_stub":[` + strings.Join(lines, ",") + `]}}`
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Run(claude, mem)
	c := mustFindCheck(t, r, "settings_hooks_registered", FAIL)
	if !strings.Contains(c.Detail, "memory-dedup.sh") {
		t.Errorf("expected detail to name the missing hook, got %q", c.Detail)
	}
}

func TestRun_BrokenSymlinkFails(t *testing.T) {
	claude, mem := newFixture(t)
	// Point a symlink at a nonexistent target — mirrors the left-over
	// memory-dedup.py we caught on the author's own install.
	if err := os.Symlink("/nonexistent/target.sh", filepath.Join(claude, "hooks", "stale-link.sh")); err != nil {
		t.Fatal(err)
	}
	r := Run(claude, mem)
	c := mustFindCheck(t, r, "no_broken_symlinks_hooks", FAIL)
	if !strings.Contains(c.Detail, "stale-link.sh") {
		t.Errorf("expected detail to name the broken symlink, got %q", c.Detail)
	}
}

func TestRun_WALErrorsInLast7Days(t *testing.T) {
	claude, mem := newFixture(t)
	// Seed WAL: one schema-error today, one format-unsupported yesterday,
	// one stale evidence-missing from 30 days ago (MUST be excluded by
	// the 7d window).
	now := time.Now()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	old := now.AddDate(0, 0, -30).Format("2006-01-02")
	walLines := []string{
		today + "|schema-error|broken-file|s1",
		yesterday + "|format-unsupported|future-file:2|s2",
		old + "|evidence-missing|hash123|s3",
		today + "|inject|some-slug|s4", // not an error event — must not count.
	}
	if err := os.WriteFile(filepath.Join(mem, ".wal"),
		[]byte(strings.Join(walLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Run(claude, mem)
	c := mustFindCheck(t, r, "wal_errors_last_7d", WARN)
	// Two events in window (schema-error + format-unsupported), not three.
	if !strings.Contains(c.Detail, "2 events") {
		t.Errorf("expected '2 events' in detail, got %q", c.Detail)
	}
	if strings.Contains(c.Detail, "evidence-missing") {
		t.Errorf("stale event should be excluded from 7d window; got %q", c.Detail)
	}
}

func TestRun_WALMissingIsOK(t *testing.T) {
	claude, mem := newFixture(t)
	// No .wal file — typical on a fresh install.
	r := Run(claude, mem)
	mustFindCheck(t, r, "wal_errors_last_7d", OK)
}

func TestRun_CorpusEmptyWarns(t *testing.T) {
	claude, mem := newFixture(t)
	r := Run(claude, mem)
	mustFindCheck(t, r, "corpus_counts", WARN)
}

func TestRun_CorpusWithFilesReportsCounts(t *testing.T) {
	claude, mem := newFixture(t)
	mk := func(sub, slug, status string) {
		path := filepath.Join(mem, sub, slug+".md")
		body := "---\ntype: " + sub + "\nstatus: " + status + "\n---\nBody.\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("mistakes", "m-one", "active")
	mk("mistakes", "m-two", "pinned")
	mk("strategies", "s-one", "stale")
	r := Run(claude, mem)
	c := mustFindCheck(t, r, "corpus_counts", OK)
	for _, fragment := range []string{"3 total", "mistakes:2", "strategies:1", "1 pinned", "1 stale"} {
		if !strings.Contains(c.Detail, fragment) {
			t.Errorf("expected %q in detail, got %q", fragment, c.Detail)
		}
	}
}

func TestReport_PrintJSONRoundtrips(t *testing.T) {
	claude, mem := newFixture(t)
	r := Run(claude, mem)
	var buf bytes.Buffer
	r.PrintJSON(&buf)
	// Decode into a map — we only assert structural shape here, not field
	// names, so the test doesn't couple to JSON tag renames.
	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("--json output not valid JSON: %v\n%s", err, buf.String())
	}
	if _, ok := decoded["checks"]; !ok {
		t.Errorf("JSON missing 'checks' field: %s", buf.String())
	}
}

// mustFindCheck locates a check by name and asserts its Status. Fails the
// test (not the process) with a descriptive message if missing.
func mustFindCheck(t *testing.T, r Report, name string, want Status) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			if c.Status != want {
				t.Errorf("check %q: want status %s, got %s (detail=%q)",
					name, want, c.Status, c.Detail)
			}
			return c
		}
	}
	t.Fatalf("check %q not found in report; checks: %+v", name, r.Checks)
	return Check{}
}
