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
// prevention, or ~1.2K Cyrillic chars.
const maxBodyBytes = 2500

// maxTotalBytes bounds the whole rendered injection. Claude Code persists a
// hook's additionalContext to a file (leaving only a ~2KB inline preview)
// once it exceeds roughly 10KB, so an oversized injection never actually
// reaches the model — the budget keeps the payload inline. Facts cut by the
// budget are not recorded as injected and get their turn on a later prompt.
const maxTotalBytes = 8000

// MaxBodyBytes is the per-record body cap, shared by push (injection) and
// pull (memoryctl recall) so a recalled note obeys the same context budget.
const MaxBodyBytes = maxBodyBytes

// MaxTotalBytes is the whole-payload cap, exported so other envelope emitters
// (skill-inject) enforce the same budget the injection render path does.
const MaxTotalBytes = maxTotalBytes

// CapBody bounds one record body to maxBytes at a UTF-8 rune boundary with a
// visible truncation marker. Exported for the recall verb.
func CapBody(body string, maxBytes int) string { return capBody(body, maxBytes) }

// Candidates assembles ranker-ready candidates for an ad-hoc query against
// the project+global scope — the same sidecar-backed assembly (self-healing
// reproject, degraded native-only fallback) the injection pipeline uses.
// Exported for the recall verb.
func Candidates(memoryDir, cwd string, files []native.MemFile, terms []string) []rank.Candidate {
	return candidates(Input{MemoryDir: memoryDir, CWD: cwd}, files, terms)
}

// Input is everything Run needs (parsed by the memoryctl inject verb).
type Input struct {
	Event           string
	SessionID       string
	CWD             string
	Prompt          string
	ClaudeHome      string // <home>/.claude
	MemoryDir       string
	Today           string
	MaxK            int
	MaxBytes        int      // total render budget; <=0 → maxTotalBytes
	AlreadyInjected []string // slugs already injected this session — never re-rendered
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
	files := native.Collect(in.ClaudeHome, in.CWD)
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
	ranked := rank.Rank(rank.Query{
		Terms: terms, Today: in.Today, Project: native.SlugFromCWD(in.CWD),
	}, cands, in.MaxK)
	if len(in.AlreadyInjected) > 0 {
		seen := make(map[string]bool, len(in.AlreadyInjected))
		for _, s := range in.AlreadyInjected {
			seen[s] = true
		}
		fresh := ranked[:0]
		for _, sc := range ranked {
			if !seen[sc.Slug] {
				fresh = append(fresh, sc)
			}
		}
		ranked = fresh
	}
	maxTotal := in.MaxBytes
	if maxTotal <= 0 {
		maxTotal = maxTotalBytes
	}
	md, injected := render(ranked, bySlug, maxBodyBytes, maxTotal)
	return Result{Markdown: md, Injected: injected}, nil
}

func candidates(in Input, files []native.MemFile, terms []string) []rank.Candidate {
	sidePath := filepath.Join(in.MemoryDir, ".sidecar.db")
	walPath := filepath.Join(in.MemoryDir, ".wal")
	projectSlug := native.SlugFromCWD(in.CWD)

	s, err := sidecar.Open(sidePath)
	if err != nil {
		// SQLite state spans three files; removing only the main DB can leave
		// a poisoned -wal/-shm that re-corrupts the fresh handle. Mirror the
		// schema-mismatch wipe in sidecar.open.
		for _, p := range []string{sidePath, sidePath + "-wal", sidePath + "-shm"} {
			_ = os.Remove(p)
		}
		s, err = sidecar.Open(sidePath)
	}
	if err == nil {
		defer s.Close()
		recs, lerr := s.All()
		scoped := scopeRecords(recs, projectSlug)
		// No rows for this project ∪ global means the sidecar has never seen
		// this project (or is empty) — reproject from the files we just
		// listed and retry. Self-heals a sidecar seeded by other projects.
		if lerr == nil && len(scoped) == 0 && len(files) > 0 {
			if rerr := sidecar.Reproject(s, files, walPath, native.Scope(in.CWD)); rerr == nil {
				if recs, lerr = s.All(); lerr == nil {
					scoped = scopeRecords(recs, projectSlug)
				}
			}
		}
		// Use the sidecar ONLY if it yielded rows for this scope. An empty
		// result (failed or empty reproject) must NOT shadow the native
		// corpus — fall through to the degraded native-only rank. Spec §5:
		// injection still happens.
		if lerr == nil && len(scoped) > 0 {
			overlap, _ := s.OverlapScores(terms)
			out := make([]rank.Candidate, 0, len(scoped))
			for _, r := range scoped {
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

// scopeRecords keeps only rows owned by this project or the global store —
// the shared sidecar also carries every other project's rows.
func scopeRecords(recs []sidecar.Record, projectSlug string) []sidecar.Record {
	var out []sidecar.Record
	for _, r := range recs {
		if r.Project == projectSlug || r.Project == native.GlobalProject {
			out = append(out, r)
		}
	}
	return out
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
		text := f.Name + " " + f.Description + " " +
			strings.Join(f.Keywords, " ") + " " + strings.Join(f.Domains, " ") + " " + f.Body
		for _, tok := range tokenize.Tokenize(text, nil) {
			if want[tok] && !seen[tok] {
				seen[tok] = true
				overlap++
			}
		}
		out = append(out, rank.Candidate{
			Slug: f.Slug, Type: f.Type, Project: f.Project, Status: "active",
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

// render writes ranked entries in order until the total budget is spent and
// returns the markdown plus the slugs actually rendered — the injection
// bookkeeping (WAL events, session set) must follow what the model can see,
// not what the ranker picked. The top entry always lands, truncated to the
// budget if it alone overflows.
func render(ranked []rank.Scored, bySlug map[string]native.MemFile, maxBody, maxTotal int) (string, []string) {
	if len(ranked) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("# Memory Context\n")
	var injected []string
	for _, sc := range ranked {
		f := bySlug[sc.Slug]
		title := f.Name
		if title == "" {
			title = sc.Slug
		}
		head := fmt.Sprintf("\n## %s\n", title)
		body := ""
		if f.Body != "" {
			body = capBody(f.Body, maxBody) + "\n"
		}
		if maxTotal > 0 && b.Len()+len(head)+len(body) > maxTotal {
			if len(injected) > 0 {
				break
			}
			room := maxTotal - b.Len() - len(head) - 20 // marker + newline slack
			if room <= 0 {
				break
			}
			body = capBody(f.Body, room) + "\n"
			if b.Len()+len(head)+len(body) > maxTotal {
				break
			}
		}
		b.WriteString(head)
		b.WriteString(body)
		injected = append(injected, sc.Slug)
	}
	if len(injected) == 0 {
		return "", nil
	}
	return b.String(), injected
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
