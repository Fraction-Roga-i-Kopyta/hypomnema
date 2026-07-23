package doctor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

// fixtureCWD is the working directory every fixture pretends the project
// lives in. native.Collect encodes it into the per-project slug ("/" → "-"),
// so the project memory dir is <home>/.claude/projects/-work-proj/memory.
const fixtureCWD = "/work/proj"

// newFixture builds a complete "healthy" v2 native install under tmp.
// The claude dir is <tmp>/.claude (its parent is the synthetic home, which
// native.Collect derives via filepath.Dir(claudeDir)). Returns claudeDir,
// memoryDir, cwd. Individual tests degrade specific parts to exercise each
// failure mode, or write native files into the project/global dirs.
func newFixture(t *testing.T) (claude, mem, cwd string) {
	t.Helper()
	home := t.TempDir()
	claude = filepath.Join(home, ".claude")
	mem = filepath.Join(claude, "memory")
	cwd = fixtureCWD
	// v2 layout: metadata root (memory/), per-project native dir, global
	// native dir, plus the install scaffolding (hooks/, bin/).
	for _, dir := range []string{
		filepath.Join(claude, "hooks"),
		filepath.Join(claude, "bin"),
		mem,
		filepath.Join(claude, "projects", strings.ReplaceAll(cwd, "/", "-"), "memory"),
		filepath.Join(claude, "memory-global"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A valid settings.json wiring every shim under its correct event, so
	// checkSettings (which parses the structure) is happy out of the box.
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(validSettingsJSON(nil)), 0o644); err != nil {
		t.Fatal(err)
	}
	// memoryctl binary stub — doctor only checks existence / non-dir.
	if err := os.WriteFile(filepath.Join(claude, "bin", "memoryctl"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Executable shim files — checkShimFiles verifies presence + exec bit.
	shimDir := filepath.Join(claude, "hooks", "v2")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range requiredHookCommands {
		if err := os.WriteFile(filepath.Join(shimDir, cmd), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return claude, mem, cwd
}

// validSettingsJSON builds a real Claude Code settings.json wiring each
// required shim under its correct hook event (per shimEvent). Shims named in
// omit are left out — used to simulate a partial/broken registration.
func validSettingsJSON(omit map[string]bool) string {
	byEvent := map[string][]string{}
	for _, shim := range requiredHookCommands {
		if omit[shim] {
			continue
		}
		ev := shimEvent[shim]
		byEvent[ev] = append(byEvent[ev],
			`{"hooks":[{"type":"command","command":"~/.claude/hooks/v2/`+shim+`"}]}`)
	}
	var events []string
	for ev, entries := range byEvent {
		events = append(events, `"`+ev+`":[`+strings.Join(entries, ",")+`]`)
	}
	return `{"hooks":{` + strings.Join(events, ",") + `}}`
}

// projDir returns the per-project native memory dir inside the fixture.
func projDir(claude, cwd string) string {
	return filepath.Join(claude, "projects", strings.ReplaceAll(cwd, "/", "-"), "memory")
}

// globalDir returns the global native memory dir inside the fixture.
func globalDir(claude string) string {
	return filepath.Join(claude, "memory-global")
}

// writeNative writes a flat native memory file with the given type/status
// frontmatter into dir.
func writeNative(t *testing.T, dir, slug, typ, status string) {
	t.Helper()
	body := "---\ntype: " + typ + "\nstatus: " + status + "\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedSidecar creates a real .sidecar.db in mem and upserts one row per
// (slug → status) pair. Status in v2 lives ONLY in the sidecar, so the
// doctor's pinned/stale counts must come from here, not file frontmatter.
func seedSidecar(t *testing.T, mem string, statusBySlug map[string]string) {
	t.Helper()
	s, err := sidecar.Open(filepath.Join(mem, ".sidecar.db"))
	if err != nil {
		t.Fatalf("seedSidecar: open: %v", err)
	}
	defer s.Close()
	for slug, status := range statusBySlug {
		if err := s.Upsert(sidecar.Record{Slug: slug, Status: status}); err != nil {
			t.Fatalf("seedSidecar: upsert %q: %v", slug, err)
		}
	}
}

func TestRun_CleanFixtureNoFails(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	r := Run(claude, mem, cwd)
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
	claude := filepath.Join(t.TempDir(), ".claude")
	// No memory dir created.
	r := Run(claude, filepath.Join(claude, "memory"), fixtureCWD)
	mustFindCheck(t, r, "memory_dir_exists", FAIL)
}

func TestRun_SettingsMissingHookCommandFails(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	// Rewrite settings.json to omit session-stop.sh — verifies that a
	// missing v2 shim is caught by checkSettings.
	if err := os.WriteFile(filepath.Join(claude, "settings.json"),
		[]byte(validSettingsJSON(map[string]bool{"session-stop.sh": true})), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Run(claude, mem, cwd)
	c := mustFindCheck(t, r, "settings_hooks_registered", FAIL)
	if !strings.Contains(c.Detail, "session-stop.sh") {
		t.Errorf("expected detail to name the missing hook, got %q", c.Detail)
	}
}

func TestRun_BrokenSymlinkFails(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	// Point a symlink at a nonexistent target — mirrors the left-over
	// memory-dedup.py we caught on the author's own install.
	if err := os.Symlink("/nonexistent/target.sh", filepath.Join(claude, "hooks", "stale-link.sh")); err != nil {
		t.Fatal(err)
	}
	r := Run(claude, mem, cwd)
	c := mustFindCheck(t, r, "no_broken_symlinks_hooks", FAIL)
	if !strings.Contains(c.Detail, "stale-link.sh") {
		t.Errorf("expected detail to name the broken symlink, got %q", c.Detail)
	}
}

func TestRun_WALErrorsInLast7Days(t *testing.T) {
	claude, mem, cwd := newFixture(t)
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
	r := Run(claude, mem, cwd)
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
	claude, mem, cwd := newFixture(t)
	// No .wal file — typical on a fresh install.
	r := Run(claude, mem, cwd)
	mustFindCheck(t, r, "wal_errors_last_7d", OK)
}

func TestRun_CorpusEmptyWarns(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	// No native files in project or global dir.
	r := Run(claude, mem, cwd)
	mustFindCheck(t, r, "corpus_counts", WARN)
}

func TestRun_CorpusWithFilesReportsCounts(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	proj := projDir(claude, cwd)
	glob := globalDir(claude)
	// v2 native layout: flat files with singular `type:` frontmatter, split
	// across the per-project and global stores. native.Collect merges both.
	// Native files carry NO status — pinned/stale live ONLY in the sidecar,
	// so we seed a real .sidecar.db with statuses keyed by native slug.
	writeNative(t, proj, "m-one", "mistake", "active")
	writeNative(t, proj, "m-two", "mistake", "pinned")
	writeNative(t, glob, "s-one", "strategy", "stale")
	seedSidecar(t, mem, map[string]string{
		"m-one.md": "active",
		"m-two.md": "pinned",
		"s-one.md": "stale",
	})
	r := Run(claude, mem, cwd)
	c := mustFindCheck(t, r, "corpus_counts", OK)
	for _, fragment := range []string{"3 total", "mistake:2", "strategy:1", "1 pinned", "1 stale"} {
		if !strings.Contains(c.Detail, fragment) {
			t.Errorf("expected %q in detail, got %q", fragment, c.Detail)
		}
	}
}

// TestRun_CorpusWithoutSidecarReportsZeroStatusCounts verifies the graceful
// path: native files present but no sidecar at all → pinned/stale default to
// 0 (status is sidecar-sourced), and the check still reports total/per-type
// counts as OK rather than failing.
func TestRun_CorpusWithoutSidecarReportsZeroStatusCounts(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	proj := projDir(claude, cwd)
	writeNative(t, proj, "m-one", "mistake", "active")
	writeNative(t, proj, "m-two", "mistake", "active")
	// No .sidecar.db seeded.
	r := Run(claude, mem, cwd)
	c := mustFindCheck(t, r, "corpus_counts", OK)
	for _, fragment := range []string{"2 total", "mistake:2", "0 pinned", "0 stale"} {
		if !strings.Contains(c.Detail, fragment) {
			t.Errorf("expected %q in detail, got %q", fragment, c.Detail)
		}
	}
}

// TestRun_CorpusIgnoresDeletedSidecarRows verifies that a sidecar row with
// status "deleted" (an orphan whose native file is gone) is not counted as
// live corpus, and that a native file with no matching sidecar row defaults
// to active (not pinned/stale).
func TestRun_CorpusIgnoresDeletedSidecarRows(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	proj := projDir(claude, cwd)
	writeNative(t, proj, "m-one", "mistake", "active")
	writeNative(t, proj, "m-two", "mistake", "active")
	seedSidecar(t, mem, map[string]string{
		"m-one.md":   "pinned",
		"gone.md":    "deleted", // orphan — no native file; must not be counted
		"orphan2.md": "stale",   // also no native file; must not be counted
	})
	r := Run(claude, mem, cwd)
	c := mustFindCheck(t, r, "corpus_counts", OK)
	// 2 native files; m-one pinned (from sidecar), m-two defaults active.
	// Deleted/orphan sidecar rows contribute nothing to the live counts.
	for _, fragment := range []string{"2 total", "mistake:2", "1 pinned", "0 stale"} {
		if !strings.Contains(c.Detail, fragment) {
			t.Errorf("expected %q in detail, got %q", fragment, c.Detail)
		}
	}
}

func TestCorpusQuality_ValidFrontmatterPasses(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	proj := projDir(claude, cwd)
	glob := globalDir(claude)
	// v2 has no triggers concept — any file with a non-empty status: is fine,
	// regardless of type or presence of evidence/precision_class.
	writeNative(t, proj, "fb-plain", "feedback", "active")
	writeNative(t, proj, "kn-plain", "knowledge", "active")
	writeNative(t, glob, "mi-pinned", "mistake", "pinned")
	r := Run(claude, mem, cwd)
	c := mustFindCheck(t, r, "corpus_frontmatter_quality", OK)
	if !strings.Contains(c.Detail, "all files have valid frontmatter") {
		t.Errorf("expected clean detail, got %q", c.Detail)
	}
}

func TestCorpusQuality_EmptyStatusWarns(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	proj := projDir(claude, cwd)
	// status: present but empty — the ranking filter would silently drop it.
	body := "---\ntype: feedback\nstatus:\n---\nBody with a present-but-empty status.\n"
	if err := os.WriteFile(filepath.Join(proj, "empty-status.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Run(claude, mem, cwd)
	c := mustFindCheck(t, r, "corpus_frontmatter_quality", WARN)
	if !strings.Contains(c.Detail, "1 with empty status:") {
		t.Errorf("expected empty-status warning, got %q", c.Detail)
	}
}

func TestSidecar_AbsentOnFreshInstallIsOK(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	// No .sidecar.db and no .wal — a brand-new install has nothing to rebuild.
	r := Run(claude, mem, cwd)
	mustFindCheck(t, r, "sidecar", OK)
}

func TestSidecar_AbsentWithWALWarns(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	// A WAL exists but the sidecar does not — it needs rebuilding.
	mustWriteWAL(t, mem, []string{
		time.Now().Format("2006-01-02") + "|inject|slug-a|s1",
	})
	r := Run(claude, mem, cwd)
	c := mustFindCheck(t, r, "sidecar", WARN)
	if !strings.Contains(c.Detail, "rebuild") {
		t.Errorf("expected rebuild hint, got %q", c.Detail)
	}
}

func TestSidecar_StaleVsWALWarns(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	// Write the sidecar first, then the WAL — so the WAL is newer (mtime).
	sidecar := filepath.Join(mem, ".sidecar.db")
	if err := os.WriteFile(sidecar, []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(sidecar, old, old); err != nil {
		t.Fatal(err)
	}
	mustWriteWAL(t, mem, []string{
		time.Now().Format("2006-01-02") + "|inject|slug-a|s1",
	})
	r := Run(claude, mem, cwd)
	c := mustFindCheck(t, r, "sidecar", WARN)
	if !strings.Contains(c.Detail, "stale") {
		t.Errorf("expected stale-vs-WAL warning, got %q", c.Detail)
	}
}

func TestSidecar_FreshIsOK(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	// WAL first, then the sidecar — sidecar is newer, so it is up to date.
	mustWriteWAL(t, mem, []string{
		time.Now().Format("2006-01-02") + "|inject|slug-a|s1",
	})
	sidecar := filepath.Join(mem, ".sidecar.db")
	if err := os.WriteFile(sidecar, []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Hour)
	if err := os.Chtimes(sidecar, now, now); err != nil {
		t.Fatal(err)
	}
	r := Run(claude, mem, cwd)
	mustFindCheck(t, r, "sidecar", OK)
}

func TestOpenQuanta_AllClosedIsOK(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	today := time.Now().Format("2006-01-02")
	// 3 sessions: each with an inject + a closing event.
	lines := []string{
		today + "|inject|slug-a|s1",
		today + "|trigger-useful|slug-a|s1",
		today + "|inject|slug-b|s2",
		today + "|trigger-silent|slug-b|s2",
		today + "|inject|slug-c|s3",
		today + "|clean-session|_global_|s3",
	}
	mustWriteWAL(t, mem, lines)
	r := Run(claude, mem, cwd)
	c := mustFindCheck(t, r, "open_quanta_last_30d", OK)
	if !strings.Contains(c.Detail, "0/3") {
		t.Errorf("expected 0/3 open, got %q", c.Detail)
	}
}

func TestOpenQuanta_MajorityOpenWarns(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	today := time.Now().Format("2006-01-02")
	// 4 sessions: only one has a closing event → 75% open.
	lines := []string{
		today + "|inject|slug-a|s1",
		today + "|trigger-useful|slug-a|s1",
		// Three sessions with injects only.
		today + "|inject|slug-b|s2",
		today + "|inject|slug-c|s3",
		today + "|inject|slug-d|s4",
	}
	mustWriteWAL(t, mem, lines)
	r := Run(claude, mem, cwd)
	c := mustFindCheck(t, r, "open_quanta_last_30d", WARN)
	if !strings.Contains(c.Detail, "3/4") {
		t.Errorf("expected 3/4 open, got %q", c.Detail)
	}
	if !strings.Contains(c.Detail, "measurement-bias.md") {
		t.Errorf("WARN detail should point the operator at the doc, got %q", c.Detail)
	}
}

func TestOpenQuanta_ExcludesOldEntriesOutsideWindow(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	old := time.Now().AddDate(0, 0, -60).Format("2006-01-02")
	lines := []string{
		// Old inject without closing — MUST be excluded from the 30d window.
		old + "|inject|slug-old|s-old",
	}
	mustWriteWAL(t, mem, lines)
	r := Run(claude, mem, cwd)
	// Empty-window path returns OK with a clear message; no WARN.
	c := mustFindCheck(t, r, "open_quanta_last_30d", OK)
	if !strings.Contains(c.Detail, "no inject-carrying sessions") {
		t.Errorf("expected empty-window message, got %q", c.Detail)
	}
}

// mustWriteWAL is a tiny fixture helper so test cases stay readable.
func mustWriteWAL(t *testing.T, mem string, lines []string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(mem, ".wal"),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRequiredHookCommands_MatchInstallScript guards against the
// tautological drift the doctor suite suffered pre-P1.4. Both the
// clean fixture and the missing-hook test iterate requiredHookCommands
// to build their input, so a hook added in install.sh without a
// matching entry in the constant left every existing assertion green.
// This test reads install.sh directly, greps the suffix of every
// register_hook invocation, and compares the set — a real drift
// between install.sh and the doctor's view of "what should be wired
// up" fails the test instead of surfacing only at runtime.
func TestRequiredHookCommands_MatchInstallScript(t *testing.T) {
	// Walk upward from the test file location to the repo root
	// (install.sh lives there). Tests run with CWD = package dir.
	installPath := ""
	for _, candidate := range []string{
		"../../install.sh",
		"../install.sh",
		"install.sh",
	} {
		if _, err := os.Stat(candidate); err == nil {
			installPath = candidate
			break
		}
	}
	if installPath == "" {
		t.Skip("install.sh not found from test CWD — skipping drift check")
	}

	data, err := os.ReadFile(installPath)
	if err != nil {
		t.Fatalf("read %s: %v", installPath, err)
	}
	// Match every register_hook line that references the v2 shim directory.
	// install.sh uses $CLAUDE_DIR/hooks/v2/<name>.sh in register_hook calls
	// ($CLAUDE_DIR defaults to ~/.claude; the v2.5.1 review O1 fix moved these
	// off a hardcoded $HOME).
	re := regexp.MustCompile(`(?:\$CLAUDE_DIR|~|\$HOME/\.claude)/hooks/v2/([a-z0-9-]+\.sh)`)
	matches := re.FindAllStringSubmatch(string(data), -1)

	fromInstall := map[string]struct{}{}
	for _, m := range matches {
		fromInstall[m[1]] = struct{}{}
	}

	fromDoctor := map[string]struct{}{}
	for _, cmd := range requiredHookCommands {
		fromDoctor[cmd] = struct{}{}
	}

	var missing, extra []string
	for k := range fromInstall {
		if _, ok := fromDoctor[k]; !ok {
			missing = append(missing, k)
		}
	}
	for k := range fromDoctor {
		if _, ok := fromInstall[k]; !ok {
			extra = append(extra, k)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		sort.Strings(missing)
		sort.Strings(extra)
		t.Errorf("requiredHookCommands drifted from install.sh:\n  in install.sh but NOT in requiredHookCommands: %v\n  in requiredHookCommands but NOT in install.sh: %v",
			missing, extra)
	}
}

func TestReport_PrintJSONRoundtrips(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	r := Run(claude, mem, cwd)
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

func TestRun_MissingShimFilesFail(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	if err := os.RemoveAll(filepath.Join(claude, "hooks", "v2")); err != nil {
		t.Fatal(err)
	}
	// Regression for the review's headline false-OK: settings.json still
	// lists every hook, but the shim files themselves are gone.
	mustFindCheck(t, Run(claude, mem, cwd), "shim_files_present", FAIL)
}

func TestRun_NonExecutableShimFails(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	p := filepath.Join(claude, "hooks", "v2", "session-start.sh")
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	c := mustFindCheck(t, Run(claude, mem, cwd), "shim_files_present", FAIL)
	if !strings.Contains(c.Detail, "not executable") {
		t.Errorf("detail should name the non-executable shim, got %q", c.Detail)
	}
}

// TestCheckCandidates: a candidate with ≥5 silent sessions and 0 useful
// flags WARN naming the slug; a confirmed or fresh candidate does not.
func TestCheckCandidates(t *testing.T) {
	home := t.TempDir()
	claudeHome := filepath.Join(home, ".claude")
	memDir := filepath.Join(claudeHome, "memory")
	cwd := "/tmp/proj"
	projDir := filepath.Join(claudeHome, "projects", "-tmp-proj", "memory")
	for _, d := range []string{memDir, projDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(projDir, "dud.md"),
		[]byte("---\nname: dud\ntype: mistake\nstatus: candidate\n---\nx\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "fresh.md"),
		[]byte("---\nname: fresh\ntype: mistake\nstatus: candidate\n---\nx\n"), 0o644)
	q := "-tmp-proj\x1fdud.md"
	var wal strings.Builder
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&wal, "2026-07-1%d|trigger-silent|%s|s%d\n", i, q, i)
	}
	// fresh.md: one silent session only — under the threshold.
	wal.WriteString("2026-07-11|trigger-silent|-tmp-proj\x1ffresh.md|s1\n")
	if err := os.WriteFile(filepath.Join(memDir, ".wal"), []byte(wal.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	c := checkCandidates(memDir, claudeHome, cwd)
	if c.Status != WARN || !strings.Contains(c.Detail, "dud") {
		t.Fatalf("got %+v, want WARN naming dud", c)
	}
	if strings.Contains(c.Detail, "fresh") {
		t.Fatalf("fresh candidate must not flag: %+v", c)
	}

	// A confirmation clears the flag.
	f, _ := os.OpenFile(filepath.Join(memDir, ".wal"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("2026-07-16|candidate-confirmed|" + q + "|s6\n")
	f.Close()
	if c := checkCandidates(memDir, claudeHome, cwd); c.Status != OK {
		t.Fatalf("confirmed candidate must not flag: %+v", c)
	}
}
