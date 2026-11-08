package sidecar

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// staleDays is the active→stale threshold per fine-grained type, in days
// (CLAUDE.md memory-layout table). Unknown types default to 90.
var staleDays = map[string]int{
	"mistake": 60, "strategy": 90, "feedback": 45,
	"knowledge": 90, "decision": 90, "note": 30,
	"skill-learning": 120, // durable; decays slower than mistakes
	// Claude Code's own memory types (its instructions write these under
	// metadata.type): who-the-user-is is durable; a reference decays like
	// knowledge/decision. Its `project` means "ongoing work, goals,
	// constraints" — plans and work notes, not a project card — so it ages
	// like knowledge; a card that must never age is pinned.
	"user": 180, "reference": 90, "project": 90,
	// A continuity fact that is no longer its project's freshest is history
	// (the newest one is exempt — see MarkStale) and ages like a note.
	"continuity": 30,
}

// MarkStale flips active rows older than their type's stale threshold to
// status='stale' (down-rank, not removed). Age is measured from the last
// injection (actual use), falling back to created — a fact in active rotation
// never goes stale just because its file is old. pinned/stale/deleted/
// retired are left alone; candidate rows are exempt by construction (the
// WHERE status='active' filter) — an uncorroborated candidate is doctor's
// concern, not decay's. The freshest continuity fact of each project (by its
// stored `fresh` date, the same native dates injection compares — see
// native.NewestContinuity) is the live "where we left
// off" marker and never ages; older continuity files do. Rows are keyed by
// (slug, project): continuity.md exists in every project. Returns the number
// newly marked. `today` is YYYY-MM-DD.
func (s *Store) MarkStale(today string) (int, error) {
	t0, err := time.Parse("2006-01-02", today)
	if err != nil {
		return 0, fmt.Errorf("sidecar.MarkStale: bad today %q: %w", today, err)
	}
	rows, err := s.db.Query(`SELECT slug, project, type, created, fresh, last_injected, status
		FROM memory WHERE status IN ('active','pinned','candidate')`)
	if err != nil {
		return 0, fmt.Errorf("sidecar.MarkStale: query: %w", err)
	}
	type row struct{ slug, project, typ, created, lastInjected, status string }
	var live []row
	var conts []native.MemFile
	for rows.Next() {
		var r row
		var fresh sql.NullString
		if err := rows.Scan(&r.slug, &r.project, &r.typ, &r.created, &fresh, &r.lastInjected, &r.status); err != nil {
			rows.Close()
			return 0, fmt.Errorf("sidecar.MarkStale: scan: %w", err)
		}
		live = append(live, r)
		if r.typ == "continuity" {
			// `fresh` is exactly what injection compares (native dates only);
			// `created` may carry the WAL-earliest fallback and must not.
			conts = append(conts, native.MemFile{Slug: r.slug, Project: r.project, Type: r.typ,
				Created: fresh.String})
		}
	}
	rows.Close()
	newest := native.NewestContinuity(conts)

	var stale []row
	for _, r := range live {
		if r.status != "active" {
			continue
		}
		if r.typ == "continuity" && newest[r.project] == r.slug {
			continue
		}
		c, perr := time.Parse("2006-01-02", r.lastInjected)
		if perr != nil {
			c, perr = time.Parse("2006-01-02", r.created)
		}
		if perr != nil {
			continue
		}
		thr, ok := staleDays[r.typ]
		if !ok {
			thr = 90
		}
		if int(t0.Sub(c).Hours()/24) > thr {
			stale = append(stale, r)
		}
	}
	for _, r := range stale {
		if _, err := s.db.Exec(`UPDATE memory SET status='stale' WHERE slug=? AND project=? AND status='active'`,
			r.slug, r.project); err != nil {
			return 0, fmt.Errorf("sidecar.MarkStale: update %s: %w", r.slug, err)
		}
	}
	return len(stale), nil
}
