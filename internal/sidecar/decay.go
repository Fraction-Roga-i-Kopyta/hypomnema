package sidecar

import (
	"fmt"
	"time"
)

// staleDays is the active→stale threshold per fine-grained type, in days
// (CLAUDE.md memory-layout table). Unknown types default to 90.
var staleDays = map[string]int{
	"mistake": 60, "strategy": 90, "feedback": 45,
	"knowledge": 90, "decision": 90, "note": 30,
	"skill-learning": 120, // durable; decays slower than mistakes
}

// MarkStale flips active rows older than their type's stale threshold to
// status='stale' (down-rank, not removed). Age is measured from the last
// injection (actual use), falling back to created — a fact in active rotation
// never goes stale just because its file is old. pinned/stale/deleted are
// left alone. Returns the number newly marked. `today` is YYYY-MM-DD.
func (s *Store) MarkStale(today string) (int, error) {
	t0, err := time.Parse("2006-01-02", today)
	if err != nil {
		return 0, fmt.Errorf("sidecar.MarkStale: bad today %q: %w", today, err)
	}
	rows, err := s.db.Query(`SELECT slug, type, created, last_injected FROM memory WHERE status='active'`)
	if err != nil {
		return 0, fmt.Errorf("sidecar.MarkStale: query: %w", err)
	}
	var stale []string
	for rows.Next() {
		var slug, typ, created, lastInjected string
		if err := rows.Scan(&slug, &typ, &created, &lastInjected); err != nil {
			rows.Close()
			return 0, fmt.Errorf("sidecar.MarkStale: scan: %w", err)
		}
		// continuity/project are "where we left off" markers — exempt from
		// rotation entirely (CLAUDE.md lifecycle contract).
		if typ == "continuity" || typ == "project" {
			continue
		}
		c, perr := time.Parse("2006-01-02", lastInjected)
		if perr != nil {
			c, perr = time.Parse("2006-01-02", created)
		}
		if perr != nil {
			continue
		}
		thr, ok := staleDays[typ]
		if !ok {
			thr = 90
		}
		if int(t0.Sub(c).Hours()/24) > thr {
			stale = append(stale, slug)
		}
	}
	rows.Close()
	for _, slug := range stale {
		if _, err := s.db.Exec(`UPDATE memory SET status='stale' WHERE slug=? AND status='active'`, slug); err != nil {
			return 0, fmt.Errorf("sidecar.MarkStale: update %s: %w", slug, err)
		}
	}
	return len(stale), nil
}
