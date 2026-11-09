package stats

import (
	"sort"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

// IndexRecords keys sidecar rows by (project, slug) for the FactRef builders.
func IndexRecords(recs []sidecar.Record) map[Key]sidecar.Record {
	out := make(map[Key]sidecar.Record, len(recs))
	for _, r := range recs {
		out[Key{Project: r.Project, Slug: r.Slug}] = r
	}
	return out
}

// BuildFactRef assembles the card fields of one live fact: identity and
// frontmatter from f (f.Project must be set, as native.Collect and the
// CLI resolvers do), projected metadata from rec, 30-day counts from u, and
// the fact's marks. rec nil (no sidecar, or the row is not projected yet)
// gives RefCount 0, Effectiveness 0.5 (the neutral prior) and the
// frontmatter status, "active" when absent. u nil gives zero counts. Name
// falls back to the file name and Type to "note", as in the injection
// header.
func BuildFactRef(f native.MemFile, rec *sidecar.Record, u *Usage, marks []sidecar.Mark, home string) FactRef {
	k := Key{Project: f.Project, Slug: f.Slug}
	r := FactRef{
		Project:       f.Project,
		Display:       Display(f.Project, home),
		Slug:          f.Slug,
		Name:          f.Name,
		Type:          f.Type,
		Status:        f.Status,
		Description:   f.Description,
		Path:          f.Path,
		Created:       f.Created,
		Effectiveness: 0.5,
		Marks:         markStrings(k, marks),
	}
	if r.Name == "" {
		r.Name = f.Slug
	}
	if r.Type == "" {
		r.Type = "note"
	}
	if rec != nil {
		applyRecord(&r, rec)
	}
	if r.Status == "" {
		r.Status = "active"
	}
	if u != nil {
		r.Injected30d, r.Useful30d = u.Fact(k)
	}
	return r
}

// KeyRef is the FactRef of a fact known only by its WAL key: no live file
// in the stores the caller scanned (retired, deleted, renamed, or a legacy
// unqualified key). Path stays "" — the sign of a missing file. The
// metadata comes from rec when the sidecar still has the row (a retired
// fact keeps status "retired"); without it Status and Type stay "", Name is
// the file name and Effectiveness the neutral prior 0.5.
func KeyRef(k Key, rec *sidecar.Record, u *Usage, marks []sidecar.Mark, home string) FactRef {
	r := FactRef{
		Project:       k.Project,
		Display:       Display(k.Project, home),
		Slug:          k.Slug,
		Effectiveness: 0.5,
		Marks:         markStrings(k, marks),
	}
	if rec != nil {
		r.Name, r.Type, r.Description, r.Created = rec.Name, rec.Type, rec.Description, rec.Created
		applyRecord(&r, rec)
	}
	if r.Name == "" {
		r.Name = k.Slug
	}
	if u != nil {
		r.Injected30d, r.Useful30d = u.Fact(k)
	}
	return r
}

// applyRecord copies the sidecar-projected fields. A record without a
// status (never written by upsert, which defaults it) keeps r.Status; a
// file without a created date takes the projection's WAL-earliest fallback.
func applyRecord(r *FactRef, rec *sidecar.Record) {
	r.RefCount = rec.RefCount
	r.Effectiveness = rec.Effectiveness
	r.LastInjected = rec.LastInjected
	r.LastUseful = rec.LastUseful
	if rec.Status != "" {
		r.Status = rec.Status
	}
	if r.Created == "" {
		r.Created = rec.Created
	}
}

// markStrings renders the marks of fact k in their display form — hide:<project>,
// less, task:<directive> — deduped (a task re-marked under a new anchor is
// one task) and sorted. Never nil.
func markStrings(k Key, marks []sidecar.Mark) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, m := range marks {
		if m.Project != k.Project || m.Slug != k.Slug {
			continue
		}
		var s string
		switch {
		case m.Directive == "hide":
			s = "hide:" + m.Arg
		case sidecar.IsTask(m.Directive):
			s = "task:" + m.Directive
		default:
			s = m.Directive
		}
		// A QKey separator never leaves memoryctl (JSON carries project and
		// slug apart); only a hand-edited WAL could smuggle one into an arg.
		if seen[s] || strings.Contains(s, "\x1f") {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// EnrichTop fills the FactRef of each Top entry of s (which Usage.Session
// leaves at project/slug): from the live file in files when its key is
// there, else from the key alone (KeyRef). Injected/Useful are kept. A nil
// s is a no-op.
func EnrichTop(s *SessionStats, files []native.MemFile, recs map[Key]sidecar.Record, u *Usage, marks []sidecar.Mark, home string) {
	if s == nil {
		return
	}
	byKey := make(map[Key]native.MemFile, len(files))
	for _, f := range files {
		byKey[Key{Project: f.Project, Slug: f.Slug}] = f
	}
	for i := range s.Top {
		k := Key{Project: s.Top[i].Project, Slug: s.Top[i].Slug}
		var rec *sidecar.Record
		if r, ok := recs[k]; ok {
			rec = &r
		}
		if f, ok := byKey[k]; ok {
			s.Top[i].FactRef = BuildFactRef(f, rec, u, marks, home)
		} else {
			s.Top[i].FactRef = KeyRef(k, rec, u, marks, home)
		}
	}
}
