package profile

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/decisions"
)

// AppendDecisionsPressure appends a "## Decisions under pressure"
// section to self-profile.md when any ADR review-trigger fires.
// No-op (leaves file untouched) when the decisions directory is
// absent, empty, or every trigger evaluates ok.
//
// Called post-Generate so the byte-for-byte parity fixture (no
// decisions/ dir) stays identical between bash and Go paths — the
// new section only materialises on installs that actually carry
// personal ADRs. Bash can grow the same logic later; until then the
// section is a Go-only feature, noted in docs.
func AppendDecisionsPressure(memoryDir string) error {
	dir := filepath.Join(memoryDir, "decisions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	now, err := resolveProfileTime()
	if err != nil {
		return err
	}
	snap, err := decisions.BuildSnapshot(memoryDir, now)
	if err != nil {
		return err
	}

	type fired struct {
		slug, message string
		status        decisions.Status
	}
	var firedResults []fired
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "README.md" {
			continue
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		_, triggers, err := decisions.ReadADR(filepath.Join(dir, e.Name()))
		if err != nil || len(triggers) == 0 {
			continue
		}
		for _, r := range decisions.Evaluate(triggers, slug, snap) {
			if r.Status == decisions.StatusPressure || r.Status == decisions.StatusOverdue {
				firedResults = append(firedResults, fired{slug, r.Message, r.Status})
			}
		}
	}
	if len(firedResults) == 0 {
		return nil
	}

	// Stable order: overdue first, then pressure, then slug.
	sort.SliceStable(firedResults, func(i, j int) bool {
		if firedResults[i].status != firedResults[j].status {
			return firedResults[i].status == decisions.StatusOverdue
		}
		return firedResults[i].slug < firedResults[j].slug
	})

	profilePath := filepath.Join(memoryDir, "self-profile.md")
	f, err := os.OpenFile(profilePath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsNotExist(err) {
			// self-profile.md not generated (e.g. no WAL). Nothing to
			// append to — skip silently, same policy as Generate().
			return nil
		}
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	fmt.Fprint(w, "\n## Decisions under pressure\n\n")
	for _, r := range firedResults {
		fmt.Fprintf(w, "- **%s** — %s\n", r.slug, r.message)
	}
	return w.Flush()
}

// resolveProfileTime mirrors the HYPOMNEMA_NOW / HYPOMNEMA_TODAY
// precedence used by Generate() so the appended section's evaluation
// freezes with the rest of the profile in tests.
func resolveProfileTime() (time.Time, error) {
	if s := os.Getenv("HYPOMNEMA_TODAY"); s != "" {
		return time.Parse("2006-01-02", s)
	}
	if s := os.Getenv("HYPOMNEMA_NOW"); s != "" {
		if t, err := time.Parse("2006-01-02 15:04", s); err == nil {
			return t, nil
		}
	}
	return time.Now(), nil
}
