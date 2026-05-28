package sidecar

import (
	"bufio"
	"fmt"
	"os"
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
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		date, event, slug, ok := parseWALLine(sc.Text())
		if !ok {
			continue
		}
		a := out[slug]
		if a == nil {
			a = &agg{}
			out[slug] = a
		}
		switch event {
		case "inject", "inject-agg":
			a.injects++
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
	return out
}

func replayOutcomes(s *Store, walPath string) error {
	f, err := os.Open(walPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		date, event, slug, ok := parseWALLine(sc.Text())
		if !ok {
			continue
		}
		if event == "outcome-positive" || event == "outcome-negative" {
			if _, err := s.db.Exec(`INSERT INTO outcome (slug, ts, kind) VALUES (?,?,?)`,
				slug, date, strings.TrimPrefix(event, "outcome-")); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseWALLine splits a 4-column WAL line "DATE|EVENT|SLUG|SESSION". The bool
// is false for blank lines, comments, or malformed rows.
func parseWALLine(line string) (date, event, slug string, ok bool) {
	line = strings.TrimRight(line, "\r")
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", "", false
	}
	parts := strings.SplitN(line, "|", 4)
	if len(parts) != 4 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}
