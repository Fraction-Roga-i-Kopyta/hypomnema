package stats

import (
	"sort"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

const allDays = 7

// InjectedKeys is the set of facts delivered (inject or recall rows) within
// the last days days.
func (u *Usage) InjectedKeys(days int) map[Key]bool {
	out := map[Key]bool{}
	for _, r := range u.rows {
		if (r.event == "inject" || r.event == "recall") && u.inWindow(r.date, days) {
			out[r.key] = true
		}
	}
	return out
}

// UsefulKeys is the set of facts with a useful citation in the window.
func (u *Usage) UsefulKeys(days int) map[Key]bool {
	out := map[Key]bool{}
	for _, ks := range u.usefulKeys(days) {
		for k := range ks {
			out[k] = true
		}
	}
	return out
}

// BuildAll assembles the all-memory scope. The corpus is the native files of
// every store (files is keyed by Store.Dir, each MemFile has Project set);
// the sidecar only supplies metadata, so stores it has never seen still
// count. Triage follows project scope's rules, with "handled" judged per
// fact against the fact's own project.
func BuildAll(stores []native.Store, files map[string][]native.MemFile, recs map[Key]sidecar.Record, u *Usage, marks []sidecar.Mark, home string) AllStats {
	if u == nil {
		u = &Usage{}
	}
	a := AllStats{
		Stores:   len(stores),
		Projects: []ProjectRow{},
		ByType:   map[string]int{},
		Triage:   Triage{Noisy: []FactRef{}, Candidates: []FactRef{}, Stale: []FactRef{}},
	}
	// Funnel stages 2 and 3 only count facts that currently have a file, so
	// useful <= injected <= stored and the project rows sum to the funnel.
	injKeys, useKeys := u.InjectedKeys(allDays), u.UsefulKeys(allDays)
	for _, st := range stores {
		row := ProjectRow{Project: st.Project, Display: Display(st.Project, home)}
		for _, f := range files[st.Dir] {
			var rec *sidecar.Record
			if r, ok := recs[Key{Project: f.Project, Slug: f.Slug}]; ok {
				rec = &r
			}
			r := BuildFactRef(f, rec, u, marks, home)
			row.Facts++
			k := Key{Project: f.Project, Slug: f.Slug}
			if injKeys[k] {
				a.Funnel.Injected7d++
			}
			if injKeys[k] && useKeys[k] {
				a.Funnel.Useful7d++
				row.Useful7d++
			}
			a.ByType[r.Type]++
			if r.Injected30d == 0 {
				a.Funnel.NotInjected30d++
			}
			switch r.Status {
			case "candidate":
				if r.Useful30d == 0 {
					a.Triage.Candidates = append(a.Triage.Candidates, r)
				}
			case "stale":
				a.Triage.Stale = append(a.Triage.Stale, r)
			}
			if r.Injected30d >= noisyInjected && r.Useful30d == 0 && !handled(r.Marks, r.Project) {
				a.Triage.Noisy = append(a.Triage.Noisy, r)
			}
		}
		a.Facts += row.Facts
		a.Projects = append(a.Projects, row)
	}
	a.Funnel.Stored = a.Facts
	sort.SliceStable(a.Projects, func(i, j int) bool {
		if a.Projects[i].Facts != a.Projects[j].Facts {
			return a.Projects[i].Facts > a.Projects[j].Facts
		}
		return a.Projects[i].Project < a.Projects[j].Project
	})
	a.Triage.Noisy = triageSort(a.Triage.Noisy)
	a.Triage.Candidates = triageSort(a.Triage.Candidates)
	a.Triage.Stale = triageSort(a.Triage.Stale)
	return a
}
