package native

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MemFile is one native memory file: minimal frontmatter + body + identity.
type MemFile struct {
	Slug        string   // path relative to its memory dir, e.g. "feedback_x.md"
	Path        string   // absolute path
	Name        string   // frontmatter: name
	Description string   // frontmatter: description
	Type        string   // frontmatter: type — native bucket (non-exhaustive); fine-grained type lives in the sidecar
	Created     string   // frontmatter: created (YYYY-MM-DD) — author's recency claim
	Status      string   // frontmatter: status — only "pinned" is honoured; lifecycle states are sidecar-owned
	Keywords    []string // frontmatter: keywords — explicit relevance signal
	Domains     []string // frontmatter: domains — domain filter tags
	Evidence    []string // frontmatter: evidence — phrases that mark the rule as applied
	ContentSHA  string   // sha256 of full file bytes — rename/edit detection
	Body        string   // markdown body, frontmatter stripped
	Project     string   // owning store: cwd slug or GlobalProject — set by Collect, not parsed from the file
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
	return MemFile{
		Slug:        filepath.Base(path),
		Path:        path,
		Name:        fm["name"],
		Description: fm["description"],
		Type:        fm["type"],
		Created:     fm["created"],
		Status:      fm["status"],
		Keywords:    splitList(fm["keywords"]),
		Domains:     splitList(fm["domains"]),
		Evidence:    splitList(fm["evidence"]),
		ContentSHA:  hex.EncodeToString(sum[:]),
		Body:        body,
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
// an empty map + the whole content as body.
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
		idx := strings.IndexByte(ln, ':')
		if idx <= 0 {
			listKey = ""
			continue
		}
		key := strings.TrimSpace(ln[:idx])
		val := strings.Trim(strings.TrimSpace(ln[idx+1:]), `"'`)
		fm[key] = val
		if val == "" {
			listKey = key
		} else {
			listKey = ""
		}
	}
	return fm, strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
}
