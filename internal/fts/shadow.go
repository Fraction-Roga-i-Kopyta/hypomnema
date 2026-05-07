package fts

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/pathutil"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/wal"
)

// ShadowConfig captures the configurable pieces of the shadow pass. Zero-value
// defaults match bin/memory-fts-shadow.sh.
type ShadowConfig struct {
	// MaxShadow caps the number of shadow-miss events written per pass.
	// Defaults to 4 (MEMORY_FTS_SHADOW_MAX in bash).
	MaxShadow int
	// Today is the date stamp written to each WAL event. When zero, uses
	// the current local date.
	Today string
	// CurrentProject is the project slug the prompt was issued from
	// (resolved by the caller via projects.json + cwd). When non-empty,
	// files whose frontmatter project is neither empty/global nor equal
	// to this slug are skipped — mirrors the project filter the primary
	// substring pipeline applies via priority key. Empty value disables
	// the filter (back-compat).
	CurrentProject string
}

// Shadow runs the full FTS5 recall-shadow pass: tokenizes the prompt, queries
// the index, filters to candidate directories, dedups against the session's
// injected set, and appends up to MaxShadow `shadow-miss` events to the WAL.
//
// Always returns nil: the shadow pass is observational — the contract
// (docs/FORMAT.md §5) says it MUST NOT affect injection. Callers that need
// stricter error handling should use the lower-level Query + Sync + wal.Append
// building blocks directly.
func Shadow(ctx context.Context, memoryDir, prompt, sessionID string, newInjected []string, cfg ShadowConfig) error {
	if prompt == "" || sessionID == "" {
		return nil
	}
	if cfg.MaxShadow == 0 {
		cfg.MaxShadow = 4
	}
	if cfg.Today == "" {
		cfg.Today = time.Now().Local().Format("2006-01-02")
	}

	dbPath := filepath.Join(memoryDir, "index.db")
	ix, err := Open(dbPath)
	if err != nil {
		return nil // observational — swallow
	}
	defer ix.Close()

	// Lazy sync to match bash query.sh behaviour. Index may be fully warm;
	// drift check is cheap.
	_ = ix.Sync(ctx, memoryDir)

	hits, err := ix.Query(ctx, prompt, cfg.MaxShadow+12)
	if err != nil || len(hits) == 0 {
		return nil
	}

	injected := loadInjectedSet(memoryDir, sessionID, newInjected)

	count := 0
	for _, h := range hits {
		if count >= cfg.MaxShadow {
			break
		}
		if !isCandidateDir(h.Path) {
			continue
		}
		slug := slugFromPath(h.Path)
		if _, seen := injected[slug]; seen {
			continue
		}
		if cfg.CurrentProject != "" {
			if !projectAllowed(h.Path, cfg.CurrentProject) {
				continue
			}
		}
		line := cfg.Today + "|shadow-miss|" + slug + "|" + sessionID
		dedupKey := "shadow-miss|" + slug + "|" + sessionID
		wal.Append(memoryDir, line, dedupKey)
		count++
	}
	return nil
}

// loadInjectedSet merges the session's already-injected slugs: those written by
// SessionStart into .runtime/injected-<session>.list plus any passed via the
// newInjected arg (UserPromptSubmit feeds us its current pass).
func loadInjectedSet(memoryDir, sessionID string, newInjected []string) map[string]struct{} {
	set := make(map[string]struct{})

	dedupFile := filepath.Join(memoryDir, ".runtime", "injected-"+sessionID+".list")
	if f, err := os.Open(dedupFile); err == nil {
		s := bufio.NewScanner(f)
		for s.Scan() {
			slug := strings.TrimSpace(s.Text())
			if slug != "" {
				set[slug] = struct{}{}
			}
		}
		f.Close()
	}

	for _, slug := range newInjected {
		slug = strings.TrimSpace(slug)
		if slug != "" {
			set[slug] = struct{}{}
		}
	}
	return set
}

// isCandidateDir gates shadow emission to the same subdirs the primary
// trigger pipeline scans — anything outside (continuity/, projects/, auto-
// generated files) would be noise in the recall signal.
func isCandidateDir(p string) bool {
	allow := []string{"/mistakes/", "/feedback/", "/strategies/", "/knowledge/", "/notes/", "/seeds/"}
	for _, seg := range allow {
		if strings.Contains(p, seg) {
			return true
		}
	}
	return false
}

// slugFromPath is a local wrapper around pathutil.SlugFromPath so
// in-package callers read cleanly. The canonical implementation
// lives in internal/pathutil.
func slugFromPath(p string) string {
	return pathutil.SlugFromPath(p)
}

// projectAllowed reads the frontmatter `project:` field of the file at
// path and returns true if the file is admissible under currentProject.
// Empty / "global" / equal-to-current → admissible. Anything else →
// rejected. Files we cannot open are admitted (fail-open matches the
// shadow pass's observational contract — never block on disk noise).
func projectAllowed(path, currentProject string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8*1024), 64*1024)
	fmCount := 0
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "---" {
			fmCount++
			if fmCount >= 2 {
				return true // no project: line found inside frontmatter → empty → global
			}
			continue
		}
		if fmCount != 1 {
			continue
		}
		if !strings.HasPrefix(line, "project:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, "project:"))
		v = strings.Trim(v, `"'`)
		v = strings.TrimSpace(v)
		switch v {
		case "", "global", currentProject:
			return true
		default:
			return false
		}
	}
	// File has no closing `---` or scanner error — fail-open.
	return true
}
