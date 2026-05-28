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
}

// Reproject rebuilds the memory + outcome tables from the supplied native
// files and the WAL at walPath. Existing rows are upserted (idempotent).
// Native files with no WAL history are inserted with ref_count 0 and the
// neutral effectiveness prior, so freshly-written facts are injectable
// immediately. The keyword table is left untouched (a later phase owns it).
func Reproject(s *Store, files []native.MemFile, walPath string) error {
	aggs := readWALAgg(walPath)

	if _, err := s.db.Exec(`DELETE FROM outcome`); err != nil {
		return fmt.Errorf("sidecar.Reproject: clear outcome: %w", err)
	}

	for _, f := range files {
		a := aggs[f.Slug]
		if a == nil {
			a = &agg{}
		}
		eff := float64(a.pos+1) / float64(a.pos+a.neg+2)
		rec := Record{
			Slug:          f.Slug,
			ContentSHA:    f.ContentSHA,
			Type:          f.Type,
			Name:          f.Name,
			Description:   f.Description,
			Created:       a.created,
			LastInjected:  a.lastInject,
			RefCount:      a.injects,
			Status:        "active",
			Effectiveness: eff,
		}
		if err := s.Upsert(rec); err != nil {
			return fmt.Errorf("sidecar.Reproject: upsert %s: %w", f.Slug, err)
		}
	}

	if err := replayOutcomes(s, walPath); err != nil {
		return fmt.Errorf("sidecar.Reproject: outcomes: %w", err)
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
