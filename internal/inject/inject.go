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

// CiteInstruction is the verbatim line every delivery path (inject render,
// recall, skill-inject) shows exactly once: it tells the model how to mark a
// fact as actually used, by wrapping the sentence it changed in a
// <cc-memory filenames="FILE">…</cc-memory> tag. Claude Code hides the tag
// from the user; the close hook parses it as the honest usefulness signal
// (internal/closer/cite.go) in place of substring matching. It counts inside
// the render budget like any other rendered text.
const CiteInstruction = `When a fact below changes what you say or do, wrap that sentence in <cc-memory filenames="FILE">…</cc-memory> (the tag is hidden from the user).`

// FactHeader renders one fact's citation-ready header line. The file name
// (Slug) is always present — it is the only thing the model can put inside
// filenames="…" to cite this exact fact. Type falls back to "note" and name
// falls back to the slug when frontmatter omits them; the created date is
// appended only when known.
func FactHeader(f native.MemFile) string {
	name := f.Name
	if name == "" {
		name = f.Slug
	}
	typ := f.Type
	if typ == "" {
		typ = "note"
	}
	if f.Created == "" {
		return fmt.Sprintf("## %s — %s (%s)", name, f.Slug, typ)
	}
	return fmt.Sprintf("## %s — %s (%s, %s)", name, f.Slug, typ, f.Created)
}

// CapBody bounds one record body to maxBytes at a UTF-8 rune boundary with a
// visible truncation marker that names the total size and, via hint, where
// the full text lives (PathHint: "full text: <absolute path>"). Exported for
// the recall and skill-inject verbs.
func CapBody(body string, maxBytes int, hint string) string { return capBody(body, maxBytes, hint) }

// pathHint is the truncation hint for a native file: its absolute path (the
// only pointer that always yields the full text). Empty when the file has no
// path (synthetic candidates in tests).
func pathHint(f native.MemFile) string {
	if f.Path == "" {
		return ""
	}
	return "full text: " + f.Path
}

// PathHint exports pathHint for the recall and skill-inject verbs, which
// render outside this package but must pass the same hint render does.
func PathHint(f native.MemFile) string { return pathHint(f) }

// Candidates assembles ranker-ready candidates for an ad-hoc query against
// the project+global scope — the same sidecar-backed assembly (self-healing
// reproject, degraded native-only fallback) the injection pipeline uses.
// Exported for the recall verb (pull ignores holdout: an explicit query
// answers regardless of an ablation in flight).
func Candidates(memoryDir string, st native.Store, files []native.MemFile, terms []string) []rank.Candidate {
	c, _ := candidates(Input{MemoryDir: memoryDir}, st, files, terms)
	return c
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
	AlreadyInjected []string // slugs already injected this session — not re-rendered while still in the current context
	// Store is the resolved native store. Zero value → resolved from CWD
	// (tests and legacy callers); the memoryctl verb always sets it.
	Store native.Store
	// HoldoutSession lists slugs already withheld earlier THIS session.
	// Holdout is session-sticky: the Stop hook re-derives holdout_remaining
	// per turn, so a fact whose budget hit zero on this session's first
	// prompt would otherwise re-inject on the second and contaminate its
	// own final observation. These slugs stay withheld regardless of the
	// sidecar's current budget.
	HoldoutSession []string
	// NoGitSignal drops cwd's git context from the query terms — for a
	// caller whose prompt already names the task (a subagent launched with
	// its own Agent call), where the parent's branch and working-tree tokens
	// are unrelated noise.
	NoGitSignal bool
}

func (in Input) store() native.Store {
	if in.Store.Dir != "" {
		return in.Store
	}
	return native.StoreFor(in.ClaudeHome, in.CWD)
}

// Result is the rendered context plus the slugs injected.
type Result struct {
	Markdown string
	Injected []string
	// ProjectBySlug maps each injected slug to its owning project (the
	// resolved store's project tag, or GlobalProject), so the caller can
	// write project-qualified WAL events. Project-local wins over global on
	// a basename tie — matching scopeRecords' preference. Covers
	// HoldoutSkipped slugs too.
	ProjectBySlug map[string]string
	// HoldoutSkipped lists held-out facts that ranked inside the top-K and
	// were withheld (would-have-injected).
	HoldoutSkipped []string
	// HoldoutRemaining is each skipped fact's remaining session budget as of
	// this ranking (BEFORE the current session is counted).
	HoldoutRemaining map[string]int
}

// Run orchestrates a single injection: keywords → native files → ranked
// top-K → Markdown. Recovers from a corrupt sidecar by rebuilding; falls
// back to a native-only degraded rank if the sidecar is unusable.
func Run(in Input) (Result, error) {
	st := in.store()
	files := native.Collect(in.ClaudeHome, st)
	if len(files) == 0 {
		return Result{}, nil
	}
	bySlug := map[string]native.MemFile{}
	for _, f := range files {
		bySlug[f.Slug] = f
	}
	terms := keywords(in.CWD, in.Prompt, !in.NoGitSignal)
	cands, held := candidates(in, st, files, terms)
	if held == nil {
		held = map[string]int{} // defense in depth — candidates never returns nil
	}
	for _, s := range in.HoldoutSession {
		if held[s] == 0 {
			held[s] = 1 // session-sticky: withheld earlier this session stays withheld
		}
	}
	if in.MaxK <= 0 {
		in.MaxK = 8
	}
	rankedAll := rank.Rank(rank.Query{
		Terms: terms, Today: in.Today, Project: st.Project,
	}, cands, 0)
	// Top-K with holdout refill: a held-out fact inside the window is
	// withheld (recorded as would-have-injected) and the next-ranked fact
	// takes its slot — the injection budget stays full during an ablation.
	var ranked []rank.Scored
	var skipped []string
	for _, sc := range rankedAll {
		if len(ranked) >= in.MaxK {
			break
		}
		if held[sc.Slug] > 0 {
			skipped = append(skipped, sc.Slug)
			continue
		}
		ranked = append(ranked, sc)
	}
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
	projectSlug := st.Project
	localSlug := map[string]bool{}
	for _, f := range files {
		if f.Project == projectSlug {
			localSlug[f.Slug] = true
		}
	}
	projectBySlug := make(map[string]string, len(injected)+len(skipped))
	for _, slug := range append(append([]string{}, injected...), skipped...) {
		// Prefer the project-local owner over global on a basename tie
		// (matches scopeRecords / bySlug rendering preference).
		if localSlug[slug] {
			projectBySlug[slug] = projectSlug
		} else if f, ok := bySlug[slug]; ok {
			projectBySlug[slug] = f.Project
		}
	}
	res := Result{Markdown: md, Injected: injected, ProjectBySlug: projectBySlug,
		HoldoutSkipped: skipped, HoldoutRemaining: map[string]int{}}
	for _, s := range skipped {
		res.HoldoutRemaining[s] = held[s]
	}
	return res, nil
}

// candidates returns ranker-ready candidates plus the holdout budgets
// (slug → HoldoutRemaining) for rows under an active ablation. Both return
// values are always non-nil-safe for writing: the degraded native-only path
// returns an empty map (ablation quietly suspends without a sidecar —
// best-effort posture), never nil.
func candidates(in Input, st native.Store, files []native.MemFile, terms []string) ([]rank.Candidate, map[string]int) {
	sidePath := filepath.Join(in.MemoryDir, ".sidecar.db")
	walPath := filepath.Join(in.MemoryDir, ".wal")
	projectSlug := st.Project

	s, err := sidecar.Open(sidePath)
	if err != nil && shouldWipeSidecar(err) {
		// Only wipe on genuine CORRUPTION. A transient error (e.g. SQLITE_BUSY
		// from a concurrent Stop-hook creating the schema) must NOT delete the
		// file — that would destroy a live sidecar out from under the other
		// process (review R2). SQLite state spans three files; removing only the
		// main DB can leave a poisoned -wal/-shm, so wipe all three.
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
			if rerr := sidecar.Reproject(s, files, walPath, st.Scope()); rerr == nil {
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
			// A failed overlap query must degrade to no-overlap, never crash the
			// hook; chunking (review E6) keeps the common >32766-term case from
			// erroring in the first place.
			overlap, oerr := s.OverlapScores(terms)
			if oerr != nil {
				overlap = map[string]int{}
			}
			out := make([]rank.Candidate, 0, len(scoped))
			held := map[string]int{}
			for _, r := range scoped {
				if r.HoldoutRemaining > 0 {
					held[r.Slug] = r.HoldoutRemaining
				}
				out = append(out, rank.Candidate{
					Slug: r.Slug, Type: r.Type, Project: r.Project,
					Domains: splitCSV(r.Domains), RefCount: r.RefCount,
					Effectiveness: r.Effectiveness, Status: r.Status,
					Created: r.Created, LastInjected: r.LastInjected,
					LastUseful: r.LastUseful,
					Overlap:    overlap[r.Slug],
				})
			}
			return out, held
		}
	}
	return degradedCandidates(files, terms), map[string]int{}
}

// shouldWipeSidecar reports whether a sidecar.Open error indicates genuine
// on-disk corruption (safe to delete + rebuild) versus a transient/contention
// error (must be retried, never deleted — the file may be a live DB another
// process is mid-write on).
func shouldWipeSidecar(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, sig := range []string{"not a database", "malformed", "sqlite_corrupt", "sqlite_notadb", "file is encrypted"} {
		if strings.Contains(msg, sig) {
			return true
		}
	}
	return false
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
		for _, tok := range tokenize.Relevance(text) {
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
	b.WriteString(CiteInstruction + "\n")
	var injected []string
	for _, sc := range ranked {
		f, ok := bySlug[sc.Slug]
		if !ok {
			// A sidecar row whose native file vanished before reconciliation
			// (e.g. the file was deleted between rank and render) — the row
			// still deserves a header, and the header still needs the file
			// name to be citable.
			f.Slug = sc.Slug
		}
		head := "\n" + FactHeader(f) + "\n"
		hint := pathHint(f)
		body := ""
		if f.Body != "" {
			body = capBody(f.Body, maxBody, hint) + "\n"
		}
		if maxTotal > 0 && b.Len()+len(head)+len(body) > maxTotal {
			if len(injected) > 0 {
				break
			}
			room := maxTotal - b.Len() - len(head) - 20 // marker + newline slack
			if room <= 0 {
				break
			}
			body = capBody(f.Body, room, hint) + "\n"
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
// visible marker so a truncated hint reads as truncated. The marker names the
// body's total size and the hint (how to get the rest) and is counted INSIDE
// maxBytes so a capped record never exceeds its budget; only a budget too
// small to hold the marker falls back to the legacy short marker appended
// after the cut.
func capBody(body string, maxBytes int, hint string) string {
	if maxBytes <= 0 || len(body) <= maxBytes {
		return body
	}
	marker := fmt.Sprintf("\n\n…(truncated — %d B total)", len(body))
	if hint != "" {
		marker = fmt.Sprintf("\n\n…(truncated — %d B total; %s)", len(body), hint)
	}
	cut := maxBytes - len(marker)
	if cut <= 0 {
		marker = "\n\n…(truncated)"
		cut = maxBytes
	}
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return strings.TrimRight(body[:cut], " \n") + marker
}
