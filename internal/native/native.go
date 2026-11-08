package native

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MemFile is one native memory file: minimal frontmatter + body + identity.
type MemFile struct {
	Slug        string   // path relative to its memory dir, e.g. "feedback_x.md"
	Path        string   // absolute path
	Name        string   // frontmatter: name
	Description string   // frontmatter: description
	Type        string   // frontmatter: type — native bucket (non-exhaustive); fine-grained type lives in the sidecar
	Created     string   // frontmatter: created (YYYY-MM-DD) — author's recency claim; falls back to Modified
	Modified    string   // date part of Claude Code's `modified` stamp (set on every Write/Edit), YYYY-MM-DD
	Status      string   // frontmatter: status — only "pinned" is honoured; lifecycle states are sidecar-owned
	Keywords    []string // frontmatter: keywords — explicit relevance signal
	Domains     []string // frontmatter: domains — domain filter tags
	Skill       string   // frontmatter: skill — binds a skill-learning to a skill name
	// PrecisionClass — frontmatter: precision_class. "ambient" marks a rule that
	// shapes behaviour silently (language pref, security baseline, meta-policy);
	// such rules are not penalised in effectiveness for going uncited.
	PrecisionClass string
	Retired        string // frontmatter: retired (YYYY-MM-DD) — tombstone stamp, set by `memoryctl retire`
	RetireReason   string // frontmatter: retire-reason
	SupersededBy   string // frontmatter: superseded-by — canonical successor (slug or free text)
	ContentSHA     string // sha256 of full file bytes — rename/edit detection
	Body           string // markdown body, frontmatter stripped
	Project        string // owning store: cwd slug or GlobalProject — set by Collect, not parsed from the file
}

// List enumerates *.md files directly under dir (non-recursive) and parses
// each. A missing dir yields (nil, nil) — a fresh install simply has no
// memory. MEMORY.md (the harness index) and non-.md files are skipped.
// Individual unreadable files are skipped, never fatal.
func List(dir string) ([]MemFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("native.List: read dir: %w", err)
	}
	var out []MemFile
	for _, e := range entries {
		if e.IsDir() || e.Name() == "MEMORY.md" || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		mf, err := parseFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // skip unreadable, never fatal
		}
		out = append(out, mf)
	}
	return out, nil
}

func parseFile(path string) (MemFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return MemFile{}, fmt.Errorf("native.parseFile: %w", err)
	}
	fm, body := splitFrontmatter(string(raw))
	sum := sha256.Sum256(raw)
	modified := stampDate(fm["modified"])
	created := fm["created"]
	if created == "" {
		// Claude Code writes `modified` on every Write/Edit and never
		// `created`: without the fallback a harness-written fact had no date
		// at all, so it never aged and scored no recency.
		created = modified
	}
	return MemFile{
		Slug:           filepath.Base(path),
		Path:           path,
		Name:           fm["name"],
		Description:    fm["description"],
		Type:           fm["type"],
		Created:        created,
		Modified:       modified,
		Status:         fm["status"],
		Keywords:       splitList(fm["keywords"]),
		Domains:        splitList(fm["domains"]),
		Skill:          fm["skill"],
		PrecisionClass: fm["precision_class"],
		Retired:        fm["retired"],
		RetireReason:   fm["retire-reason"],
		SupersededBy:   fm["superseded-by"],
		ContentSHA:     hex.EncodeToString(sum[:]),
		Body:           body,
	}, nil
}

// splitList parses a frontmatter list value: inline "[a, b]" or the
// comma-joined form splitFrontmatter accumulates for block-style lists.
// A bare scalar yields a single-element list.
func splitList(v string) []string {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(v), "["), "]"))
	if v == "" {
		return nil
	}
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.Trim(strings.TrimSpace(item), `"'`); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// splitFrontmatter parses a leading `---`-fenced YAML-ish block into a flat
// key->value map and returns the trimmed body. Tolerates a UTF-8 BOM, CRLF
// endings, quoted scalars, and trailing whitespace on the closing fence —
// matching the existing bash parser's tolerances. An unterminated fence yields
// an empty map + the whole content as body. Children of a top-level
// "metadata:" key are promoted to top-level keys (Claude Code's native memory
// instructions nest type/created/status there); a column-0 key wins on
// collision.
func splitFrontmatter(s string) (map[string]string, string) {
	fm := map[string]string{}
	s = strings.TrimPrefix(s, "\xef\xbb\xbf")
	if !strings.HasPrefix(s, "---") {
		return fm, strings.TrimSpace(s)
	}
	lines := strings.Split(s, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t\r") == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return fm, strings.TrimSpace(s)
	}
	listKey := "" // key whose block-style list items we are accumulating
	// inMeta/metaIndent track a `metadata:` block whose direct children are
	// promoted to top-level keys — Claude Code's native memory instructions
	// nest type/created/status there. Only `metadata` promotes; a top-level
	// key of the same name wins in either order.
	inMeta := false
	metaIndent := -1
	for _, ln := range lines[1:end] {
		ln = strings.TrimRight(ln, "\r")
		// Block-style list item under the pending key: collapse into the
		// same comma-joined form as an inline [a, b] list.
		if item, isItem := strings.CutPrefix(strings.TrimSpace(ln), "- "); isItem && listKey != "" {
			item = strings.Trim(strings.TrimSpace(item), `"'`)
			if fm[listKey] == "" {
				fm[listKey] = item
			} else {
				fm[listKey] += ", " + item
			}
			continue
		}
		if strings.TrimSpace(ln) == "" {
			continue // blank line: no-op, never closes a metadata block
		}
		indent := 0
		for indent < len(ln) && (ln[indent] == ' ' || ln[indent] == '\t') {
			indent++
		}
		if indent == 0 {
			inMeta, metaIndent = false, -1
		} else {
			// An INDENTED line that isn't a block-list item is continuation
			// content of a block scalar (`root-cause: |`, `description: >`) —
			// never a top-level key (review G2). The one exception is a direct
			// child of `metadata:`; deeper lines inside it are prose again.
			if !inMeta {
				continue
			}
			if metaIndent < 0 {
				metaIndent = indent
			}
			if indent != metaIndent {
				continue
			}
		}
		idx := strings.IndexByte(ln, ':')
		if idx <= 0 {
			listKey = ""
			continue
		}
		key := strings.TrimSpace(ln[:idx])
		val := strings.Trim(strings.TrimSpace(ln[idx+1:]), `"'`)
		if indent > 0 {
			// Promoted metadata child: never overwrite a column-0 key.
			if _, exists := fm[key]; exists {
				listKey = ""
				continue
			}
		}
		fm[key] = val
		if val == "" {
			listKey = key
			if indent == 0 && key == "metadata" {
				inMeta = true
			}
		} else {
			listKey = ""
		}
	}
	return fm, strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
}

// stampDate returns the local-zone date (YYYY-MM-DD) of an RFC 3339
// timestamp — Claude Code writes `modified` in UTC, and dates are compared
// with local created/today — or the YYYY-MM-DD prefix of anything else that
// starts with a date; "" otherwise.
func stampDate(v string) string {
	v = strings.TrimSpace(v)
	if ts, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return ts.In(time.Local).Format("2006-01-02")
	}
	if len(v) < 10 {
		return ""
	}
	if _, err := time.Parse("2006-01-02", v[:10]); err != nil {
		return ""
	}
	return v[:10]
}

// FreshnessDate is a fact's latest known date: the later of its created and
// modified dates (YYYY-MM-DD compares lexically).
func FreshnessDate(created, modified string) string {
	if modified > created {
		return modified
	}
	return created
}

// NewestContinuity returns, per project, the slug of its freshest continuity
// fact — the "where we left off" marker that is current. Older continuity
// files are history: they stay readable but are no longer pushed and age
// like notes. Ties go to the lexically greater slug, so dated names
// (continuity-2026-09-03.md) resolve to the latest.
func NewestContinuity(files []MemFile) map[string]string {
	best := map[string]MemFile{}
	for _, f := range files {
		if f.Type != "continuity" {
			continue
		}
		b, ok := best[f.Project]
		fd, bd := FreshnessDate(f.Created, f.Modified), FreshnessDate(b.Created, b.Modified)
		if !ok || fd > bd || (fd == bd && f.Slug > b.Slug) {
			best[f.Project] = f
		}
	}
	out := make(map[string]string, len(best))
	for p, f := range best {
		out[p] = f.Slug
	}
	return out
}
