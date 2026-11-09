package sidecar

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
)

// Mark is one row of the mark table: a /memboard directive on a fact.
// Slug carries ".md" like memory.slug. Arg is the project a hide applies
// to, a task's anchor (native.BodySHA of the file when it was marked), or
// "" for less. Day is the WAL date of the mark.
type Mark struct{ Project, Slug, Directive, Arg, Day string }

// IsTask reports whether directive opens a task for Claude (closed by
// `done`, by an edit of the file, or by retiring the fact).
func IsTask(directive string) bool {
	switch directive {
	case "rewrite", "split", "merge", "review":
		return true
	}
	return false
}

// IsDirective reports whether d is a directive `memoryctl mark` accepts.
func IsDirective(d string) bool {
	switch d {
	case "hide", "less", "clear", "done":
		return true
	}
	return IsTask(d)
}

// markNeedsArg: hide carries the project it applies to, a task its anchor.
func markNeedsArg(d string) bool { return d == "hide" || IsTask(d) }

// Marks returns the mark table, ordered by (project, slug, directive, arg).
func (s *Store) Marks() ([]Mark, error) {
	rows, err := s.db.Query(`SELECT project, slug, directive, arg, day FROM mark
		ORDER BY project, slug, directive, arg`)
	if err != nil {
		return nil, fmt.Errorf("sidecar.Marks: %w", err)
	}
	defer rows.Close()
	var out []Mark
	for rows.Next() {
		var m Mark
		if err := rows.Scan(&m.Project, &m.Slug, &m.Directive, &m.Arg, &m.Day); err != nil {
			return nil, fmt.Errorf("sidecar.Marks: scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// parseMarkTarget splits a `mark` WAL target
// "<project>\x1f<stem>:<directive>[:<arg>]". It reads from the right
// (directive and arg never hold ':'), so a ':' inside the stem survives.
// ok is false for an unqualified target, an unknown directive, a missing
// or unexpected arg, or a stem that is not a plain file name.
func parseMarkTarget(target string) (m Mark, ok bool) {
	project, rest, qualified := native.ParseQKey(target)
	if !qualified || project == "" {
		return Mark{}, false
	}
	parts := strings.Split(rest, ":")
	n := len(parts)
	switch {
	case n >= 2 && IsDirective(parts[n-1]) && !markNeedsArg(parts[n-1]):
		m.Directive, parts = parts[n-1], parts[:n-1]
	case n >= 3 && markNeedsArg(parts[n-2]) && parts[n-1] != "":
		m.Directive, m.Arg, parts = parts[n-2], parts[n-1], parts[:n-2]
	default:
		return Mark{}, false
	}
	stem := strings.TrimSuffix(strings.Join(parts, ":"), ".md")
	if stem == "" || strings.ContainsAny(stem, `/\`) {
		return Mark{}, false
	}
	m.Project, m.Slug = project, stem+".md"
	return m, true
}

type markKey struct{ project, slug, directive, arg string }

// replayMarks folds the lifecycle rows (WAL order) into the live mark set:
// hide/less/task upsert (a repeat mark moves the day), clear drops every
// row of the fact, done drops its tasks, and a retire drops every row of
// the fact recorded before it — a later revive does not bring them back.
func replayMarks(rows []walRow) map[markKey]string {
	live := map[markKey]string{}
	drop := func(project, slug string, which func(directive string) bool) {
		for k := range live {
			if k.project == project && k.slug == slug && which(k.directive) {
				delete(live, k)
			}
		}
	}
	all := func(string) bool { return true }
	for _, r := range rows {
		switch r.event {
		case "mark":
			m, ok := parseMarkTarget(r.target)
			if !ok {
				continue
			}
			switch m.Directive {
			case "clear":
				drop(m.Project, m.Slug, all)
			case "done":
				drop(m.Project, m.Slug, IsTask)
			default:
				live[markKey{m.Project, m.Slug, m.Directive, m.Arg}] = r.date
			}
		case "retire":
			project, rest, qualified := native.ParseQKey(r.target)
			if !qualified {
				continue
			}
			drop(project, strings.TrimSuffix(trimTargetExtras(rest), ".md")+".md", all)
		case "revive":
			// Deliberately nothing: marks made before the retire stay gone.
		}
	}
	return live
}

// replaceMarks rewrites the whole mark table from the replayed WAL rows,
// independent of the reprojection's project scope. Each surviving row whose
// project is in dirs is checked against its own file: a missing file drops
// it, and a task whose anchor no longer matches native.BodySHA is closed
// (the file was edited after the mark). A row whose project is not in dirs
// is kept unchecked — an incomplete dirs must never erase marks. A file
// that exists but cannot be read keeps its rows too.
func replaceMarks(e dbtx, rows []walRow, dirs map[string]string) error {
	live := replayMarks(rows)
	type fileState struct {
		missing bool
		sha     string // "" when unreadable
	}
	files := map[string]fileState{}
	for k := range live {
		dir, known := dirs[k.project]
		if !known {
			continue
		}
		path := filepath.Join(dir, k.slug)
		st, seen := files[path]
		if !seen {
			raw, err := os.ReadFile(path)
			switch {
			case os.IsNotExist(err):
				st.missing = true
			case err == nil:
				st.sha = native.BodySHA(string(raw))
			}
			files[path] = st
		}
		if st.missing || (IsTask(k.directive) && st.sha != "" && k.arg != st.sha) {
			delete(live, k)
		}
	}
	if _, err := e.Exec(`DELETE FROM mark`); err != nil {
		return fmt.Errorf("sidecar.ReprojectAll: clear marks: %w", err)
	}
	for k, day := range live {
		if _, err := e.Exec(`INSERT INTO mark (project, slug, directive, arg, day) VALUES (?,?,?,?,?)`,
			k.project, k.slug, k.directive, k.arg, day); err != nil {
			return fmt.Errorf("sidecar.ReprojectAll: insert mark %s/%s: %w", k.project, k.slug, err)
		}
	}
	return nil
}
