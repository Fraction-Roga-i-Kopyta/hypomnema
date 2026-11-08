package doctor

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/inject"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/native"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/sidecar"
	"github.com/Fraction-Roga-i-Kopyta/hypomnema/internal/wal"
)

// candidateSilentMin is how many silent sessions (with zero useful ones) a
// candidate accrues before doctor flags it as never-corroborated.
const candidateSilentMin = 5

// checkCandidates flags candidate facts that keep injecting without ever
// being applied: ≥candidateSilentMin cite-silent sessions and no
// cite-useful / candidate-confirmed. Suggests retire or a keyword
// rewrite. WARN-only — corroboration failure is advice, not breakage.
func checkCandidates(memoryDir, claudeHome string, st native.Store) Check {
	type tally struct {
		useful, silent int
		confirmed      bool
	}
	// Keyed by the qualified project\x1fslug when the target carries a
	// project (bare slug only as legacy fallback) — a same-basename fact in
	// another project must not pollute this candidate's tally.
	byKey := map[string]*tally{}
	if f, err := os.Open(filepath.Join(memoryDir, ".wal")); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
		seen := map[string]bool{} // slug+session+kind dedup (close fires per turn)
		for sc.Scan() {
			parts := strings.SplitN(strings.TrimRight(sc.Text(), "\r"), "|", 4)
			if len(parts) != 4 {
				continue
			}
			event := parts[1]
			// cite-none's target is the session id, not a fact slug — keying
			// it would create a phantom per-key tally under that session id,
			// and cite-undelivered (a real qualified slug, but diagnostic
			// only) must not create or contribute to a tally either. Skip
			// both before any key is computed.
			if event == "cite-none" || event == "cite-undelivered" {
				continue
			}
			project, slug, qualified := native.ParseQKey(parts[2])
			slug = strings.TrimSuffix(slug, ".md")
			key := slug
			if qualified {
				key = native.QKey(project, slug)
			}
			t := byKey[key]
			if t == nil {
				t = &tally{}
				byKey[key] = t
			}
			switch event {
			case "cite-useful":
				if k := key + "\x00" + wal.ParentSession(parts[3]) + "\x00u"; !seen[k] {
					seen[k] = true
					t.useful++
				}
			case "cite-silent":
				if k := key + "\x00" + wal.ParentSession(parts[3]) + "\x00s"; !seen[k] {
					seen[k] = true
					t.silent++
				}
			case "candidate-confirmed":
				t.confirmed = true
			}
		}
	}
	var flagged []string
	for _, mf := range native.Collect(claudeHome, st) {
		if mf.Status != "candidate" {
			continue
		}
		bare := strings.TrimSuffix(mf.Slug, ".md")
		t := byKey[native.QKey(mf.Project, bare)]
		if t == nil {
			t = byKey[bare] // legacy bare-slug events
		}
		if t != nil && !t.confirmed && t.useful == 0 && t.silent >= candidateSilentMin {
			flagged = append(flagged, bare)
		}
	}
	sort.Strings(flagged)
	if len(flagged) == 0 {
		return Check{Name: "candidate_corroboration", Status: OK,
			Detail: "no uncorroborated candidates"}
	}
	return Check{Name: "candidate_corroboration", Status: WARN,
		Detail: fmt.Sprintf("%d candidate(s) never corroborated after ≥%d silent sessions: %s — retire or rewrite keywords",
			len(flagged), candidateSilentMin, strings.Join(flagged, ", ")),
		Extra: map[string]interface{}{"slugs": flagged}}
}

// checkCorpus enumerates the v2 native corpus (per-project + global, merged
// by native.Collect) and reports counts by logical type. The type is the
// singular `type:` frontmatter value (mistake, strategy, …), not a directory.
//
// Native files carry NO status: in v2 status lives only in the sidecar, so
// pinned/stale totals are looked up from the sidecar (.sidecar.db) by slug.
// A native file with no matching sidecar row defaults to "active". The lookup
// is fully graceful: a missing or unreadable sidecar yields 0 pinned / 0 stale
// and never fails the check. Sidecar rows whose status is "deleted" are
// orphans (no live native file) and contribute nothing to the corpus counts.
func checkCorpus(claudeDir, memoryDir string, st native.Store) Check {
	files := native.Collect(claudeDir, st)
	statusBySlug := sidecarStatusMap(memoryDir)
	counts := map[string]int{}
	pinned, stale := 0, 0
	for _, f := range files {
		typ := f.Type
		if typ == "" {
			typ = "untyped"
		}
		counts[typ]++
		status := statusBySlug[f.Slug]
		if status == "" {
			status = "active"
		}
		switch status {
		case "pinned":
			pinned++
		case "stale":
			stale++
		}
	}
	total := len(files)
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{}
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", k, counts[k]))
	}
	detail := fmt.Sprintf("%d total (%s); %d pinned, %d stale",
		total, strings.Join(parts, " "), pinned, stale)
	if stale > 0 {
		detail += " — consider `memoryctl retire <slug>` for stale facts that should not return"
	}
	status := OK
	if total == 0 {
		status = WARN
		detail = "corpus is empty — hypomnema has nothing to inject yet"
	}
	return Check{Name: "corpus_counts", Status: status, Detail: detail}
}

// sidecarStatusMap opens the sidecar at memoryDir/.sidecar.db and returns a
// slug→status map for every live row. Rows with status "deleted" are skipped
// (orphans whose native file is gone). Fully graceful: a missing or unreadable
// sidecar returns an empty (non-nil) map so the caller can default every native
// file to "active" without crashing or failing the check.
func sidecarStatusMap(memoryDir string) map[string]string {
	out := map[string]string{}
	dbPath := filepath.Join(memoryDir, ".sidecar.db")
	// doctor is strictly read-only — do not let sidecar.Open create an empty
	// DB as a side effect when none exists. Treat absence as "no statuses".
	if _, err := os.Stat(dbPath); err != nil {
		return out
	}
	s, err := sidecar.Open(dbPath)
	if err != nil {
		return out
	}
	defer s.Close()
	recs, err := s.All()
	if err != nil {
		return out
	}
	for _, r := range recs {
		if r.Status == "deleted" {
			continue
		}
		out[r.Slug] = r.Status
	}
	return out
}

// fmStatus reports whether the first frontmatter block declares a `status:`
// key and, if so, its (possibly empty) value. Distinguishing "absent" from
// "present but empty" is the whole point. Stops at the second `---` sentinel.
func fmStatus(path string) (present bool, value string) {
	f, err := os.Open(path)
	if err != nil {
		return false, ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8*1024), 64*1024)
	fmCount := 0
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimRight(line, " \t\r") == "---" {
			fmCount++
			if fmCount >= 2 {
				break
			}
			continue
		}
		if fmCount != 1 {
			continue
		}
		if strings.HasPrefix(line, "status:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "status:"))
			v = strings.Trim(v, `"'`)
			return true, v
		}
	}
	return false, ""
}

// fmHasNestedMetadata reports whether the file's frontmatter opens a
// column-0 `metadata:` block (Claude Code's native nesting). The parser
// promotes its children, so this is observability, not a defect.
func fmHasNestedMetadata(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8*1024), 64*1024)
	fmCount := 0
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimRight(line, " \t\r") == "---" {
			fmCount++
			if fmCount >= 2 {
				break
			}
			continue
		}
		if fmCount != 1 {
			continue
		}
		if strings.TrimRight(line, " \t\r") == "metadata:" {
			return true
		}
	}
	return false
}

// checkCorpusQuality surfaces frontmatter issues the ranking pipeline cannot
// act on but the user probably didn't mean. In v2 the only such issue is a
// `status:` line present but empty — the status filter then drops the file
// silently. (The v1 "feedback/knowledge without triggers:" nudge is gone:
// triggers are retired in v2; the ranker treats all tokens as relevance
// signal, so there is no reactive-trigger requirement to warn about.)
//
// Always WARN-level — a nudge, never a hard failure.
func checkCorpusQuality(claudeDir string, st native.Store) Check {
	const name = "corpus_frontmatter_quality"
	var emptyStatus []string
	nested := 0
	for _, f := range native.Collect(claudeDir, st) {
		present, value := fmStatus(f.Path)
		if present && value == "" {
			emptyStatus = append(emptyStatus, f.Slug)
		}
		if fmHasNestedMetadata(f.Path) {
			nested++
		}
	}
	sort.Strings(emptyStatus)

	status := OK
	detail := "all files have valid frontmatter"
	hint := ""
	if len(emptyStatus) > 0 {
		status = WARN
		detail = fmt.Sprintf("%d with empty status:", len(emptyStatus))
		hint = "set status: to 'active' (or remove the line) — an empty status: drops the file from injection"
	}

	extra := map[string]interface{}{
		"empty_status_count":    len(emptyStatus),
		"nested_metadata_count": nested,
	}
	if hint != "" {
		extra["hint"] = hint
	}
	// Surface sample filenames (capped) so the operator knows where to
	// look without running a separate grep.
	const sampleCap = 5
	if len(emptyStatus) > 0 {
		if len(emptyStatus) > sampleCap {
			extra["empty_status_sample"] = emptyStatus[:sampleCap]
		} else {
			extra["empty_status_sample"] = emptyStatus
		}
	}

	return Check{
		Name:   name,
		Status: status,
		Detail: detail,
		Extra:  extra,
	}
}

// checkOversized flags facts whose body exceeds the per-record injection cap:
// the model only ever sees the first inject.MaxBodyBytes of such a fact (a
// header and a truncation marker), so the fact should be split or retired.
func checkOversized(claudeDir string, st native.Store) Check {
	const name = "oversized_facts"
	type item struct {
		slug string
		size int
	}
	var over []item
	for _, f := range native.Collect(claudeDir, st) {
		if n := len(f.Body); n > inject.MaxBodyBytes {
			over = append(over, item{f.Slug, n})
		}
	}
	if len(over) == 0 {
		return Check{Name: name, Status: OK,
			Detail: fmt.Sprintf("all bodies within the %d B injection cap", inject.MaxBodyBytes)}
	}
	sort.Slice(over, func(i, j int) bool {
		if over[i].size != over[j].size {
			return over[i].size > over[j].size
		}
		return over[i].slug < over[j].slug
	})
	top := over
	if len(top) > 5 {
		top = top[:5]
	}
	parts := make([]string, 0, len(top))
	for _, it := range top {
		parts = append(parts, fmt.Sprintf("%s (%d B)", it.slug, it.size))
	}
	return Check{
		Name:   name,
		Status: WARN,
		Detail: fmt.Sprintf("%d fact(s) exceed the %d B injection cap and inject truncated: %s — split them into focused facts, or retire the journal-like ones (the truncation marker names the file path for the full text)",
			len(over), inject.MaxBodyBytes, strings.Join(parts, ", ")),
		Extra: map[string]interface{}{"oversized_count": len(over), "cap_bytes": inject.MaxBodyBytes},
	}
}
