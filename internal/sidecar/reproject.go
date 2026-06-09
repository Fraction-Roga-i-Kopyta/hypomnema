package sidecar

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

type agg struct {
	injects    int
	pos        int
	neg        int
	created    string // earliest WAL date for the slug
	lastInject string // latest inject date
	// sessions holds one classification per session id: true once the slug
	// was trigger-useful in that session (useful wins over silent — close
	// runs every turn, so a fact silent at turn 1 may be cited at turn 5).
	sessions map[string]bool
}

func (a *agg) classify(session string, useful bool) {
	if a.sessions == nil {
		a.sessions = map[string]bool{}
	}
	if useful {
		a.sessions[session] = true
	} else if _, seen := a.sessions[session]; !seen {
		a.sessions[session] = false
	}
}

// outcomes folds the per-session trigger classifications into the legacy
// outcome-positive/negative counters: pos+neg for the Bayesian effectiveness.
func (a *agg) outcomes() (pos, neg int) {
	pos, neg = a.pos, a.neg
	for _, useful := range a.sessions {
		if useful {
			pos++
		} else {
			neg++
		}
	}
	return pos, neg
}

// Reproject rebuilds the memory + outcome tables from the supplied native
// files and the WAL at walPath. Existing rows are upserted (idempotent).
// Native files with no WAL history are inserted with ref_count 0 and the
// neutral effectiveness prior, so freshly-written facts are injectable
// immediately.
//
// The sidecar DB is shared across every project, while a session only ever
// sees "its project + global" files — so deletion reconciliation is scoped:
// a row is tombstoned (status='deleted', history kept — spec §5) only when
// its native file is missing from `files` AND its project is inside `scope`
// (the explicit scope plus every project present in `files`). Rows owned by
// other projects are never touched — that is the cross-project tombstoning
// fix. Same-name files in two projects still share one row (slug is the PK);
// last writer wins until a composite key lands.
func Reproject(s *Store, files []native.MemFile, walPath string, scope []string) error {
	aggs := readWALAgg(walPath)

	if _, err := s.db.Exec(`DELETE FROM outcome`); err != nil {
		return fmt.Errorf("sidecar.Reproject: clear outcome: %w", err)
	}

	for _, f := range files {
		// Look up by bare slug so v1 WAL (no .md) matches v2 native files (.md).
		a := aggs[strings.TrimSuffix(f.Slug, ".md")]
		if a == nil {
			a = &agg{}
		}
		pos, neg := a.outcomes()
		eff := float64(pos+1) / float64(pos+neg+2)
		// Frontmatter created is the author's recency claim; WAL-earliest is
		// only the fallback for files that never declared one.
		created := f.Created
		if created == "" {
			created = a.created
		}
		// Only "pinned" is honoured from frontmatter (it exempts the row from
		// decay); every other lifecycle state is sidecar-owned.
		status := "active"
		if f.Status == "pinned" {
			status = "pinned"
		}
		rec := Record{
			Slug:          f.Slug,
			ContentSHA:    f.ContentSHA,
			Type:          f.Type,
			Name:          f.Name,
			Description:   f.Description,
			Project:       f.Project,
			Created:       created,
			LastInjected:  a.lastInject,
			RefCount:      a.injects,
			Status:        status,
			Effectiveness: eff,
		}
		if err := s.Upsert(rec); err != nil {
			return fmt.Errorf("sidecar.Reproject: upsert %s: %w", f.Slug, err)
		}
	}

	// Scoped deletion reconciliation (see doc comment above).
	inScope := make(map[string]bool, len(scope)+4)
	for _, p := range scope {
		inScope[p] = true
	}
	keep := make(map[string]bool, len(files))
	for _, f := range files {
		keep[f.Slug] = true
		inScope[f.Project] = true
	}
	existing, err := s.All()
	if err != nil {
		return fmt.Errorf("sidecar.Reproject: reconcile list: %w", err)
	}
	for _, r := range existing {
		if !keep[r.Slug] && r.Status != "deleted" && inScope[r.Project] {
			if _, err := s.db.Exec(`UPDATE memory SET status='deleted' WHERE slug=?`, r.Slug); err != nil {
				return fmt.Errorf("sidecar.Reproject: mark deleted %s: %w", r.Slug, err)
			}
			// Drop the dead row's keywords so it stops matching overlap queries.
			if _, err := s.db.Exec(`DELETE FROM keyword WHERE slug=?`, r.Slug); err != nil {
				return fmt.Errorf("sidecar.Reproject: clear keywords %s: %w", r.Slug, err)
			}
		}
	}

	if err := replayOutcomes(s, walPath); err != nil {
		return fmt.Errorf("sidecar.Reproject: outcomes: %w", err)
	}
	if err := s.PopulateKeywords(files); err != nil {
		return fmt.Errorf("sidecar.Reproject: keywords: %w", err)
	}
	return nil
}

func readWALAgg(walPath string) map[string]*agg {
	out := map[string]*agg{}
	f, err := os.Open(walPath)
	if err != nil {
		return out // missing WAL -> no history, not fatal
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		date, event, slug, field4, ok := parseWALLine(sc.Text())
		if !ok {
			continue
		}
		// Normalise slug to bare form so v1 WAL entries (no .md suffix) and
		// v2 WAL entries (with .md suffix) aggregate to the same key.
		slug = strings.TrimSuffix(slug, ".md")
		a := out[slug]
		if a == nil {
			a = &agg{}
			out[slug] = a
		}
		switch event {
		case "inject":
			a.injects++
			if date > a.lastInject {
				a.lastInject = date
			}
		case "inject-agg":
			// field4 is the aggregated inject count, not a session id
			// (hooks/wal-compact.sh writes DATE|inject-agg|SLUG|<count>).
			n, err := strconv.Atoi(field4)
			if err != nil || n < 1 {
				n = 1
			}
			a.injects += n
			if date > a.lastInject {
				a.lastInject = date
			}
		case "outcome-positive":
			a.pos++
		case "outcome-negative":
			a.neg++
		// The trigger events close writes are the live usefulness signal:
		// cited-in-session → positive, injected-but-silent → negative.
		// Deduped per session via classify (close fires on every turn).
		case "trigger-useful":
			a.classify(field4, true)
		case "trigger-silent", "trigger-silent-retro":
			a.classify(field4, false)
		}
		if a.created == "" || date < a.created {
			a.created = date
		}
	}
	// sc.Err() is intentionally ignored: a partial/truncated WAL read yields
	// partial history, which is acceptable (the WAL is the audit source of
	// truth; the sidecar is a rebuildable projection).
	return out
}

func replayOutcomes(s *Store, walPath string) error {
	f, err := os.Open(walPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		date, event, slug, _, ok := parseWALLine(sc.Text())
		if !ok {
			continue
		}
		if event == "outcome-positive" || event == "outcome-negative" {
			if _, err := s.db.Exec(`INSERT INTO outcome (slug, ts, kind) VALUES (?,?,?)`,
				slug, date, strings.TrimPrefix(event, "outcome-")); err != nil {
				return fmt.Errorf("sidecar.replayOutcomes: insert %s: %w", slug, err)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("sidecar.replayOutcomes: scan: %w", err)
	}
	return nil
}

// parseWALLine splits a 4-column WAL line "DATE|EVENT|SLUG|FIELD4". FIELD4 is
// the session id for most events, but for inject-agg it is the aggregated
// inject count (see hooks/wal-compact.sh). The bool is false for blank lines,
// comments, or rows with fewer than 4 columns.
func parseWALLine(line string) (date, event, slug, field4 string, ok bool) {
	line = strings.TrimRight(line, "\r")
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", "", "", false
	}
	parts := strings.SplitN(line, "|", 4)
	if len(parts) != 4 {
		return "", "", "", "", false
	}
	return parts[0], parts[1], parts[2], parts[3], true
}
