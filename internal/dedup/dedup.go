// Package dedup reimplements bin/memory-dedup.py's pretool/posttool
// orchestration in Go. It consumes the candidate file path (plus stdin
// content for pretool invocations), scans the native mistake corpus, and
// returns a Decision the CLI translates into exit codes and WAL events.
//
// Identical semantics to the Python version:
//   - Pretool mode (file does not exist yet): read stdin for content,
//     parse root-cause, write `dedup-blocked` WAL event and exit 2 if
//     similarity to any existing mistake is ≥ MergeThreshold. Below that,
//     write `dedup-candidate` at CandidateThreshold, exit 0 otherwise.
//   - Posttool mode (file already on disk): read file directly. On
//     high-similarity hit, increment recurrence on the existing file,
//     delete the new file, write `dedup-merged`, exit 1.
//
// v2 change: the comparison corpus is the native memory store (mistake-typed
// files passed in Options.Files), not the retired memoryDir/mistakes/ subdir.
// Only the file SOURCE changed — the similarity logic, thresholds, exit codes,
// and WAL event shapes are identical to the v1 implementation.
package dedup

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/fuzzy"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/pathutil"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/wal"
)

// Thresholds match bin/memory-dedup.py exactly.
const (
	MergeThreshold     = 80.0
	CandidateThreshold = 50.0
	MinRootCauseLen    = 10
)

// Decision is returned by Run.
type Decision int

const (
	// Allow — no duplicate found, write proceeds normally.
	Allow Decision = 0
	// Merged — posttool only: the new file was merged into an existing
	// one (recurrence incremented, file deleted).
	Merged Decision = 1
	// Blocked — pretool only: the proposed write would create a high-
	// similarity duplicate. Shell wrapper converts this to exit 2.
	Blocked Decision = 2
)

// Options controls Run behaviour.
type Options struct {
	// MemoryDir is the metadata root (.wal, .sidecar.db) — default
	// ~/.claude/memory via CLI. In v2 it no longer holds mistake content.
	MemoryDir string
	// Files is the native memory corpus to compare against (merged per-project
	// + global, as native.Collect returns). Run filters it to mistake-typed
	// files. The native mistake files are the dedup comparison source — there
	// is no MemoryDir/mistakes/ subdir in v2.
	Files []native.MemFile
	// SessionID is passed through to WAL entries for traceability.
	SessionID string
	// Stdin is the content source when the target file does not yet
	// exist (pretool invocation). Pass os.Stdin from CLI.
	Stdin io.Reader
	// Today overrides the WAL date stamp (YYYY-MM-DD). Used in tests.
	Today string
	// Out captures the user-facing message ("Blocked: ..." / "Merged: ...").
	// Pass os.Stdout / os.Stderr from the CLI per Decision.
	Out io.Writer
}

// Run executes the dedup pipeline against the candidate file and returns
// the decision. Any error encountered (malformed frontmatter, unreadable
// existing mistake, empty root-cause) maps to Allow — the hook contract
// says dedup must never break a legitimate write.
func Run(targetPath string, opts Options) (Decision, error) {
	if opts.MemoryDir == "" {
		return Allow, fmt.Errorf("dedup.Run: MemoryDir must be set")
	}
	if opts.Today == "" {
		opts.Today = time.Now().Local().Format("2006-01-02")
	}
	if opts.SessionID == "" {
		opts.SessionID = "unknown"
	}
	if opts.Out == nil {
		opts.Out = io.Discard
	}

	// Boundary check (v2): in v1 the target had to live inside MemoryDir. v2
	// mistake content lives in the native store, not MemoryDir, so that test no
	// longer applies. The safety property it protected still does: a caller
	// must not be able to feed an arbitrary outside path and, on a 100% match,
	// have dedup delete that file (posttool merge path). We therefore require
	// the target's parent directory to be one of the directories the native
	// mistake corpus actually lives in (derived from Options.Files). A target
	// whose parent is not a known native dir is rejected with Allow (silent
	// no-op) per the package's "dedup must never break a legitimate write"
	// contract. With no native files there is nothing to compare against, so
	// the scan below short-circuits to Allow anyway. External review
	// 2026-05-08 finding E3 (security property preserved, source re-pointed).
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return Allow, nil
	}
	if !targetInNativeDirs(absTarget, opts.Files) {
		return Allow, nil
	}

	pretool := !fileExists(targetPath)

	newRC, err := resolveRootCause(targetPath, pretool, opts.Stdin)
	if err != nil || newRC == "" {
		return Allow, nil
	}

	bestScore, bestFile, err := scanMistakes(opts.Files, targetPath, newRC)
	if err != nil {
		return Allow, nil
	}

	if bestFile == "" {
		return Allow, nil
	}

	newName := slugFromPath(targetPath)
	existingName := slugFromPath(bestFile)

	switch {
	case bestScore >= MergeThreshold:
		if pretool {
			line := fmt.Sprintf("%s|dedup-blocked|%s>%s|%s",
				opts.Today, newName, existingName, opts.SessionID)
			key := fmt.Sprintf("dedup-blocked|%s>%s|%s",
				newName, existingName, opts.SessionID)
			wal.Append(opts.MemoryDir, line, key)
			fmt.Fprintf(opts.Out, "Blocked: %s is similar to existing %s (%.0f%%)\n",
				newName, existingName, bestScore)
			return Blocked, nil
		}
		// posttool — merge path. Only emit the WAL event and user-facing
		// message if the on-disk mutation actually succeeded; otherwise a
		// failed recurrence bump produces a "merged" WAL entry for a
		// non-event. Allow falls through with the new file still on disk.
		//
		// Serialize the read-modify-write on the existing mistake file under
		// the WAL lock: two concurrent Run()s (e.g. `memoryctl dedup` in a
		// shell alongside an agent's PostToolUse) merging into the same
		// existing file can each read recurrence=N and each write N+1 —
		// losing one increment. Matching `wal.Append`'s permissive policy,
		// a lock-acquire timeout falls through to an unlocked attempt
		// rather than dropping the merge entirely.
		lock, _ := wal.Acquire(opts.MemoryDir, wal.DefaultLockConfig)
		if err := incrementRecurrence(bestFile); err != nil {
			lock.Release()
			return Allow, nil
		}
		_ = os.Remove(targetPath)
		lock.Release()
		line := fmt.Sprintf("%s|dedup-merged|%s>%s|%s",
			opts.Today, newName, existingName, opts.SessionID)
		key := fmt.Sprintf("dedup-merged|%s>%s|%s",
			newName, existingName, opts.SessionID)
		wal.Append(opts.MemoryDir, line, key)
		fmt.Fprintf(opts.Out, "Merged: %s -> %s (similarity %.0f%%)\n",
			newName, existingName, bestScore)
		return Merged, nil

	case bestScore >= CandidateThreshold:
		line := fmt.Sprintf("%s|dedup-candidate|%s~%s|%s",
			opts.Today, newName, existingName, opts.SessionID)
		key := fmt.Sprintf("dedup-candidate|%s~%s|%s",
			newName, existingName, opts.SessionID)
		wal.Append(opts.MemoryDir, line, key)
		fmt.Fprintf(opts.Out, "Possible duplicate: %s ~ %s (%.0f%%)\n",
			newName, existingName, bestScore)
	}

	return Allow, nil
}

// rootCauseRe captures the scalar value after `root-cause:`, with an optional
// wrapping quote pair. Multi-line block scalars (|, >) produce a single-char
// match and are filtered by the caller (too short to clear MinRootCauseLen).
var rootCauseRe = regexp.MustCompile(`(?m)^root-cause:\s*["']?(.+?)["']?\s*$`)

// resolveRootCause extracts the root-cause value from either the file on
// disk (posttool) or stdin (pretool). Returns "" when the file has no
// root-cause or the value is a YAML block-scalar marker (unsupported).
func resolveRootCause(path string, pretool bool, stdin io.Reader) (string, error) {
	var data []byte
	var err error
	if pretool {
		if stdin == nil {
			return "", nil
		}
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return "", err
	}

	m := rootCauseRe.FindStringSubmatch(string(data))
	if len(m) < 2 {
		return "", nil
	}
	val := strings.TrimSpace(m[1])
	if val == "|" || val == ">" || val == "" {
		return "", nil
	}
	if len([]rune(val)) < MinRootCauseLen {
		return "", nil
	}
	return val, nil
}

// targetInNativeDirs reports whether absTarget sits directly inside one of the
// directories the native mistake corpus lives in (the parent dir of any file in
// files). This is the v2 boundary guard: it admits the legitimate native memory
// directories (per-project + global) without hard-coding their paths, and
// rejects an arbitrary outside path the same way the old MemoryDir-prefix check
// did. Returns false when files is empty (nothing to compare against anyway).
func targetInNativeDirs(absTarget string, files []native.MemFile) bool {
	targetDir := filepath.Dir(absTarget)
	for _, f := range files {
		absP, err := filepath.Abs(f.Path)
		if err != nil {
			continue
		}
		if filepath.Dir(absP) == targetDir {
			return true
		}
	}
	return false
}

// scanMistakes iterates the native corpus (filtered to mistake-typed files),
// extracts each file's root-cause line, and returns the highest-scoring
// existing file. Ties broken by first-seen order over files (callers pass a
// stable, slug-ordered slice). The target itself is skipped by absolute path
// (posttool: the new file is already on disk and may be in the corpus). Files
// that can't be read or don't have a root-cause are skipped silently (same
// policy as the Python implementation). The similarity logic and thresholds
// are unchanged — only the file source moved from a dir read to Options.Files.
func scanMistakes(files []native.MemFile, targetPath, newRC string) (float64, string, error) {
	bestScore := 0.0
	bestFile := ""

	absTarget, _ := filepath.Abs(targetPath)

	for _, f := range files {
		if f.Type != "mistake" {
			continue
		}
		absP, _ := filepath.Abs(f.Path)
		if absP == absTarget {
			continue
		}

		existingRC, err := readRootCauseFromFile(f.Path)
		if err != nil || existingRC == "" {
			continue
		}
		score := fuzzy.TokenSetRatio(newRC, existingRC)
		if score > bestScore {
			bestScore = score
			bestFile = f.Path
		}
	}
	return bestScore, bestFile, nil
}

func readRootCauseFromFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	m := rootCauseRe.FindStringSubmatch(string(data))
	if len(m) < 2 {
		return "", nil
	}
	val := strings.TrimSpace(m[1])
	if val == "|" || val == ">" || val == "" {
		return "", nil
	}
	if len([]rune(val)) < MinRootCauseLen {
		return "", nil
	}
	return val, nil
}

// incrementRecurrence bumps the integer following `recurrence:` on the
// first match. If the file has no recurrence field we leave it alone —
// same policy as the Python regex-based implementation.
func incrementRecurrence(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	var out strings.Builder
	bumped := false
	re := regexp.MustCompile(`^recurrence:\s*(\d+)`)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !bumped {
			if m := re.FindStringSubmatch(line); m != nil {
				n, _ := strconv.Atoi(m[1])
				line = fmt.Sprintf("recurrence: %d", n+1)
				bumped = true
			}
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	f.Close()
	if err := scanner.Err(); err != nil {
		return err
	}
	// Atomic: a native memory file is user content not recoverable from the
	// WAL, so a crash mid-write must not truncate it (review C5).
	return pathutil.WriteFileAtomic(path, []byte(out.String()), 0o644)
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func slugFromPath(p string) string {
	return pathutil.SlugFromPath(p)
}
