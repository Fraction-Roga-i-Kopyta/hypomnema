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
	// retired is true when the WAL carries a retire event for the slug with
	// no later revive — reproject preserves the distinction from 'deleted'
	// (hand-removed file) so a rebuilt sidecar keeps the deliberate decision.
	retired bool
	// confirmed is true once a candidate-confirmed event was seen —
	// graduation of a soft-corroboration candidate is WAL-derived.
	confirmed bool
}

// graduated reports whether a candidate fact has earned active status: an
// explicit confirmation event, or any useful evidence (belt and braces —
// the event is the primary signal, usefulness covers a lost event).
func (a *agg) graduated() bool {
	if a.confirmed || a.pos > 0 {
		return true
	}
	for _, useful := range a.sessions {
		if useful {
			return true
		}
	}
	return false
}

// mergeFrom folds another aggregate into a (used to combine a fact's
// project-qualified history with its grandfathered legacy bare-slug history).
// nil src is a no-op. Session classifications union with useful-wins.
func (a *agg) mergeFrom(src *agg) {
	if src == nil {
		return
	}
	a.injects += src.injects
	a.pos += src.pos
	a.neg += src.neg
	if src.lastInject > a.lastInject {
		a.lastInject = src.lastInject
	}
	if a.created == "" || (src.created != "" && src.created < a.created) {
		a.created = src.created
	}
	for sess, useful := range src.sessions {
		a.classify(sess, useful)
	}
	a.retired = a.retired || src.retired
	a.confirmed = a.confirmed || src.confirmed
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
//
// The whole projection runs in ONE transaction: Reproject fires on every
// Stop hook, so concurrent injects must never observe a half-rebuilt
// sidecar, a crash mid-rebuild must not leave outcome empty, and one commit
// replaces thousands of per-statement fsyncs per turn.
func Reproject(s *Store, files []native.MemFile, walPath string, scope []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("sidecar.Reproject: begin: %w", err)
	}
	// No-op after Commit; on any early return it discards the partial rebuild.
	defer func() { _ = tx.Rollback() }()
	if err := reprojectIn(tx, files, walPath, scope); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sidecar.Reproject: commit: %w", err)
	}
	return nil
}

func reprojectIn(e dbtx, files []native.MemFile, walPath string, scope []string) error {
	aggs := readWALAgg(walPath)

	if _, err := e.Exec(`DELETE FROM outcome`); err != nil {
		return fmt.Errorf("sidecar.Reproject: clear outcome: %w", err)
	}

	for _, f := range files {
		// Combine this file's project-qualified history (new events) with any
		// legacy bare-slug history (grandfathered onto the current owner — M2).
		// Same-basename files in two projects each get their own qualified
		// aggregate, so their effectiveness no longer merges (review E5-deep).
		bare := strings.TrimSuffix(f.Slug, ".md")
		a := &agg{}
		a.mergeFrom(aggs[native.QKey(f.Project, bare)]) // qualified (v2.10+)
		a.mergeFrom(aggs[bare])                         // legacy bare slug
		pos, neg := a.outcomes()
		// An ambient rule shapes behaviour silently (language pref, security
		// baseline, meta-policy) — going uncited is its NORMAL mode, not
		// evidence it failed. Counting trigger-silent as negative spiralled its
		// effectiveness down, then effGate damped its ranking — the "poor get
		// poorer" loop (P3a). Drop the silence penalty so it stays at/above the
		// neutral prior; explicit useful citations still raise it.
		if f.PrecisionClass == "ambient" {
			neg = 0
		}
		eff := float64(pos+1) / float64(pos+neg+2)
		// Frontmatter created is the author's recency claim; WAL-earliest is
		// only the fallback for files that never declared one.
		created := f.Created
		if created == "" {
			created = a.created
		}
		// Only "pinned" and (until graduation) "candidate" are honoured from
		// frontmatter; every other lifecycle state is sidecar-owned. A
		// candidate whose WAL carries a confirmation projects as active —
		// the content file is never rewritten (same pattern as stale).
		status := "active"
		switch {
		case f.Status == "pinned":
			status = "pinned"
		case f.Status == "candidate" && !a.graduated():
			status = "candidate"
		}
		rec := Record{
			Slug:        f.Slug,
			ContentSHA:  f.ContentSHA,
			Type:        f.Type,
			Name:        f.Name,
			Description: f.Description,
			Project:     f.Project,
			// Persist domains so the column is correct (review E7). This does
			// NOT activate domain filtering — Query.Domains stays empty, so
			// rank.domainOK still passes everything; wiring the filter is a
			// behaviour change left for a design cycle.
			Domains:       strings.Join(f.Domains, ","),
			Created:       created,
			LastInjected:  a.lastInject,
			RefCount:      a.injects,
			Status:        status,
			Effectiveness: eff,
		}
		if err := upsertIn(e, rec); err != nil {
			return fmt.Errorf("sidecar.Reproject: upsert %s: %w", f.Slug, err)
		}
	}

	// Scoped deletion reconciliation (see doc comment above). keep is keyed
	// by the composite (project, slug) — a same-basename file in another
	// project must not shield this project's dead row from reconciliation.
	inScope := make(map[string]bool, len(scope)+4)
	for _, p := range scope {
		inScope[p] = true
	}
	keep := make(map[string]bool, len(files))
	for _, f := range files {
		keep[native.QKey(f.Project, f.Slug)] = true
		inScope[f.Project] = true
	}

	// A retired fact's file lives in .archive/ and is absent from `files`,
	// so the upsert loop never writes its row — yet a rebuilt sidecar must
	// still know the retirement was deliberate (tombstones survive rebuild).
	// Insert minimal retired rows from the WAL aggregates. Only qualified
	// aggregates carry a project (retire events postdate qualification), and
	// only in-scope projects are touched (scoped-reconciliation invariant).
	for key, a := range aggs {
		if !a.retired {
			continue
		}
		project, bare, qualified := native.ParseQKey(key)
		if !qualified || !inScope[project] {
			continue
		}
		slug := bare + ".md"
		if keep[native.QKey(project, slug)] {
			continue // file is back in the live store (hand-restored) — live row wins
		}
		if err := upsertIn(e, Record{Slug: slug, Project: project, Status: "retired",
			Created: a.created, LastInjected: a.lastInject, RefCount: a.injects}); err != nil {
			return fmt.Errorf("sidecar.Reproject: retired row %s: %w", slug, err)
		}
	}

	existing, err := allIn(e)
	if err != nil {
		return fmt.Errorf("sidecar.Reproject: reconcile list: %w", err)
	}
	for _, r := range existing {
		if keep[native.QKey(r.Project, r.Slug)] || r.Status == "deleted" || r.Status == "retired" || !inScope[r.Project] {
			continue
		}
		status := "deleted"
		bare := strings.TrimSuffix(r.Slug, ".md")
		if a := aggs[native.QKey(r.Project, bare)]; a != nil && a.retired {
			status = "retired"
		} else if a := aggs[bare]; a != nil && a.retired {
			status = "retired"
		}
		if _, err := e.Exec(`UPDATE memory SET status=? WHERE slug=? AND project=?`, status, r.Slug, r.Project); err != nil {
			return fmt.Errorf("sidecar.Reproject: mark %s %s: %w", status, r.Slug, err)
		}
		// Drop the dead row's keywords so it stops matching overlap queries.
		if _, err := e.Exec(`DELETE FROM keyword WHERE slug=? AND project=?`, r.Slug, r.Project); err != nil {
			return fmt.Errorf("sidecar.Reproject: clear keywords %s: %w", r.Slug, err)
		}
	}

	if err := replayOutcomes(e, walPath); err != nil {
		return fmt.Errorf("sidecar.Reproject: outcomes: %w", err)
	}
	if err := populateKeywordsIn(e, files); err != nil {
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
		date, event, target, field4, ok := parseWALLine(sc.Text())
		if !ok {
			continue
		}
		// Targets may be project-qualified (project\x1fslug, v2.10+) or a legacy
		// bare slug (pre-v2.10). Key qualified events by QKey(project, bareslug)
		// so two projects' same-basename facts aggregate separately; key legacy
		// events by the bare slug alone — reproject grandfathers those onto the
		// current owner(s). Normalise .md so v1/v2 slugs collapse.
		project, slug, qualified := native.ParseQKey(target)
		// Lifecycle events carry sub-delimited extras (">successor",
		// ":reason") in the slug part — strip them before keying.
		switch event {
		case "retire", "revive", "ablate-start", "ablate-stop":
			slug = trimTargetExtras(slug)
		}
		slug = strings.TrimSuffix(slug, ".md")
		key := slug
		if qualified {
			key = native.QKey(project, slug)
		}
		a := out[key]
		if a == nil {
			a = &agg{}
			out[key] = a
		}
		switch event {
		case "inject":
			a.injects++
			if date > a.lastInject {
				a.lastInject = date
			}
		case "recall":
			// Pull delivery (memoryctl recall) — counts as an injection for
			// ref_count/recency, which is also what revives a stale row:
			// Reproject resets status, MarkStale sees a fresh last_injected.
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
		case "retire":
			a.retired = true
		case "revive":
			a.retired = false
		case "candidate-confirmed":
			a.confirmed = true
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

func replayOutcomes(e dbtx, walPath string) error {
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
			if _, err := e.Exec(`INSERT INTO outcome (slug, ts, kind) VALUES (?,?,?)`,
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

// trimTargetExtras strips sub-delimited extras (">successor", ":reason",
// ":N") from a WAL target's slug part, leaving the bare aggregation key.
// Used for retire/revive/ablate-start/ablate-stop targets.
func trimTargetExtras(s string) string {
	if i := strings.IndexAny(s, ">:"); i >= 0 {
		return s[:i]
	}
	return s
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
