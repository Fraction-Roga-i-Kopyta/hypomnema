package inject

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/rank"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/tokenize"
)

// Input is everything Run needs (parsed by the memoryctl inject verb).
type Input struct {
	Event      string
	SessionID  string
	CWD        string
	Prompt     string
	ClaudeHome string // <home>/.claude
	MemoryDir  string
	Today      string
	MaxK       int
}

// Result is the rendered context plus the slugs injected.
type Result struct {
	Markdown string
	Injected []string
}

// Run orchestrates a single injection: keywords → native files → ranked
// top-K → Markdown. Recovers from a corrupt sidecar by rebuilding; falls
// back to a native-only degraded rank if the sidecar is unusable.
func Run(in Input) (Result, error) {
	osHome := filepath.Dir(in.ClaudeHome)
	files, err := collectNative(osHome, in.CWD)
	if err != nil {
		return Result{}, fmt.Errorf("inject.Run: native: %w", err)
	}
	if len(files) == 0 {
		return Result{}, nil
	}
	bySlug := map[string]native.MemFile{}
	for _, f := range files {
		bySlug[f.Slug] = f
	}
	terms := Keywords(in.CWD, in.Prompt)
	cands := candidates(in, files, terms)
	if in.MaxK <= 0 {
		in.MaxK = 8
	}
	ranked := rank.Rank(rank.Query{Terms: terms, Today: in.Today}, cands, in.MaxK)
	injected := make([]string, 0, len(ranked))
	for _, sc := range ranked {
		injected = append(injected, sc.Slug)
	}
	return Result{Markdown: render(ranked, bySlug), Injected: injected}, nil
}

func collectNative(osHome, cwd string) ([]native.MemFile, error) {
	proj, err := native.List(native.ProjectMemoryDir(osHome, cwd))
	if err != nil {
		return nil, err
	}
	glob, err := native.List(native.GlobalMemoryDir(osHome))
	if err != nil {
		return nil, err
	}
	return append(proj, glob...), nil
}

func candidates(in Input, files []native.MemFile, terms []string) []rank.Candidate {
	sidePath := filepath.Join(in.MemoryDir, ".sidecar.db")
	walPath := filepath.Join(in.MemoryDir, ".wal")

	s, err := sidecar.Open(sidePath)
	if err != nil {
		_ = os.Remove(sidePath)
		s, err = sidecar.Open(sidePath)
	}
	if err == nil {
		defer s.Close()
		recs, lerr := s.All()
		if lerr == nil && len(recs) == 0 {
			_ = sidecar.Reproject(s, files, walPath)
			recs, lerr = s.All()
		}
		if lerr == nil {
			overlap, _ := s.OverlapScores(terms)
			out := make([]rank.Candidate, 0, len(recs))
			for _, r := range recs {
				out = append(out, rank.Candidate{
					Slug: r.Slug, Type: r.Type, Project: r.Project,
					Domains: splitCSV(r.Domains), RefCount: r.RefCount,
					Effectiveness: r.Effectiveness, Status: r.Status,
					Created: r.Created, LastInjected: r.LastInjected,
					Overlap: overlap[r.Slug],
				})
			}
			return out
		}
	}
	return degradedCandidates(files, terms)
}

func degradedCandidates(files []native.MemFile, terms []string) []rank.Candidate {
	want := map[string]bool{}
	for _, t := range terms {
		want[t] = true
	}
	out := make([]rank.Candidate, 0, len(files))
	for _, f := range files {
		seen := map[string]bool{}
		overlap := 0
		for _, tok := range tokenize.Tokenize(f.Name+" "+f.Description+" "+f.Body, nil) {
			if want[tok] && !seen[tok] {
				seen[tok] = true
				overlap++
			}
		}
		out = append(out, rank.Candidate{
			Slug: f.Slug, Type: f.Type, Status: "active",
			Effectiveness: 0.5, Overlap: overlap,
		})
	}
	return out
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func render(ranked []rank.Scored, bySlug map[string]native.MemFile) string {
	if len(ranked) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Memory Context\n")
	for _, sc := range ranked {
		f := bySlug[sc.Slug]
		title := f.Name
		if title == "" {
			title = sc.Slug
		}
		fmt.Fprintf(&b, "\n## %s\n", title)
		if f.Body != "" {
			b.WriteString(f.Body)
			b.WriteString("\n")
		}
	}
	return b.String()
}
