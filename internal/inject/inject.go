package inject

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/rank"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/tokenize"
)

// maxBodyBytes bounds each injected record body. Bodies range from ~500B to
// 100KB+ (e.g. avatar-unified-analysis.md); without a cap a single large note
// dominates the context window. ~2.5KB ≈ a full mistake's root-cause +
// prevention, or ~1.2K Cyrillic chars. With MaxK records the injection is
// bounded at roughly MaxK × this.
const maxBodyBytes = 2500

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
	return Result{Markdown: render(ranked, bySlug, maxBodyBytes), Injected: injected}, nil
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
			if rerr := sidecar.Reproject(s, files, walPath); rerr == nil {
				recs, lerr = s.All()
			}
		}
		// Use the sidecar ONLY if it yielded rows. An empty sidecar (failed or
		// empty reproject) must NOT shadow the native corpus — fall through to
		// the degraded native-only rank. Spec §5: injection still happens.
		if lerr == nil && len(recs) > 0 {
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

func render(ranked []rank.Scored, bySlug map[string]native.MemFile, maxBody int) string {
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
			b.WriteString(capBody(f.Body, maxBody))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// capBody bounds a single record's body to maxBytes (a byte budget), backing
// up to a UTF-8 rune boundary so multi-byte text never splits, and appends a
// visible marker so a truncated hint reads as truncated.
func capBody(body string, maxBytes int) string {
	if maxBytes <= 0 || len(body) <= maxBytes {
		return body
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return strings.TrimRight(body[:cut], " \n") + "\n\n…(truncated)"
}
