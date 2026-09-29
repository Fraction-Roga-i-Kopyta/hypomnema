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

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

// fixtureCWD is the working directory every fixture pretends the project
// lives in. native.StoreFor (via SanitizePath) encodes it into the
// per-project slug ("/" → "-"), so the project memory dir is
// <home>/.claude/projects/-work-proj/memory; native.Collect only enumerates
// whatever store it is handed, it does not derive the slug itself.
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
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
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
	r := Run(claude, filepath.Join(claude, "memory"), native.StoreFor(claude, fixtureCWD), fixtureCWD)
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
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
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
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
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
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
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
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
	mustFindCheck(t, r, "wal_errors_last_7d", OK)
}

func TestRun_CorpusEmptyWarns(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	// No native files in project or global dir.
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
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
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
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
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
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
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
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
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
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
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
	c := mustFindCheck(t, r, "corpus_frontmatter_quality", WARN)
	if !strings.Contains(c.Detail, "1 with empty status:") {
		t.Errorf("expected empty-status warning, got %q", c.Detail)
	}
}

func TestSidecar_AbsentOnFreshInstallIsOK(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	// No .sidecar.db and no .wal — a brand-new install has nothing to rebuild.
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
	mustFindCheck(t, r, "sidecar", OK)
}

func TestSidecar_AbsentWithWALWarns(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	// A WAL exists but the sidecar does not — it needs rebuilding.
	mustWriteWAL(t, mem, []string{
		time.Now().Format("2006-01-02") + "|inject|slug-a|s1",
	})
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
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
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
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
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
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
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
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
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
	c := mustFindCheck(t, r, "open_quanta_last_30d", WARN)
	if !strings.Contains(c.Detail, "3/4") {
		t.Errorf("expected 3/4 open, got %q", c.Detail)
	}
	if !strings.Contains(c.Detail, "measurement-bias.md") {
		t.Errorf("WARN detail should point the operator at the doc, got %q", c.Detail)
	}
}

// TestOpenQuanta_CiteEventsCloseTheSession verifies the v2.14+ closing
// signals: a session with only inject + cite-useful (or cite-silent) is
// closed, and — the explicit regression case — a session with only
// inject + cite-none (the "readable transcript, no resolvable citation"
// marker) is also closed, not counted as an open quantum.
func TestOpenQuanta_CiteEventsCloseTheSession(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	today := time.Now().Format("2006-01-02")
	lines := []string{
		today + "|inject|slug-a|s1",
		today + "|cite-useful|slug-a|s1",
		today + "|inject|slug-b|s2",
		today + "|cite-silent|slug-b|s2",
		// cite-none: target and session are both the session id (close's
		// format for a readable, citation-less session).
		today + "|inject|slug-c|s3",
		today + "|cite-none|s3|s3",
	}
	mustWriteWAL(t, mem, lines)
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
	c := mustFindCheck(t, r, "open_quanta_last_30d", OK)
	if !strings.Contains(c.Detail, "0/3") {
		t.Errorf("expected 0/3 open (all three sessions closed by cite-* events), got %q", c.Detail)
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
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
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
	r := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
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
	mustFindCheck(t, Run(claude, mem, native.StoreFor(claude, cwd), cwd), "shim_files_present", FAIL)
}

// memoryctlStub writes an executable shell stub over the fixture's
// bin/memoryctl. --help always succeeds; `close --subagent` exits 2 when
// subagentExit2 is true (simulating a binary built before subagent support
// landed) and 0 otherwise.
func memoryctlStub(t *testing.T, claude string, subagentExit2 bool) {
	t.Helper()
	closeExit := "0"
	if subagentExit2 {
		closeExit = "2"
	}
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--help\" ]; then exit 0; fi\n" +
		"if [ \"$1\" = \"close\" ] && [ \"$2\" = \"--subagent\" ]; then exit " + closeExit + "; fi\n" +
		"exit 0\n"
	p := filepath.Join(claude, "bin", "memoryctl")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestCheckMemoryctl_PredatesSubagentSupportWarns(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	memoryctlStub(t, claude, true)
	c := mustFindCheck(t, Run(claude, mem, native.StoreFor(claude, cwd), cwd), "memoryctl_available", WARN)
	if !strings.Contains(c.Detail, "predates subagent support") {
		t.Errorf("expected %q to contain \"predates subagent support\"", c.Detail)
	}
}

// A build that knows `close --subagent` but not `inject --subagent` still
// leaves SubagentStart doing nothing behind the shims — doctor must say so.
func TestCheckMemoryctl_InjectSubagentRefusedWarns(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"inject\" ] && [ \"$2\" = \"--subagent\" ]; then exit 2; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(claude, "bin", "memoryctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := mustFindCheck(t, Run(claude, mem, native.StoreFor(claude, cwd), cwd), "memoryctl_available", WARN)
	if !strings.Contains(c.Detail, "inject --subagent") {
		t.Errorf("expected the detail to name `inject --subagent`, got %q", c.Detail)
	}
}

func TestCheckMemoryctl_SubagentSupportedNoPredatesWarning(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	memoryctlStub(t, claude, false)
	report := Run(claude, mem, native.StoreFor(claude, cwd), cwd)
	for _, c := range report.Checks {
		if c.Name != "memoryctl_available" {
			continue
		}
		if strings.Contains(c.Detail, "predates subagent support") {
			t.Errorf("did not expect \"predates subagent support\", got %q (status=%s)", c.Detail, c.Status)
		}
	}
}

func TestRun_NonExecutableShimFails(t *testing.T) {
	claude, mem, cwd := newFixture(t)
	p := filepath.Join(claude, "hooks", "v2", "session-start.sh")
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	c := mustFindCheck(t, Run(claude, mem, native.StoreFor(claude, cwd), cwd), "shim_files_present", FAIL)
	if !strings.Contains(c.Detail, "not executable") {
		t.Errorf("detail should name the non-executable shim, got %q", c.Detail)
	}
}

// TestCheckCandidates: a candidate with ≥5 silent sessions and 0 useful
// flags WARN naming the slug; a confirmed or fresh candidate does not.
// Fixtures use cite-useful/cite-silent (the v2.14+ signal) — the legacy
// trigger-useful/trigger-silent events, still present in old WAL history,
// must NOT be counted (a candidate with only trigger-silent rows must not
// flag).
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
	os.WriteFile(filepath.Join(projDir, "legacy-only.md"),
		[]byte("---\nname: legacy-only\ntype: mistake\nstatus: candidate\n---\nx\n"), 0o644)
	q := "-tmp-proj\x1fdud.md"
	var wal strings.Builder
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&wal, "2026-07-1%d|cite-silent|%s|s%d\n", i, q, i)
	}
	// fresh.md: one silent session only — under the threshold.
	wal.WriteString("2026-07-11|cite-silent|-tmp-proj\x1ffresh.md|s1\n")
	// A same-basename fact in ANOTHER project: its useful citation must not
	// mask dud's flag, and its silents must not inflate fresh's tally.
	wal.WriteString("2026-07-11|cite-useful|-other-proj\x1fdud.md|x1\n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&wal, "2026-07-1%d|cite-silent|-other-proj\x1ffresh.md|x%d\n", i, i)
	}
	// legacy-only.md: ≥5 pre-v2.14 trigger-silent rows and zero cite-*
	// rows — must NOT flag; the old signal is ignored entirely now.
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&wal, "2026-07-1%d|trigger-silent|-tmp-proj\x1flegacy-only.md|l%d\n", i, i)
	}
	if err := os.WriteFile(filepath.Join(memDir, ".wal"), []byte(wal.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	c := checkCandidates(memDir, claudeHome, native.StoreFor(claudeHome, cwd))
	if c.Status != WARN || !strings.Contains(c.Detail, "dud") {
		t.Fatalf("got %+v, want WARN naming dud", c)
	}
	if strings.Contains(c.Detail, "fresh") {
		t.Fatalf("fresh candidate must not flag: %+v", c)
	}
	if strings.Contains(c.Detail, "legacy-only") {
		t.Fatalf("candidate with only pre-v2.14 trigger-silent rows must not flag: %+v", c)
	}

	// A confirmation clears the flag.
	f, _ := os.OpenFile(filepath.Join(memDir, ".wal"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("2026-07-16|candidate-confirmed|" + q + "|s6\n")
	f.Close()
	if c := checkCandidates(memDir, claudeHome, native.StoreFor(claudeHome, cwd)); c.Status != OK {
		t.Fatalf("confirmed candidate must not flag: %+v", c)
	}
}

// TestCheckCandidates_CiteNoneAndUndeliveredDoNotCount: cite-none's target
// is a session id, not a fact slug, and cite-undelivered is
// diagnostic-only — neither may contribute to a candidate's
// useful/silent tally. A candidate at exactly the WARN threshold from real
// cite-silent rows must stay flagged with an unchanged count even when the
// WAL is full of cite-none/cite-undelivered noise, including a pathological
// case where a cite-none session id collides with the candidate's own bare
// slug.
func TestCheckCandidates_CiteNoneAndUndeliveredDoNotCount(t *testing.T) {
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
	q := "-tmp-proj\x1fdud.md"
	var wal strings.Builder
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&wal, "2026-09-1%d|cite-silent|%s|s%d\n", i, q, i)
	}
	// Noise: cite-none rows whose session id happens to equal the
	// candidate's own bare slug ("dud") — the pathological collision case
	// for the legacy bare-slug fallback lookup.
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&wal, "2026-09-1%d|cite-none|dud|dud\n", i)
	}
	// Noise: cite-undelivered rows for the SAME fact — must not add to
	// useful or silent, and must not confirm it.
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&wal, "2026-09-1%d|cite-undelivered|%s|x%d\n", i, q, i)
	}
	if err := os.WriteFile(filepath.Join(memDir, ".wal"), []byte(wal.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	c := checkCandidates(memDir, claudeHome, native.StoreFor(claudeHome, cwd))
	if c.Status != WARN || !strings.Contains(c.Detail, "dud") {
		t.Fatalf("got %+v, want WARN naming dud (cite-none/cite-undelivered noise must not mask a real flag)", c)
	}
	slugs, _ := c.Extra["slugs"].([]string)
	if len(slugs) != 1 || slugs[0] != "dud" {
		// Exactly the real candidate should be flagged — the noise must not
		// spawn a second phantom-keyed flag alongside it.
		t.Fatalf("want exactly [dud] flagged, got %+v (full check: %+v)", slugs, c)
	}
}

func TestCheckCorpusQuality_CountsNestedMetadata(t *testing.T) {
	home := t.TempDir()
	claudeHome := filepath.Join(home, ".claude")
	cwd := "/tmp/proj"
	projDir := filepath.Join(claudeHome, "projects", "-tmp-proj", "memory")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(projDir, "flat.md"),
		[]byte("---\nname: flat\ntype: mistake\nstatus: active\n---\nx\n"), 0o644)
	os.WriteFile(filepath.Join(projDir, "nested.md"),
		[]byte("---\nname: nested\nmetadata:\n  type: mistake\n  status: active\n---\nx\n"), 0o644)

	c := checkCorpusQuality(claudeHome, native.StoreFor(claudeHome, cwd))
	if c.Status != OK {
		t.Fatalf("nested metadata is tolerated, not a quality issue: %+v", c)
	}
	if got, _ := c.Extra["nested_metadata_count"].(int); got != 1 {
		t.Errorf("nested_metadata_count = %v, want 1", c.Extra["nested_metadata_count"])
	}
}

func TestCheckOversized(t *testing.T) {
	home := t.TempDir()
	claudeHome := filepath.Join(home, ".claude")
	cwd := "/tmp/proj"
	projDir := filepath.Join(claudeHome, "projects", "-tmp-proj", "memory")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(projDir, "small.md"),
		[]byte("---\nname: small\ntype: note\n---\nshort\n"), 0o644)
	if c := checkOversized(claudeHome, native.StoreFor(claudeHome, cwd)); c.Status != OK {
		t.Fatalf("all-small corpus must be OK: %+v", c)
	}
	os.WriteFile(filepath.Join(projDir, "huge.md"),
		[]byte("---\nname: huge\ntype: note\n---\n"+strings.Repeat("z", 3000)+"\n"), 0o644)
	c := checkOversized(claudeHome, native.StoreFor(claudeHome, cwd))
	if c.Status != WARN || !strings.Contains(c.Detail, "huge.md (3000 B)") || !strings.Contains(c.Detail, "file path") {
		t.Fatalf("want WARN naming huge.md with its size: %+v", c)
	}
	if strings.Contains(c.Detail, "small.md") {
		t.Errorf("small file must not be listed: %s", c.Detail)
	}
	if got, _ := c.Extra["oversized_count"].(int); got != 1 {
		t.Errorf("oversized_count = %v, want 1", c.Extra["oversized_count"])
	}
}

func TestCheckStoreResolution(t *testing.T) {
	claude := filepath.Join(t.TempDir(), ".claude")
	anchor := "/tmp/my_proj" // "_" → new slug "-tmp-my-proj", legacy "-tmp-my_proj"
	st := native.StoreFor(claude, anchor)

	c := checkStoreResolution(claude, st, anchor)
	if c.Status != OK || !strings.Contains(c.Detail, st.Dir) {
		t.Errorf("clean install: %+v", c)
	}
	if !strings.Contains(c.Detail, "anchor "+anchor) {
		t.Errorf("detail must name the resolved anchor (ARCHITECTURE/CHANGELOG say \"resolved store and anchor\"): %+v", c)
	}

	legacy := filepath.Join(claude, "projects", "-tmp-my_proj", "memory")
	os.MkdirAll(legacy, 0o755)
	os.WriteFile(filepath.Join(legacy, "old.md"), []byte("---\nname: old\ntype: note\n---\nx\n"), 0o644)
	c = checkStoreResolution(claude, st, anchor)
	if c.Status != WARN || !strings.Contains(c.Detail, legacy) || !strings.Contains(c.Detail, "1 facts") {
		t.Errorf("legacy store with facts must WARN naming it: %+v", c)
	}
	if !strings.Contains(c.Detail, "move their facts (not MEMORY.md) into") {
		t.Errorf("WARN text must match the TROUBLESHOOTING recipe wording: %+v", c)
	}
}

// TestCheckCitationSignal_NoCiteDataIsOK: a WAL with zero cite-useful/
// cite-silent rows anywhere (pre-v2.14 history, or a fresh install) reports
// OK naming the pre-v2.14 cutover — never a WARN, since there is nothing yet
// to judge the citation channel by.
func TestCheckCitationSignal_NoCiteDataIsOK(t *testing.T) {
	_, mem, _ := newFixture(t)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	lines := []string{
		now.Format("2006-01-02") + "|inject|slug-a|s1",
		now.Format("2006-01-02") + "|trigger-useful|slug-a|s1", // legacy signal — irrelevant here
	}
	mustWriteWAL(t, mem, lines)
	c := checkCitationSignal(filepath.Join(mem, ".wal"), now)
	if c.Status != OK || !strings.Contains(c.Detail, "no citation data yet") {
		t.Fatalf("got %+v, want OK naming 'no citation data yet'", c)
	}
}

// TestCheckCitationSignal_NeverCitedWarns: citation data exists somewhere in
// history (so the channel has fired before), but the last 7 days show ≥3
// distinct inject-carrying sessions and zero cite-useful sessions — the
// channel looks like it just went dark.
func TestCheckCitationSignal_NeverCitedWarns(t *testing.T) {
	_, mem, _ := newFixture(t)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -20).Format("2006-01-02")
	today := now.Format("2006-01-02")
	lines := []string{
		old + "|cite-useful|slug-old|s-old", // proves citation data exists at all
		today + "|inject|slug-a|s1",
		today + "|cite-none|s1|s1",
		today + "|inject|slug-b|s2",
		today + "|cite-none|s2|s2",
		today + "|inject|slug-c|s3",
		today + "|cite-none|s3|s3",
		// no cite-useful in the 7d window
	}
	mustWriteWAL(t, mem, lines)
	c := checkCitationSignal(filepath.Join(mem, ".wal"), now)
	if c.Status != WARN || !strings.Contains(c.Detail, "never cited") {
		t.Fatalf("got %+v, want WARN containing 'never cited'", c)
	}
}

// TestCheckCitationSignal_SomeCitedIsOK: 3 sessions injected within the
// window, one of them also carries a cite-useful — reports OK with the M/N
// ratio.
func TestCheckCitationSignal_SomeCitedIsOK(t *testing.T) {
	_, mem, _ := newFixture(t)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	today := now.Format("2006-01-02")
	lines := []string{
		today + "|inject|slug-a|s1",
		today + "|cite-useful|slug-a|s1",
		today + "|inject|slug-b|s2",
		today + "|cite-none|s2|s2",
		today + "|inject|slug-c|s3",
		today + "|cite-none|s3|s3",
	}
	mustWriteWAL(t, mem, lines)
	c := checkCitationSignal(filepath.Join(mem, ".wal"), now)
	if c.Status != OK || !strings.Contains(c.Detail, "1/3") {
		t.Fatalf("got %+v, want OK containing '1/3'", c)
	}
}

// TestCheckCitationSignal_MissingWALIsOK mirrors the fresh-install path the
// other WAL-reading checks use: no .wal file at all is OK, not a WARN.
func TestCheckCitationSignal_MissingWALIsOK(t *testing.T) {
	_, mem, _ := newFixture(t)
	c := checkCitationSignal(filepath.Join(mem, ".wal"), time.Now())
	if c.Status != OK || !strings.Contains(c.Detail, "no citation data yet") {
		t.Fatalf("got %+v, want OK naming 'no citation data yet'", c)
	}
}

// TestCheckCitationSignal_CiteNoneOnlyHistoryWarns: a WAL whose only cite-*
// rows are cite-none (close ran and reached a classification pass, but no
// citation ever resolved as delivered) must NOT read as "no citation data
// yet" — that reading previously let 3 inject+cite-none sessions in the
// window report OK, hiding the exact channel-went-dark case this check
// exists to catch.
func TestCheckCitationSignal_CiteNoneOnlyHistoryWarns(t *testing.T) {
	_, mem, _ := newFixture(t)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	today := now.Format("2006-01-02")
	lines := []string{
		today + "|inject|slug-a|s1",
		today + "|cite-none|s1|s1",
		today + "|inject|slug-b|s2",
		today + "|cite-none|s2|s2",
		today + "|inject|slug-c|s3",
		today + "|cite-none|s3|s3",
	}
	mustWriteWAL(t, mem, lines)
	c := checkCitationSignal(filepath.Join(mem, ".wal"), now)
	if c.Status != WARN || !strings.Contains(c.Detail, "never cited") {
		t.Fatalf("got %+v, want WARN containing 'never cited' (cite-none-only history must still count as history)", c)
	}
}

// TestCheckCitationSignal_RecallOnlyCiteUsefulDoesNotInflateM: a session
// that never injects anything but still resolves a delivered citation (e.g.
// pull-only via recall) must not count toward M — M is the intersection of
// inject-carrying sessions and cite-useful sessions, not a raw cite-useful
// tally, or a healthy-looking M/N ratio could mask an inject path whose
// citations never land.
func TestCheckCitationSignal_RecallOnlyCiteUsefulDoesNotInflateM(t *testing.T) {
	_, mem, _ := newFixture(t)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	today := now.Format("2006-01-02")
	lines := []string{
		today + "|inject|slug-a|s1",
		today + "|cite-none|s1|s1",
		today + "|inject|slug-b|s2",
		today + "|cite-none|s2|s2",
		today + "|inject|slug-c|s3",
		today + "|cite-none|s3|s3",
		today + "|cite-useful|slug-d|s4", // recall-only session — no inject row for s4
	}
	mustWriteWAL(t, mem, lines)
	c := checkCitationSignal(filepath.Join(mem, ".wal"), now)
	if c.Status != WARN || !strings.Contains(c.Detail, "never cited") {
		t.Fatalf("got %+v, want WARN — s4's cite-useful must not count toward M (no inject in s4 this window)", c)
	}
}

// TestCheckCitationSignal_PreCutoverSessionsNotCounted: right after an
// upgrade the window still holds sessions closed by a pre-citation close —
// they injected facts but carry no cite-* row, because the model was never
// told to cite. Only sessions a citation-aware close classified count
// toward N, so one uncited post-upgrade session must not raise a WARN on
// the strength of the pre-upgrade ones.
func TestCheckCitationSignal_PreCutoverSessionsNotCounted(t *testing.T) {
	_, mem, _ := newFixture(t)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	today := now.Format("2006-01-02")
	lines := []string{
		yesterday + "|inject|slug-a|old1",
		yesterday + "|trigger-silent|slug-a|old1",
		yesterday + "|inject|slug-b|old2",
		yesterday + "|inject|slug-c|old3",
		today + "|inject|slug-a|new1",
		today + "|cite-none|new1|new1",
	}
	mustWriteWAL(t, mem, lines)
	c := checkCitationSignal(filepath.Join(mem, ".wal"), now)
	if c.Status != OK || !strings.Contains(c.Detail, "0/1") {
		t.Fatalf("got %+v, want OK containing '0/1' (pre-cutover sessions excluded from N)", c)
	}
}

func TestCheckCandidates_SubagentKeysCountAsOneSession(t *testing.T) {
	home := t.TempDir()
	claudeHome := filepath.Join(home, ".claude")
	memDir := filepath.Join(claudeHome, "memory")
	projDir := filepath.Join(claudeHome, "projects", "-tmp-proj", "memory")
	for _, d := range []string{memDir, projDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(projDir, "dud.md"),
		[]byte("---\nname: dud\ntype: mistake\nstatus: candidate\n---\nx\n"), 0o644)
	var wal strings.Builder
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&wal, "2026-07-11|cite-silent|-tmp-proj\x1fdud.md|s1:a%d\n", i)
	}
	os.WriteFile(filepath.Join(memDir, ".wal"), []byte(wal.String()), 0o644)
	if c := checkCandidates(memDir, claudeHome, native.StoreFor(claudeHome, "/tmp/proj")); c.Status != OK {
		t.Fatalf("five silent subagents of one session are one silent session: %+v", c)
	}
}
