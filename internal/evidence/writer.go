package evidence

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AppendToFrontmatter adds `phrases` to the `evidence:` block of the
// memory file at `path`. If the block doesn't exist, it's created
// just before the closing `---`. If the file has no frontmatter, no
// change is made (returning an error — the caller shouldn't be asked
// to mine evidence for a non-memory file).
//
// Write is atomic via tmp+rename. Existing phrases are preserved
// verbatim; new ones are appended in the given order, each on its
// own line at the same 2-space indent as the existing items. No
// other frontmatter field is touched — minimises diff noise.
func AppendToFrontmatter(path string, phrases []string) error {
	if len(phrases) == 0 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated, err := injectPhrases(string(data), phrases)
	if err != nil {
		return err
	}
	if updated == string(data) {
		return nil // nothing to change (all phrases already present)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".evidence-append.*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.WriteString(updated); err != nil {
		tmp.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename tmp: %w", err)
	}
	cleanup = false
	return nil
}

// injectPhrases is the pure string transformation at the core of
// AppendToFrontmatter. Splitting it out keeps the file-I/O layer
// thin and lets tests cover edge cases without temp dirs.
func injectPhrases(src string, phrases []string) (string, error) {
	lines := strings.Split(src, "\n")

	// Locate frontmatter delimiters (first two `---` lines at
	// column 0).
	openIdx, closeIdx := -1, -1
	for i, line := range lines {
		if line == "---" {
			if openIdx < 0 {
				openIdx = i
			} else if closeIdx < 0 {
				closeIdx = i
				break
			}
		}
	}
	if openIdx < 0 || closeIdx < 0 {
		return "", fmt.Errorf("no frontmatter (expected two `---` delimiters)")
	}

	// Find `evidence:` key if present, and the line where its list
	// items end (last `  - ...` before a non-list line or closeIdx).
	evidenceIdx := -1
	for i := openIdx + 1; i < closeIdx; i++ {
		if strings.HasPrefix(lines[i], "evidence:") {
			evidenceIdx = i
			break
		}
	}

	existingSet := collectExisting(lines, evidenceIdx, closeIdx)
	var newPhrases []string
	for _, p := range phrases {
		canon := strings.ToLower(strings.TrimSpace(p))
		if canon == "" {
			continue
		}
		if _, dup := existingSet[canon]; dup {
			continue
		}
		existingSet[canon] = struct{}{}
		newPhrases = append(newPhrases, p)
	}
	if len(newPhrases) == 0 {
		return src, nil
	}

	// Each phrase on its own line, formatted `  - "phrase"`. Escape
	// embedded `"` by backslash — YAML accepts this under double
	// quotes.
	insertion := make([]string, 0, len(newPhrases))
	for _, p := range newPhrases {
		p = strings.ReplaceAll(p, `"`, `\"`)
		insertion = append(insertion, `  - "`+p+`"`)
	}

	// Both branches below splice new lines into `lines` at a specific
	// index. Appending to a subslice of `lines` would alias the
	// underlying array and corrupt the `tail` half — build a fresh
	// slice for the merged result instead.
	var insertAt int
	var newHeader []string
	if evidenceIdx < 0 {
		// No evidence block — create one immediately before the
		// closing `---`. Splice in "evidence:" + items.
		insertAt = closeIdx
		newHeader = []string{"evidence:"}
	} else {
		// Evidence block exists — scan forward until we hit the
		// first non-list, non-continuation line. Insert there.
		insertAt = evidenceIdx + 1
		for i := evidenceIdx + 1; i < closeIdx; i++ {
			t := strings.TrimRight(lines[i], " \t")
			if strings.HasPrefix(t, "  -") || (strings.HasPrefix(t, "  ") && t != "") {
				insertAt = i + 1
				continue
			}
			if t == "" {
				continue
			}
			break
		}
	}

	merged := make([]string, 0, len(lines)+len(newHeader)+len(insertion))
	merged = append(merged, lines[:insertAt]...)
	merged = append(merged, newHeader...)
	merged = append(merged, insertion...)
	merged = append(merged, lines[insertAt:]...)
	return strings.Join(merged, "\n"), nil
}

// collectExisting reads the evidence: block's existing phrases into
// a lowercase set for dedup. Returns empty set if no evidence block.
func collectExisting(lines []string, evidenceIdx, closeIdx int) map[string]struct{} {
	out := map[string]struct{}{}
	if evidenceIdx < 0 {
		return out
	}
	for i := evidenceIdx + 1; i < closeIdx; i++ {
		val, ok := ParseYAMLListItem(lines[i])
		if !ok {
			// First non-list line ends the block (as in YAML). Blank
			// lines and indented continuation are tolerated.
			if strings.TrimSpace(lines[i]) != "" && !strings.HasPrefix(lines[i], "  ") {
				return out
			}
			continue
		}
		out[strings.ToLower(val)] = struct{}{}
	}
	return out
}

// --- rejection persistence ---

// RejectionsPath returns the canonical location for a slug's
// per-target rejection file.
func RejectionsPath(memoryDir, slug string) string {
	return filepath.Join(memoryDir, ".runtime", "evidence-rejections", slug+".list")
}

// LoadRejections reads the per-target rejection list (lowercase,
// one phrase per line). Missing file → empty set, not an error.
func LoadRejections(path string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		out[strings.ToLower(line)] = struct{}{}
	}
	return out, sc.Err()
}

// AppendRejection persists one rejected phrase. Creates the parent
// directory and file on first use.
func AppendRejection(path, phrase string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.ToLower(strings.TrimSpace(phrase)) + "\n")
	return err
}
