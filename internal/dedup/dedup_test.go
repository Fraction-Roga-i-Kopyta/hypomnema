package dedup

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupMemoryDir creates a temp memory dir with the directory layout
// dedup.Run expects: mistakes/ populated, .runtime/ for WAL siblings.
func setupMemoryDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"mistakes", ".runtime"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// writeMistake writes a mistake markdown file with the given root-cause.
// Uses single-line quoted form which rootCauseRe matches exactly.
func writeMistake(t *testing.T, memDir, slug, rootCause string, recurrence int) string {
	t.Helper()
	path := filepath.Join(memDir, "mistakes", slug+".md")
	content := "---\ntype: mistake\nstatus: active\nroot-cause: \"" + rootCause + "\"\nrecurrence: " +
		strconvItoa(recurrence) + "\n---\n\nBody text.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// strconvItoa avoids importing strconv in the test file — keeps imports
// lean; this package already uses strconv in dedup.go.
func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func readWAL(t *testing.T, memDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(memDir, ".wal"))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(data)
}

func runDedup(t *testing.T, mem, target string, stdin io.Reader) (Decision, string, string) {
	t.Helper()
	var out bytes.Buffer
	opts := Options{
		MemoryDir: mem,
		SessionID: "test-session",
		Today:     "2026-04-22",
		Stdin:     stdin,
		Out:       &out,
	}
	decision, err := Run(target, opts)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	return decision, out.String(), readWAL(t, mem)
}

// --- Pretool mode tests (target file does not yet exist) ---

func TestPretool_AllowNoSimilarExists(t *testing.T) {
	mem := setupMemoryDir(t)
	writeMistake(t, mem, "networking-bug", "connection timeout when querying external API under load", 0)

	newContent := "---\ntype: mistake\nroot-cause: \"CSS flex container collapses on empty child elements\"\n---\n"
	decision, _, wal := runDedup(t, mem, filepath.Join(mem, "mistakes", "new-css.md"),
		strings.NewReader(newContent))

	if decision != Allow {
		t.Errorf("expected Allow, got %v", decision)
	}
	if strings.Contains(wal, "dedup-blocked") || strings.Contains(wal, "dedup-candidate") {
		t.Errorf("unexpected WAL event: %s", wal)
	}
}

func TestPretool_BlockHighSimilarity(t *testing.T) {
	mem := setupMemoryDir(t)
	rootCause := "connection timeout when querying external API under high load"
	writeMistake(t, mem, "existing-timeout", rootCause, 0)

	// Near-identical content should score ≥80.
	newContent := "---\ntype: mistake\nroot-cause: \"" + rootCause + "\"\n---\n"
	decision, msg, wal := runDedup(t, mem, filepath.Join(mem, "mistakes", "duplicate.md"),
		strings.NewReader(newContent))

	if decision != Blocked {
		t.Fatalf("expected Blocked, got %v (msg=%q, wal=%q)", decision, msg, wal)
	}
	if !strings.Contains(wal, "dedup-blocked|duplicate>existing-timeout") {
		t.Errorf("expected dedup-blocked WAL event, got: %s", wal)
	}
	if !strings.Contains(msg, "Blocked:") {
		t.Errorf("expected user-facing Blocked message, got: %q", msg)
	}
}

func TestPretool_CandidateMediumSimilarity(t *testing.T) {
	mem := setupMemoryDir(t)
	// Share some tokens but not all — target score 50-80.
	writeMistake(t, mem, "existing-auth",
		"OAuth token refresh fails silently when refresh token expires beyond the grace period", 0)
	newContent := "---\ntype: mistake\nroot-cause: \"OAuth token refresh fails silently during background job\"\n---\n"
	decision, _, wal := runDedup(t, mem, filepath.Join(mem, "mistakes", "related-oauth.md"),
		strings.NewReader(newContent))

	if decision != Allow {
		t.Errorf("expected Allow (candidate), got %v", decision)
	}
	// Candidate event is optional — only fires in the 50-80 band. Accept
	// either no event or the candidate event; reject blocked/merged.
	if strings.Contains(wal, "dedup-blocked") || strings.Contains(wal, "dedup-merged") {
		t.Errorf("unexpected block/merge WAL event on medium similarity: %s", wal)
	}
}

func TestPretool_MissingRootCauseAllows(t *testing.T) {
	mem := setupMemoryDir(t)
	writeMistake(t, mem, "existing-a", "some specific problem description for comparison", 0)

	// Content with no root-cause line at all.
	newContent := "---\ntype: mistake\nstatus: active\n---\n\nBody without root-cause.\n"
	decision, _, wal := runDedup(t, mem, filepath.Join(mem, "mistakes", "bodyless.md"),
		strings.NewReader(newContent))

	if decision != Allow {
		t.Errorf("expected Allow on missing root-cause, got %v", decision)
	}
	if wal != "" {
		t.Errorf("expected no WAL on missing root-cause, got: %s", wal)
	}
}

func TestPretool_BlockScalarMarkerAllows(t *testing.T) {
	mem := setupMemoryDir(t)
	writeMistake(t, mem, "existing-b", "some specific problem description for comparison", 0)

	// YAML block-scalar marker: value is literal `|` (too short for
	// MinRootCauseLen). resolveRootCause returns "" in this case.
	newContent := "---\ntype: mistake\nroot-cause: |\n  the real content goes here on indented lines\n---\n"
	decision, _, _ := runDedup(t, mem, filepath.Join(mem, "mistakes", "scalar.md"),
		strings.NewReader(newContent))
	if decision != Allow {
		t.Errorf("expected Allow on block-scalar marker, got %v", decision)
	}
}

func TestPretool_EmptyMistakesDirAllows(t *testing.T) {
	mem := setupMemoryDir(t) // mistakes/ exists but empty

	newContent := "---\ntype: mistake\nroot-cause: \"perfectly reasonable description with enough characters\"\n---\n"
	decision, _, wal := runDedup(t, mem, filepath.Join(mem, "mistakes", "first.md"),
		strings.NewReader(newContent))
	if decision != Allow {
		t.Errorf("expected Allow against empty mistakes/, got %v", decision)
	}
	if wal != "" {
		t.Errorf("expected no WAL against empty mistakes/, got: %s", wal)
	}
}

// --- Posttool mode tests (target file exists on disk) ---

func TestPosttool_MergeOnHighSimilarity(t *testing.T) {
	mem := setupMemoryDir(t)
	rootCause := "database connection pool exhausted during peak traffic events"
	existingPath := writeMistake(t, mem, "pool-exhaustion", rootCause, 2)

	// Create the "new" file on disk with identical root-cause — this is the
	// posttool path (file was just written by the Write tool).
	newPath := writeMistake(t, mem, "new-pool-bug", rootCause, 0)

	// Stdin is ignored in posttool mode; pass nil for clarity.
	decision, msg, wal := runDedup(t, mem, newPath, nil)

	if decision != Merged {
		t.Fatalf("expected Merged, got %v (msg=%q, wal=%q)", decision, msg, wal)
	}
	if !strings.Contains(wal, "dedup-merged|new-pool-bug>pool-exhaustion") {
		t.Errorf("expected dedup-merged WAL event, got: %s", wal)
	}
	// New file should be gone.
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Errorf("new file should be deleted after merge, stat err: %v", err)
	}
	// Existing file should have recurrence bumped from 2 to 3.
	existingContent, err := os.ReadFile(existingPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(existingContent), "recurrence: 3") {
		t.Errorf("expected recurrence bumped to 3, existing content:\n%s", existingContent)
	}
}

// TestPosttool_MergeFailsReturnsAllow verifies the v0.10.2 fix: if the
// on-disk side of a posttool merge fails (e.g., the existing mistake file
// is read-only), Run must return Allow and skip the WAL event rather than
// writing `dedup-merged` for a merge that didn't actually happen.
func TestPosttool_MergeFailsReturnsAllow(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses read-only permissions; test is not meaningful")
	}
	mem := setupMemoryDir(t)
	rootCause := "shared memory leak in worker subprocess after hot reload"
	existingPath := writeMistake(t, mem, "worker-leak", rootCause, 1)

	// Make the existing file read-only so incrementRecurrence's WriteFile fails.
	if err := os.Chmod(existingPath, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(existingPath, 0o644) })

	newPath := writeMistake(t, mem, "dup-worker", rootCause, 0)
	decision, _, wal := runDedup(t, mem, newPath, nil)

	if decision != Allow {
		t.Errorf("expected Allow when merge fails, got %v", decision)
	}
	if strings.Contains(wal, "dedup-merged") {
		t.Errorf("expected NO dedup-merged WAL on failed merge, got: %s", wal)
	}
	// New file should still exist (the merge did not happen).
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("new file should still exist after failed merge, err: %v", err)
	}
}

func TestPosttool_MissingRootCauseAllows(t *testing.T) {
	mem := setupMemoryDir(t)
	// Write a file without a root-cause field.
	path := filepath.Join(mem, "mistakes", "orphan.md")
	content := "---\ntype: mistake\nstatus: active\n---\n\nJust a body.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	decision, _, wal := runDedup(t, mem, path, nil)
	if decision != Allow {
		t.Errorf("expected Allow on missing root-cause, got %v", decision)
	}
	if wal != "" {
		t.Errorf("expected no WAL events, got: %s", wal)
	}
}

// --- Graceful-degradation tests ---

func TestRun_MissingMemoryDirReturnsError(t *testing.T) {
	_, err := Run("/tmp/whatever.md", Options{
		MemoryDir: "", // empty — contract error
		Stdin:     strings.NewReader(""),
	})
	if err == nil {
		t.Error("expected error for missing MemoryDir")
	}
}

func TestRun_MissingMistakesDirAllows(t *testing.T) {
	// MemoryDir exists but has no mistakes/ subdir.
	mem := t.TempDir()
	newContent := "---\nroot-cause: \"some perfectly valid root cause description here\"\n---\n"
	decision, _, wal := runDedup(t, mem, filepath.Join(mem, "mistakes", "new.md"),
		strings.NewReader(newContent))
	if decision != Allow {
		t.Errorf("expected Allow, got %v", decision)
	}
	if wal != "" {
		t.Errorf("expected no WAL, got: %s", wal)
	}
}
