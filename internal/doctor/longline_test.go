package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A WAL row longer than the old 1 MiB scanner cap must not hide the rows
// after it from the WAL checks.
func TestCheckWALErrors_OversizedRowDoesNotTruncate(t *testing.T) {
	walPath := filepath.Join(t.TempDir(), ".wal")
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	lines := "2026-11-19|inject|a.md|s1\n" +
		"2026-11-19|inject|big.md|" + strings.Repeat("s", 2<<20) + "\n" +
		"2026-11-19|" + walErrorEvents[0] + "|x|s2\n"
	if err := os.WriteFile(walPath, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	c := checkWALErrors(walPath, now)
	if !strings.Contains(c.Detail, walErrorEvents[0]+":1") {
		t.Fatalf("check = %+v, want the %s row after the oversized one counted", c, walErrorEvents[0])
	}
}

// parentCloseAfter must see a session-close that follows an oversized row.
func TestParentCloseAfter_OversizedRowDoesNotTruncate(t *testing.T) {
	walPath := filepath.Join(t.TempDir(), ".wal")
	lines := "2026-11-19|inject|big.md|" + strings.Repeat("s", 2<<20) + "\n" +
		"2026-11-19|session-close|x|s1\n"
	if err := os.WriteFile(walPath, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	closed, err := parentCloseAfter(walPath, 0)
	if err != nil || !closed {
		t.Fatalf("parentCloseAfter = %v, %v; want true, nil", closed, err)
	}
}

// fmStatus reads frontmatter only, but a frontmatter line over the old
// 64 KiB cap must not hide the `status:` key after it.
func TestFmStatus_LongFrontmatterLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.md")
	body := "---\ndescription: " + strings.Repeat("d", 100<<10) + "\nstatus: \n---\nbody\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if present, v := fmStatus(p); !present || v != "" {
		t.Fatalf("fmStatus = %v, %q; want true, \"\"", present, v)
	}
}

func TestFmHasNestedMetadata_LongFrontmatterLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.md")
	body := "---\ndescription: " + strings.Repeat("d", 100<<10) + "\nmetadata:\n  type: note\n---\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !fmHasNestedMetadata(p) {
		t.Fatal("fmHasNestedMetadata = false, want true")
	}
}
