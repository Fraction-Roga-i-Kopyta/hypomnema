package stats

import (
	"sort"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
)

const (
	projectDays   = 7
	triageMax     = 50
	noisyInjected = 5
	problemRunes  = 120
)

// Problem cuts a doctor check detail to the panel's 120-rune limit.
func Problem(detail string) string {
	if r := []rune(detail); len(r) > problemRunes {
		return string(r[:problemRunes])
	}
	return detail
}

// BuildProject assembles the project scope. files are the facts visible to
// the store (its own plus global, Project set); sessions are the parent
// session ids of that project (empty or nil gives all-zero days); health is
// the in-process doctor summary.
func BuildProject(st native.Store, files []native.MemFile, recs map[Key]sidecar.Record, u *Usage, marks []sidecar.Mark, sessions map[string]bool, health Health, home string) ProjectStats {
	if sessions == nil {
		sessions = map[string]bool{}
	}
	if health.Problems == nil {
		health.Problems = []string{}
	}
	if u == nil {
		u = &Usage{}
	}
	ps := ProjectStats{
		Project: st.Project,
		Display: Display(st.Project, home),
		ByType:  map[string]int{},
		Days:    u.Days(sessions, projectDays),
		Health:  health,
		Queue:   []FactRef{},
		Triage:  Triage{Noisy: []FactRef{}, Candidates: []FactRef{}, Stale: []FactRef{}},
	}
	for _, f := range files {
		var rec *sidecar.Record
		if r, ok := recs[Key{Project: f.Project, Slug: f.Slug}]; ok {
			rec = &r
		}
		r := BuildFactRef(f, rec, u, marks, home)
		ps.Visible++
		if f.Project == native.GlobalProject {
			ps.Global++
		} else {
			ps.Local++
		}
		ps.ByType[r.Type]++
		switch r.Status {
		case "candidate":
			ps.Candidates++
			if r.Useful30d == 0 {
				ps.Triage.Candidates = append(ps.Triage.Candidates, r)
			}
		case "stale":
			ps.Stale++
			ps.Triage.Stale = append(ps.Triage.Stale, r)
		case "pinned":
			ps.Pinned++
		}
		if r.Injected30d >= noisyInjected && r.Useful30d == 0 && !handled(r.Marks, st.Project) {
			ps.Triage.Noisy = append(ps.Triage.Noisy, r)
		}
		for _, m := range r.Marks {
			if strings.HasPrefix(m, "task:") {
				ps.Queue = append(ps.Queue, r)
				break
			}
		}
	}
	ps.Triage.Noisy = triageSort(ps.Triage.Noisy)
	ps.Triage.Candidates = triageSort(ps.Triage.Candidates)
	ps.Triage.Stale = triageSort(ps.Triage.Stale)
	ps.Queue = triageSort(ps.Queue)
	return ps
}

// handled: the user already dealt with the fact in this project (less, or
// hidden here); hide only affects injection, so counts still include it.
func handled(marks []string, project string) bool {
	for _, m := range marks {
		if m == "less" || m == "hide:"+project {
			return true
		}
	}
	return false
}

// triageSort orders by Injected30d desc then slug and caps at triageMax.
func triageSort(l []FactRef) []FactRef {
	sort.SliceStable(l, func(i, j int) bool {
		if l[i].Injected30d != l[j].Injected30d {
			return l[i].Injected30d > l[j].Injected30d
		}
		return l[i].Slug < l[j].Slug
	})
	if len(l) > triageMax {
		l = l[:triageMax]
	}
	return l
}
