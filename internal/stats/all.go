package stats

import (
	"sort"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

const allDays = 7

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
	useful := u.UsefulByProject(allDays)
	for _, st := range stores {
		row := ProjectRow{Project: st.Project, Display: Display(st.Project, home), Useful7d: useful[st.Project]}
		for _, f := range files[st.Dir] {
			var rec *sidecar.Record
			if r, ok := recs[Key{Project: f.Project, Slug: f.Slug}]; ok {
				rec = &r
			}
			r := BuildFactRef(f, rec, u, marks, home)
			row.Facts++
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
	a.Funnel.Injected7d = u.DistinctInjected(allDays)
	a.Funnel.Useful7d = u.DistinctUseful(allDays)
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
