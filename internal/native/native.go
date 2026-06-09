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
	Slug        string // path relative to its memory dir, e.g. "feedback_x.md"
	Path        string // absolute path
	Name        string // frontmatter: name
	Description string // frontmatter: description
	Type        string // frontmatter: type — native bucket (non-exhaustive); fine-grained type lives in the sidecar
	ContentSHA  string // sha256 of full file bytes — rename/edit detection
	Body        string // markdown body, frontmatter stripped
	Project     string // owning store: cwd slug or GlobalProject — set by Collect, not parsed from the file
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
		ContentSHA:  hex.EncodeToString(sum[:]),
		Body:        body,
	}, nil
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
	for _, ln := range lines[1:end] {
		ln = strings.TrimRight(ln, "\r")
		idx := strings.IndexByte(ln, ':')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(ln[:idx])
		val := strings.Trim(strings.TrimSpace(ln[idx+1:]), `"'`)
		fm[key] = val
	}
	return fm, strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
}
