package doctor

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

// checkSidecar reports on the SQLite metadata sidecar (.sidecar.db). The
// sidecar is rebuildable from the WAL + native frontmatter:
//   - absent, no .wal      → OK (fresh install, nothing to rebuild yet)
//   - absent, .wal present → WARN (run `memoryctl sidecar rebuild`)
//   - no wal_offset in meta (pre-v2.16 sidecar) → WARN (rebuild)
//   - sidecar cannot be opened → WARN (unreadable)
//   - wal_offset beyond the WAL's size or not on a line boundary
//     (compacted/rotated/regrown) → WARN (rebuild)
//   - a PARENT session-close after wal_offset → WARN (a Stop ran without
//     reprojecting)
//   - otherwise OK. Injects, recalls and subagent closes land between Stops
//     by design and are projected at the next parent close.
func checkSidecar(memoryDir string, now time.Time) Check {
	const name = "sidecar"
	sidecarPath := filepath.Join(memoryDir, ".sidecar.db")
	walPath := filepath.Join(memoryDir, ".wal")

	walInfo, walErr := os.Stat(walPath)
	sidecarInfo, sidecarErr := os.Stat(sidecarPath)

	if sidecarErr != nil {
		if walErr != nil {
			return Check{
				Name:   name,
				Status: OK,
				Detail: "no .sidecar.db yet (fresh install — nothing to rebuild)",
			}
		}
		return Check{
			Name:   name,
			Status: WARN,
			Detail: ".sidecar.db missing but .wal exists — run `memoryctl sidecar rebuild`",
		}
	}
	if walErr != nil {
		return Check{Name: name, Status: OK,
			Detail: fmt.Sprintf(".sidecar.db present (mtime %s), no .wal yet", sidecarInfo.ModTime().Format("2006-01-02 15:04"))}
	}

	off, ok, err := sidecarWALOffset(sidecarPath)
	if err != nil {
		return Check{Name: name, Status: WARN,
			Detail: "sidecar unreadable (" + err.Error() + ") — if it persists, remove .sidecar.db, .sidecar.db-wal and .sidecar.db-shm in the memory dir, then run `memoryctl sidecar rebuild` (the next inject also self-heals a corrupt sidecar)"}
	}
	if !ok {
		return Check{Name: name, Status: WARN,
			Detail: "sidecar carries no WAL offset (written before v2.16) — run `memoryctl sidecar rebuild`"}
	}
	if off > walInfo.Size() || !atLineStart(walPath, off) {
		return Check{Name: name, Status: WARN,
			Detail: "WAL no longer matches the sidecar's projection offset (compacted or rotated) — run `memoryctl sidecar rebuild`"}
	}
	closed, scanErr := parentCloseAfter(walPath, off)
	if scanErr != nil {
		return Check{Name: name, Status: WARN,
			Detail: "cannot scan WAL past the projection offset (" + scanErr.Error() + ") — run `memoryctl sidecar rebuild`"}
	}
	if closed {
		return Check{Name: name, Status: WARN,
			Detail: "sidecar stale vs WAL: a session closed after the last projection — run `memoryctl sidecar rebuild`"}
	}
	return Check{
		Name:   name,
		Status: OK,
		Detail: fmt.Sprintf(".sidecar.db present (mtime %s), projected through WAL byte %d", sidecarInfo.ModTime().Format("2006-01-02 15:04"), off),
	}
}

// sidecarWALOffset reads meta.wal_offset. The caller has already confirmed
// the file exists, so sidecar.Open does not create one (same contract as
// sidecarStatusMap). err is non-nil when the DB cannot be opened or queried.
func sidecarWALOffset(dbPath string) (int64, bool, error) {
	s, err := sidecar.Open(dbPath)
	if err != nil {
		return 0, false, err
	}
	defer s.Close()
	return s.WALOffset()
}

// atLineStart reports whether off falls on a line boundary of the WAL
// (offset 0, or the byte before it is '\n'). A WAL truncated by hand and
// regrown past the old offset fails this and is treated as rotated.
func atLineStart(walPath string, off int64) bool {
	if off == 0 {
		return true
	}
	f, err := os.Open(walPath)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 1)
	if _, err := f.ReadAt(b, off-1); err != nil {
		return false
	}
	return b[0] == '\n'
}

// parentCloseAfter reports whether the WAL holds a parent session-close
// (session column without ':') at or after byte offset off. Any open, seek or
// scan error (including a line over the 1 MB scanner buffer) is returned, so
// the caller can fail toward WARN rather than report a false "no close".
func parentCloseAfter(walPath string, off int64) (bool, error) {
	f, err := os.Open(walPath)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return false, err
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "|", 4)
		if len(parts) == 4 && parts[1] == "session-close" && !strings.Contains(parts[3], ":") {
			return true, nil
		}
	}
	return false, sc.Err()
}

// checkStoreResolution reports the native store this invocation resolved
// (harness rule: override → canonical git root → sanitized slug) and warns
// when a store named by the pre-v2.13 formula (only "/" → "-", no canonical
// root) still holds facts that are no longer read — paths with dots,
// underscores, spaces or non-ASCII, worktrees, and repo subdirectories.
func checkStoreResolution(claudeDir string, st native.Store, anchor string) Check {
	const name = "store_resolution"
	detail := fmt.Sprintf("%s (project %s, via %s; anchor %s)", st.Dir, st.Project, st.Source, anchor)
	if st.Source != "default" {
		// An override store has no slug, hence no legacy-slug sibling.
		return Check{Name: name, Status: OK, Detail: detail}
	}
	// <config dir>/projects — taken from the resolved store, not claudeDir,
	// so CLAUDE_CONFIG_DIR installs are checked in the right tree.
	projectsRoot := filepath.Dir(filepath.Dir(st.Dir))
	seen := map[string]bool{st.Project: true}
	var stray []string
	for _, p := range []string{anchor, st.Root} {
		if p == "" {
			continue
		}
		legacy := strings.ReplaceAll(filepath.Clean(p), "/", "-")
		if seen[legacy] {
			continue
		}
		seen[legacy] = true
		dir := filepath.Join(projectsRoot, legacy, "memory")
		if files, err := native.List(dir); err == nil && len(files) > 0 {
			stray = append(stray, fmt.Sprintf("%s (%d facts)", dir, len(files)))
		}
	}
	if len(stray) == 0 {
		return Check{Name: name, Status: OK, Detail: detail}
	}
	return Check{Name: name, Status: WARN, Detail: detail +
		"; legacy store(s) no longer read since v2.13: " + strings.Join(stray, ", ") +
		" — move their facts (not MEMORY.md) into " + st.Dir + " (see TROUBLESHOOTING)"}
}
